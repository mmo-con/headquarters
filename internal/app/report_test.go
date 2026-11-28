package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
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

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// goldenStatus returns a fixed status that fills every field of the report
// at least once: a dirty repository with other branches, a detached one
// without a family whose fetch failed, and a missing one.
func goldenStatus() *Status {
	at := func(s string) time.Time {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return t
	}
	inspected := domain.InspectionInfo{Generation: 1, FinishedAt: at("2026-11-23T16:59:00Z")}
	remotes := func(url string) domain.Known[[]domain.Remote] {
		return domain.KnownValue([]domain.Remote{{Name: "origin", URL: url}})
	}

	hq := domain.Repository{ID: "/home/me/src/headquarters", Name: "headquarters",
		Path: "/home/me/src/headquarters", Family: "mmo-con", Inspected: inspected}
	hq.Head = domain.KnownValue(domain.HeadState{Branch: "main", CommitID: "3077302d"})
	hq.Operation = domain.KnownValue(domain.NoOperation)
	hq.Worktree = domain.KnownValue(domain.WorktreeState{Modified: 2, Untracked: 1, UntrackedChecked: true})
	hq.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true, Ref: "origin/main", Behind: 3})
	hq.Stashes = domain.KnownValue(1)
	hq.Remotes = remotes("https://user:token@github.com/mmo-con/headquarters.git")
	hq.PrimaryRemote = &hq.Remotes.Value[0]
	hq.LastFetch = domain.KnownValue(ptr(at("2026-10-01T08:12:00Z")))
	hq.LastCommit = domain.KnownValue(&domain.CommitSummary{ID: "3077302d4c1e0b6f1a2b3c4d5e6f708192a3b4c5",
		ShortID: "3077302", Author: "Jane Doe", Subject: "docs: streamline README", Time: at("2026-11-22T20:41:00Z")})
	hq.Branches = domain.KnownValue([]domain.BranchState{
		{Name: "release", Upstream: "origin/release", Behind: 2, Pinned: true, CommitTime: at("2026-11-20T09:30:00Z")},
		{Name: "stable", Pinned: true, Missing: true},
		{Name: "feature/x", Upstream: "origin/feature/x", Gone: true, CommitTime: at("2026-11-19T10:00:00Z")},
		{Name: "spike", CommitTime: at("2026-11-18T11:00:00Z")},
	})

	notes := domain.Repository{ID: "/home/me/notes", Name: "notes", Path: "/home/me/notes",
		Family: domain.UngroupedFamily, Inspected: inspected}
	notes.Head = domain.KnownValue(domain.HeadState{CommitID: "9ebb42d0", Detached: true})
	notes.Operation = domain.KnownValue(domain.Rebasing)
	notes.Worktree = domain.KnownValue(domain.WorktreeState{Conflicts: 1})
	notes.Upstream = domain.KnownValue(domain.UpstreamState{})
	notes.Stashes = domain.KnownValue(0)
	notes.Remotes = remotes("https://example.com/notes.git")
	notes.PrimaryRemote = &notes.Remotes.Value[0]
	notes.LastFetch = domain.KnownValue[*time.Time](nil)
	notes.LastCommit = domain.KnownValue(&domain.CommitSummary{ID: "9ebb42d0a1b2c3d4e5f60718293a4b5c6d7e8f90",
		ShortID: "9ebb42d", Author: "Jane Doe", Subject: "wip", Time: at("2026-11-21T12:00:00Z")})
	notes.Branches = domain.KnownValue([]domain.BranchState{})

	missing := domain.Repository{ID: "/home/me/old", Name: "old", Path: "/home/me/old", Family: "archive",
		Availability: domain.Missing, EntryError: errors.New("stat /home/me/old: no such file or directory")}

	s := &Status{Repositories: []domain.Repository{hq, notes, missing},
		ops: NewOperations(nil, nil, time.Now), now: at("2026-11-23T17:00:00Z")}
	s.ops.last[notes.ID] = domain.OperationResult{RepositoryID: notes.ID, Operation: domain.OperationFetch,
		Status: domain.StatusFailed, Error: errors.New("git exited with status 128: fatal: repository not found")}
	return s
}

func TestReportGolden(t *testing.T) {
	s := goldenStatus()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(NewReport(s, s.now)); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "status.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run go test ./internal/app -run TestReportGolden -update", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("report differs from %s; review the change and run with -update:\n%s", golden, buf.String())
	}
}
