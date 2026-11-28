package app

import (
	"regexp"
	"strings"
	"time"

	"github.com/mmo-con/headquarters/internal/domain"
)

// ReportSchema is the version of the status report. Adding fields keeps it;
// renaming, removing, or changing the meaning of a field increments it.
const ReportSchema = 1

// Report is the machine-readable status of the selected repositories. All
// text from repositories is sanitized and remote URLs are redacted; unknown
// state is null, never a clean or up-to-date value.
type Report struct {
	Schema       int                `json:"schema"`
	GeneratedAt  time.Time          `json:"generated_at"`
	Summary      Summary            `json:"summary"`
	Repositories []RepositoryReport `json:"repositories"`
}

// Summary counts the reported repositories in total, per state of
// StatusStates, and per health level.
type Summary struct {
	Total  int            `json:"total"`
	States map[string]int `json:"states"`
	Health map[string]int `json:"health"`
}

// RepositoryReport is the state of one repository.
type RepositoryReport struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Family       string `json:"family"`
	Availability string `json:"availability"`
	// Known reports whether the inspection determined the complete state.
	Known  bool   `json:"known"`
	Health string `json:"health"`
	// States lists the states of StatusStates that r is in, in that order;
	// it contains "unknown" exactly when Known is false.
	States []string `json:"states"`
	// Head is the checked-out branch and commit, null if unknown.
	Head *HeadReport `json:"head"`
	// Operation is "none" or the operation in progress, such as "merging".
	Operation *string       `json:"operation"`
	Counts    *CountsReport `json:"counts"`
	// Branches lists the other local branches that need attention, in the
	// order of the detail panel; null if their state is unknown or not read.
	Branches  []BranchReport `json:"branches"`
	Remote    *RemoteReport  `json:"remote"`
	LastFetch *time.Time     `json:"last_fetch"`
	// Fetch is the result of the fetch, null if the repository was not
	// fetched.
	Fetch *FetchReport `json:"fetch"`
	Error *string      `json:"error"`
}

type CountsReport struct {
	Staged    int `json:"staged"`
	Modified  int `json:"modified"`
	Deleted   int `json:"deleted"`
	Renamed   int `json:"renamed"`
	Conflicts int `json:"conflicts"`
	// Untracked is null if untracked files are not checked.
	Untracked *int `json:"untracked"`
	Stashed   *int `json:"stashed"`
}

// HeadReport is the state of HEAD. State is "attached", "detached", or
// "unborn"; Branch is null if HEAD is detached, Commit is null if it is
// unborn, and Upstream is null if the upstream state is unknown.
type HeadReport struct {
	State    string          `json:"state"`
	Branch   *string         `json:"branch"`
	Commit   *CommitReport   `json:"commit"`
	Upstream *UpstreamReport `json:"upstream"`
}

// UpstreamReport is the upstream of a branch. Name is null if no upstream
// is configured. Ahead and Behind are null unless the upstream exists, so
// that 0 always means a known zero.
type UpstreamReport struct {
	Name   *string `json:"name"`
	Gone   bool    `json:"gone"`
	Ahead  *int    `json:"ahead"`
	Behind *int    `json:"behind"`
}

func upstreamReport(name string, gone bool, ahead, behind int) *UpstreamReport {
	u := &UpstreamReport{Gone: gone}
	if name != "" {
		u.Name = ptr(clean(name))
		if !gone {
			u.Ahead, u.Behind = ptr(ahead), ptr(behind)
		}
	}
	return u
}

// BranchReport is a local branch other than the checked-out one that needs
// attention. State is a domain.BranchReason. Upstream and CommitTime are
// null for a missing pinned branch.
type BranchReport struct {
	Name       string          `json:"name"`
	State      string          `json:"state"`
	Pinned     bool            `json:"pinned"`
	Upstream   *UpstreamReport `json:"upstream"`
	CommitTime *time.Time      `json:"commit_time"`
}

func branchReport(b domain.BranchState) BranchReport {
	reason := b.Reason()
	br := BranchReport{Name: clean(b.Name), State: string(reason), Pinned: b.Pinned}
	if reason == domain.BranchMissing {
		return br
	}
	br.Upstream = upstreamReport(b.Upstream, b.Gone, b.Ahead, b.Behind)
	if !b.CommitTime.IsZero() {
		br.CommitTime = ptr(b.CommitTime.UTC())
	}
	return br
}

type RemoteReport struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type CommitReport struct {
	Hash      string    `json:"hash"`
	ShortHash string    `json:"short_hash"`
	Author    string    `json:"author"`
	Subject   string    `json:"subject"`
	Time      time.Time `json:"time"`
}

type FetchReport struct {
	Status string  `json:"status"`
	Error  *string `json:"error"`
}

