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
		rr := repositoryReport(reportStatus(r), &r)
		if rr.Operation == nil || *rr.Operation != want {
			t.Errorf("%v: operation = %v, want %q", op, rr.Operation, want)
		}
	}
}
