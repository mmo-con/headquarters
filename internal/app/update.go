package app

import (
	"context"
	"fmt"
	"time"

	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	"github.com/mmo-con/headquarters/internal/jobs"
)

// Verdict is the outcome of the safety check of the safe update: the
// repository is eligible, already up to date, or blocked for a reason.
type Verdict struct {
	kind   verdictKind
	reason string
}

type verdictKind int

const (
	verdictEligible verdictKind = iota
	verdictUpToDate
	verdictBlocked
)

// Blocked returns a verdict that keeps a repository from being updated.
func Blocked(reason string) Verdict { return Verdict{kind: verdictBlocked, reason: reason} }

var upToDate = Verdict{kind: verdictUpToDate, reason: "already up to date"}

// Eligible reports whether the repository can be fast-forwarded.
func (v Verdict) Eligible() bool { return v.kind == verdictEligible }

// UpToDate reports whether the repository needs no update.
func (v Verdict) UpToDate() bool { return v.kind == verdictUpToDate }

// String describes why the repository is not updated; it is empty for an
// eligible repository.
func (v Verdict) String() string { return v.reason }

// Eligibility checks every safety condition of the safe update.
func Eligibility(r domain.Repository, storeBusy bool) Verdict {
	switch {
	case !r.IsAvailable():
		return Blocked("repository is " + r.Availability.String())
	case r.Inspected.Err != nil || !r.Head.Valid || !r.Worktree.Valid || !r.Upstream.Valid || !r.Operation.Valid:
		return Blocked("state is unknown")
	case r.Operation.Value != domain.NoOperation:
		return Blocked(r.Operation.Value.String() + " in progress")
	case r.Worktree.Value.OnlySubmoduleCommits():
		return Blocked("submodules not updated; run git submodule update")
	case r.Worktree.Value.HasTrackedChanges():
		return Blocked("worktree contains local changes")
	case r.Head.Value.Detached:
		return Blocked("detached HEAD")
	case r.Head.Value.Unborn:
		return Blocked("branch has no commits")
	case !r.Upstream.Value.Configured:
		return Blocked("no upstream")
	case r.Upstream.Value.Gone:
		return Blocked("upstream gone")
	case r.Upstream.Value.Diverged():
		return Blocked("diverged from upstream")
	case r.Upstream.Value.Ahead > 0:
		return Blocked("ahead of upstream")
	case r.Upstream.Value.Behind == 0:
		return upToDate
	case r.Upstream.Value.CommitID == "":
		return Blocked("upstream commit is unknown")
	case storeBusy:
		return Blocked("another operation or lazygit session is active")
	}
	return Verdict{}
}

// UpdateCandidate is one row of the update preview.
type UpdateCandidate struct {
	Repository domain.Repository
	Verdict    Verdict
}

// PlanUpdate splits targets into eligible and skipped repositories.
func PlanUpdate(targets []domain.Repository, storeBusy func(domain.StoreID) bool) (eligible, skipped []UpdateCandidate) {
	for _, r := range targets {
		c := UpdateCandidate{Repository: r, Verdict: Eligibility(r, r.StoreID != "" && storeBusy(r.StoreID))}
		if c.Verdict.Eligible() {
			eligible = append(eligible, c)
		} else {
			skipped = append(skipped, c)
		}
	}
	return eligible, skipped
}

// UpdateJobs creates fast-forward jobs. Each job inspects the repository again
// while holding the store lock and skips it if any condition no longer holds
// or if the state differs from the preview the user confirmed.
func UpdateJobs(client git.Interface, all []domain.Repository, eligible []UpdateCandidate, timeout time.Duration) []jobs.Job {
	var out []jobs.Job
	for _, c := range eligible {
		r := c.Repository
		out = append(out, jobs.Job{
			Kind:         jobs.KindUpdate,
			Operation:    domain.OperationUpdate,
			RepositoryID: r.ID,
			Repositories: StoreMembers(all, map[domain.StoreID]bool{r.StoreID: true}),
			StoreID:      r.StoreID,
			Mutating:     true,
			Timeout:      timeout,
			Run: func(ctx context.Context) (any, error) {
				in, err := client.Inspect(ctx, r.Location, git.InspectOptions{Untracked: domain.UntrackedNo})
				if err != nil {
					return nil, fmt.Errorf("recheck failed: %w", err)
				}
				fresh := r
				fresh.ApplyInspection(fresh.Inspected.Generation, in, nil, time.Now())
				if v := Eligibility(fresh, false); !v.Eligible() {
					return nil, &jobs.SkipError{Reason: "update skipped: " + v.String()}
				}
				if change := changedSincePreview(r, fresh); change != "" {
					return nil, &jobs.SkipError{Reason: "update skipped: " + change + " since the update was planned; press u to try again"}
				}
				return nil, client.MergeFastForwardOnly(ctx, r.Location, r.Upstream.Value.CommitID)
			},
		})
	}
	return out
}

// changedSincePreview names the part of the state that differs between the
// confirmed preview and a fresh inspection, or returns "".
func changedSincePreview(preview, fresh domain.Repository) string {
	ph, fh := preview.Head.Value, fresh.Head.Value
	pu, fu := preview.Upstream.Value, fresh.Upstream.Value
	switch {
	case ph.Branch != fh.Branch:
		return "branch changed"
	case ph.CommitID != fh.CommitID:
		return "HEAD moved"
	case pu.Ref != fu.Ref:
		return "upstream changed"
	case pu.CommitID != fu.CommitID:
		return "upstream moved"
	}
	return ""
}
