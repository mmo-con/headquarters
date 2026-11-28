package app

import (
	"testing"
	"time"

	"github.com/mmo-con/headquarters/internal/domain"
)

// reportStatus returns a Status of repos without operation results.
func reportStatus(repos ...domain.Repository) *Status {
	return &Status{Repositories: repos, ops: NewOperations(nil, nil, time.Now), now: time.Now()}
}

func TestReportAvailabilityValues(t *testing.T) {
	for a, want := range map[domain.Availability]string{
		domain.Available:        "available",
		domain.Missing:          "missing",
		domain.NotARepository:   "not_a_repository",
		domain.NotWorktreeRoot:  "not_worktree_root",
		domain.BareRepository:   "bare",
		domain.UntrustedOwner:   "untrusted_owner",
		domain.ValidationFailed: "validation_failed",
	} {
		r := domain.Repository{Name: "r", Availability: a}
		if got := repositoryReport(reportStatus(r), &r).Availability; got != want {
			t.Errorf("%v: availability = %q, want %q", a, got, want)
		}
	}
}

func TestReportOperationValues(t *testing.T) {
	for op, want := range map[domain.OperationInProgress]string{
		domain.NoOperation:   "none",
		domain.Merging:       "merging",
		domain.Rebasing:      "rebasing",
		domain.CherryPicking: "cherry_picking",
		domain.Reverting:     "reverting",
		domain.Bisecting:     "bisecting",
	} {
		r := domain.Repository{Name: "r"}
		r.Operation = domain.KnownValue(op)
		r.Worktree = domain.KnownValue(domain.WorktreeState{})
		rr := repositoryReport(reportStatus(r), &r)
		if rr.Worktree == nil || rr.Worktree.Operation == nil || *rr.Worktree.Operation != want {
			t.Errorf("%v: worktree = %v, want operation %q", op, rr.Worktree, want)
		}
	}
}

func TestReportStashesWithUnknownWorktree(t *testing.T) {
	r := domain.Repository{Name: "r"}
	r.Stashes = domain.KnownValue(2)
	rr := repositoryReport(reportStatus(r), &r)
	if rr.Worktree != nil || rr.Stashes == nil || *rr.Stashes != 2 {
		t.Errorf("worktree = %v, stashes = %v, want null and 2", rr.Worktree, rr.Stashes)
	}
}