// NewReport builds the report of s.
func NewReport(s *Status, now time.Time) Report {
	rep := Report{
		Schema:       ReportSchema,
		GeneratedAt:  now.UTC().Truncate(time.Second),
		Summary:      Summary{States: map[string]int{}, Health: map[string]int{}},
		Repositories: []RepositoryReport{},
	}
	for _, name := range StatusStates() {
		rep.Summary.States[name] = 0
	}
	for h := HealthUnknown; h <= HealthOK; h++ {
		rep.Summary.Health[h.String()] = 0
	}
	for i := range s.Repositories {
		r := &s.Repositories[i]
		rep.Summary.Total++
		rr := repositoryReport(s, r)
		for _, name := range rr.States {
			rep.Summary.States[name]++
		}
		rep.Summary.Health[rr.Health]++
		rep.Repositories = append(rep.Repositories, rr)
	}
	return rep
}

func repositoryReport(s *Status, r *domain.Repository) RepositoryReport {
	rr := RepositoryReport{
		Name:         clean(r.Name),
		Path:         clean(r.Path),
		Family:       clean(string(r.Family)),
		Availability: availabilityValue(r.Availability),
		Known:        Known(r),
		Health:       s.Health(r).String(),
		States:       []string{},
	}
	for _, name := range StatusStates() {
		if s.Is(r, name) {
			rr.States = append(rr.States, name)
		}
	}
	if r.Head.Valid {
		h := r.Head.Value
		kind := "attached"
		switch {
		case h.Detached:
			kind = "detached"
		case h.Unborn:
			kind = "unborn"
		}
		hr := &HeadReport{State: kind}
		if !h.Detached && h.Branch != "" {
			hr.Branch = ptr(clean(h.Branch))
		}
		if r.LastCommit.Valid && r.LastCommit.Value != nil {
			c := r.LastCommit.Value
			hr.Commit = &CommitReport{Hash: c.ID, ShortHash: c.ShortID, Author: clean(c.Author), Subject: clean(c.Subject), Time: c.Time.UTC()}
		}
		if r.Upstream.Valid {
			u := r.Upstream.Value
			ref := ""
			if u.Configured {
				ref = u.Ref
			}
			hr.Upstream = upstreamReport(ref, u.Gone, u.Ahead, u.Behind)
		}
		rr.Head = hr
	}
	if r.Operation.Valid {
		rr.Operation = ptr(operationValue(r.Operation.Value))
	}
	if r.Worktree.Valid {
		w := r.Worktree.Value
		c := &CountsReport{Staged: w.Staged, Modified: w.Modified, Deleted: w.Deleted, Renamed: w.Renamed, Conflicts: w.Conflicts}
		if w.UntrackedChecked {
			c.Untracked = ptr(w.Untracked)
		}
		if r.Stashes.Valid {
			c.Stashed = ptr(r.Stashes.Value)
		}
		rr.Counts = c
	}
	if r.Branches.Valid {
		rr.Branches = make([]BranchReport, 0, len(r.Branches.Value))
		for _, b := range r.Branches.Value {
			rr.Branches = append(rr.Branches, branchReport(b))
		}
	}
	if p := r.PrimaryRemote; p != nil {
		rr.Remote = &RemoteReport{Name: clean(p.Name), URL: clean(domain.RedactURL(p.URL))}
	}
	if r.LastFetch.Valid && r.LastFetch.Value != nil {
		t := r.LastFetch.Value.UTC()
		rr.LastFetch = &t
	}
	var errs []string
	for _, err := range []error{r.EntryError, r.Inspected.Err} {
		if err != nil {
			errs = append(errs, clean(err.Error()))
		}
	}
	if res, ok := s.FetchResult(r); ok {
		f := &FetchReport{Status: string(res.Status)}
		if res.Error != nil {
			msg := clean(res.Error.Error())
			f.Error = &msg
			if res.Status == domain.StatusFailed {
				errs = append(errs, "fetch failed: "+msg)
			}
		}
		rr.Fetch = f
	}
	if len(errs) > 0 {
		rr.Error = ptr(strings.Join(errs, "; "))
	}
	return rr
}

// availabilityValue is the report value of a. The report has its own
// values, so that changing the text of the user interface keeps the format.
func availabilityValue(a domain.Availability) string {
	switch a {
	case domain.Available:
		return "available"
	case domain.Missing:
		return "missing"
	case domain.NotARepository:
		return "not_a_repository"
	case domain.NotWorktreeRoot:
		return "not_worktree_root"
	case domain.BareRepository:
		return "bare"
	case domain.UntrustedOwner:
		return "untrusted_owner"
	case domain.ValidationFailed:
		return "validation_failed"
	}
	return "unknown"
}

// operationValue is the report value of o: "none" or the operation in
// progress in snake_case.
func operationValue(o domain.OperationInProgress) string {
	if o == domain.NoOperation {
		return "none"
	}
	return strings.ReplaceAll(o.String(), "-", "_")
}

func ptr[T any](v T) *T { return &v }

var urlPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s'"]+`)

// clean makes text from a repository safe to print and redacts the user
// information of URLs within it.
func clean(s string) string {
	return domain.Printable(urlPattern.ReplaceAllStringFunc(s, domain.RedactURL))
}
