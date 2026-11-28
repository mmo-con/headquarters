package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/jobs"
)

// StatusOptions select the repositories of a status collection.
type StatusOptions struct {
	Filter Filter
	// Fetch fetches the repositories that match Filter before the report,
	// like F in the dashboard.
	Fetch bool
}

// Status is the collected state of the selected repositories.
type Status struct {
	// Repositories are the entries that match the filter after any fetch,
	// in the configured sort order.
	Repositories []domain.Repository
	Warnings     []string
	// Fetched reports whether a fetch ran.
	Fetched bool
	ops     *Operations
	now     time.Time
}

// Health classifies r like the health column of the dashboard, including a
// failed fetch.
func (s *Status) Health(r *domain.Repository) Health { return s.ops.Health(r, s.now) }

// FetchResult returns the result of the fetch of r, if it was fetched.
func (s *Status) FetchResult(r *domain.Repository) (domain.OperationResult, bool) {
	return s.ops.LastResult(r.ID)
}

// StatusStates lists the states that a status check can test: the "is:"
// filter states, except marks, and unknown.
func StatusStates() []string {
	var out []string
	for _, k := range StateKeywords {
		if k != "is:marked" {
			out = append(out, strings.TrimPrefix(k, "is:"))
		}
	}
	return append(out, "unknown")
}

// ValidateStatusStates reports the first name that is not one of
// StatusStates.
func ValidateStatusStates(names []string) error {
	valid := StatusStates()
	for _, n := range names {
		if !slices.Contains(valid, n) {
			return fmt.Errorf("unknown state %q; use one of %s", n, strings.Join(valid, ", "))
		}
	}
	return nil
}

// Known reports whether the inspection determined the complete state of r:
// the entry is available and its inspection finished without an error.
func Known(r *domain.Repository) bool {
	return r.IsAvailable() && !r.Inspected.FinishedAt.IsZero() && r.Inspected.Err == nil &&
		r.Head.Valid && r.Operation.Valid && r.Worktree.Valid && r.Upstream.Valid
}

// Is reports whether r is in a state of StatusStates: "unknown" means that
// the state is not completely known (see Known), "error" also matches a
// failed fetch, and every other state matches like its "is:" filter token.
// All states use the time of the status, like Health.
func (s *Status) Is(r *domain.Repository, state string) bool {
	switch state {
	case "unknown":
		return !Known(r)
	case "error":
		if res, ok := s.FetchResult(r); ok && res.Status == domain.StatusFailed {
			return true
		}
	}
	return Filter{Text: "is:" + state, Now: s.now}.Matches(r, false)
}

// CollectStatus loads the inventory, inspects every available entry, and
// with opts.Fetch fetches the matching ones and inspects their stores again.
// It consumes the events of the scheduler until its batches are finished, so
// it must not run while another frontend of a consumes them. Cancelling ctx
// cancels the running jobs; their entries keep the state determined so far.
func (a *Application) CollectStatus(ctx context.Context, opts StatusOptions) Status {
	inv := a.LoadInventory(ctx)
	repos := inv.Repositories
	index := make(map[domain.RepositoryID]int, len(repos))
	for i, r := range repos {
		index[r.ID] = i
	}
	find := func(id domain.RepositoryID) *domain.Repository {
		if i, ok := index[id]; ok {
			return &repos[i]
		}
		return nil
	}

	d := newDriver(ctx, a.Scheduler)
	defer d.stop()

	var insp *Inspections
	insp = NewInspections(a, d.submit(func(ev jobs.Event) { insp.Handle(ev, find, time.Now()) }))
	inspect := func(rs []domain.Repository) {
		id := insp.Start(rs)
		d.wait(func() bool { return !insp.Running(id) })
		RefreshStoreFetchTimes(repos)
	}
	reinspect := map[domain.RepositoryID]bool{}
	var ops *Operations
	ops = NewOperations(a, d.submit(func(ev jobs.Event) {
		ids, _ := ops.Handle(ev)
		for _, id := range ids {
			reinspect[id] = true
		}
	}), time.Now)

	inspect(repos)
	st := Status{Warnings: inv.Warnings, ops: ops}
	if opts.Fetch && ctx.Err() == nil {
		if _, ok := ops.Fetch(repos, selectRepos(repos, opts.Filter)); ok {
			st.Fetched = true
			d.wait(ops.Batch().Done)
			if ctx.Err() != nil {
				return st
			}
			var ids []domain.RepositoryID
			for id := range reinspect {
				ids = append(ids, id)
			}
			inspect(lookup(repos, ids))
		}
	}

	st.now = time.Now()
	final := opts.Filter
	final.Now = st.now
	sel := selectRepos(repos, final)
	ptrs := make([]*domain.Repository, len(sel))
	for i := range sel {
		ptrs[i] = &sel[i]
	}
	key, _ := SortKeyNamed(a.Config.UI.Sort)
	Sort(ptrs, key, st.Health)
	for _, p := range ptrs {
		st.Repositories = append(st.Repositories, *p)
	}
	return st
}

func selectRepos(repos []domain.Repository, f Filter) []domain.Repository {
	var out []domain.Repository
	for i := range repos {
		if f.Matches(&repos[i], false) {
			out = append(out, repos[i])
		}
	}
	return out
}

// driver delivers the scheduler events of the batches it started to their
// handlers on the calling goroutine, which owns the services that keep batch
// state. Cancelling its context cancels the running batches and every batch
// started afterward.
type driver struct {
	ctx       context.Context
	scheduler *jobs.Scheduler
	routes    map[uint64]func(jobs.Event)
	stop      func() bool
}

func newDriver(ctx context.Context, s *jobs.Scheduler) *driver {
	return &driver{ctx: ctx, scheduler: s, routes: map[uint64]func(jobs.Event){},
		stop: context.AfterFunc(ctx, s.CancelAll)}
}

// submit returns a function that starts a batch whose events go to h.
func (d *driver) submit(h func(jobs.Event)) func([]jobs.Job) uint64 {
	return func(js []jobs.Job) uint64 {
		id := d.scheduler.Submit(js)
		d.routes[id] = h
		// The context may be cancelled before AfterFunc has run CancelAll,
		// which cancels later batches itself.
		if d.ctx.Err() != nil {
			d.scheduler.Cancel(id)
		}
		return id
	}
}

// wait handles events until done reports true or the queue is closed.
func (d *driver) wait(done func() bool) {
	for !done() {
		evs := d.scheduler.Events.Next()
		if evs == nil {
			return
		}
		for _, ev := range evs {
			if h := d.routes[ev.BatchID]; h != nil {
				h(ev)
			}
		}
	}
}
