package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	tu "github.com/mmo-con/headquarters/internal/testutil"
)

func cleanupRepo() domain.Repository {
	return domain.Repository{
		ID: "/w/r", StoreID: "/w/r/.git", Name: "r", Availability: domain.Available,
		Location:      domain.Location{WorktreeRoot: "/w/r"},
		PrimaryRemote: &domain.Remote{Name: "origin"},
		BranchPolicy:  domain.BranchPolicy{Pinned: []string{"release"}},
	}
}

// cleanupState is a store whose default branch origin/main is at commit d0
// and whose worktree /w/r has main checked out.
func cleanupState(branches ...git.CleanupBranch) git.BranchCleanup {
	main := git.CleanupBranch{Name: "main", CommitID: "d0", Upstream: "origin/main", Worktree: "/w/r", MergedDefault: true, MergedHead: true}
	return git.BranchCleanup{Remote: "origin", Default: "refs/remotes/origin/main", DefaultID: "d0",
		ContainmentChecked: true, Branches: append([]git.CleanupBranch{main}, branches...)}
}

// planned returns branch: reason for every row; "delete" marks rows deleted
// with git branch -d, "delete-ref" rows deleted with the checked update-ref.
func planned(rows []CleanupCandidate) map[string]string {
	out := map[string]string{}
	for _, c := range rows {
		switch {
		case c.Deletable() && c.RefDelete:
			out[c.Branch] = "delete-ref"
		case c.Deletable():
			out[c.Branch] = "delete"
		default:
			out[c.Branch] = c.Reason
		}
	}
	return out
}

func TestPlanCleanup(t *testing.T) {
	merged := func(name string) git.CleanupBranch {
		return git.CleanupBranch{Name: name, CommitID: "c-" + name, MergedDefault: true, MergedHead: true}
	}
	tests := []struct {
		name   string
		branch git.CleanupBranch
		want   string // "delete", "delete-ref", a reason substring, or "" for not listed
	}{
		{"merged without upstream", merged("a"), "delete"},
		{"merged with gone upstream", func() git.CleanupBranch {
			b := merged("a")
			b.Upstream, b.Gone = "origin/a", true
			return b
		}(), "delete"},
		{"merged, upstream still exists", func() git.CleanupBranch {
			b := merged("a")
			b.Upstream = "origin/a"
			return b
		}(), "upstream origin/a still exists"},
		{"at the default commit, upstream still exists", func() git.CleanupBranch {
			b := merged("a")
			b.Upstream, b.CommitID = "origin/a", "d0"
			return b
		}(), "upstream origin/a still exists"},
		{"at the default commit, upstream gone", func() git.CleanupBranch {
			// For example after a fast-forward merge on the server.
			b := merged("a")
			b.Upstream, b.Gone, b.CommitID = "origin/a", true, "d0"
			return b
		}(), "delete"},
		{"checked out here", func() git.CleanupBranch { b := merged("a"); b.Worktree = "/w/r"; return b }(), "checked out"},
		{"checked out in another worktree", func() git.CleanupBranch { b := merged("a"); b.Worktree = "/w/other"; return b }(), "checked out in /w/other"},
		{"pinned", merged("release"), "pinned"},
		{"gone, changes not in the default branch", git.CleanupBranch{Name: "a", CommitID: "x", Upstream: "origin/a", Gone: true},
			"upstream gone, changes not in origin/main"},
		{"gone, squash- or rebase-merged", git.CleanupBranch{Name: "a", CommitID: "x", Upstream: "origin/a", Gone: true, Contained: true},
			"delete-ref"},
		{"gone, merged into the default branch but not into HEAD", func() git.CleanupBranch {
			b := merged("a")
			b.Upstream, b.Gone, b.MergedHead = "origin/a", true, false
			return b
		}(), "delete-ref"},
		{"no upstream, merged into the default branch but not into HEAD", func() git.CleanupBranch {
			b := merged("a")
			b.MergedHead = false
			return b
		}(), "update main first"},
		{"merged but ahead of its upstream", func() git.CleanupBranch {
			b := merged("a")
			b.Upstream, b.Ahead = "origin/a", 2
			return b
		}(), "not pushed to origin/a"},
		{"no commits of its own", func() git.CleanupBranch { b := merged("a"); b.CommitID = "d0"; return b }(), "no commits of its own"},
		{"not merged and active", git.CleanupBranch{Name: "a", CommitID: "x", Upstream: "origin/a"}, ""},
		{"not merged without upstream", git.CleanupBranch{Name: "a", CommitID: "x"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := planned(PlanCleanup(cleanupRepo(), cleanupState(tc.branch)))
			if _, listed := got["main"]; listed {
				t.Fatalf("the default branch must not be listed: %v", got)
			}
			reason, listed := got[tc.branch.Name]
			switch {
			case tc.want == "" && listed:
				t.Fatalf("must not be listed: %q", reason)
			case tc.want != "" && !strings.Contains(reason, tc.want):
				t.Fatalf("got %q, want %q", reason, tc.want)
			}
		})
	}
}

func TestPlanCleanupWithoutContainmentCheck(t *testing.T) {
	st := cleanupState(git.CleanupBranch{Name: "a", CommitID: "x", Upstream: "origin/a", Gone: true})
	st.ContainmentChecked = false
	if got := planned(PlanCleanup(cleanupRepo(), st))["a"]; !strings.Contains(got, "Git 2.38") {
		t.Fatalf("a = %q", got)
	}
}

func TestPlanCleanupKeepsCommitAndRepository(t *testing.T) {
	r := cleanupRepo()
	rows := PlanCleanup(r, cleanupState(git.CleanupBranch{Name: "a", CommitID: "c1", MergedDefault: true, MergedHead: true}))
	if len(rows) != 1 || rows[0].CommitID != "c1" || rows[0].Repository.ID != r.ID {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPlanCleanupWithoutDefaultBranch(t *testing.T) {
	st := cleanupState(git.CleanupBranch{Name: "a", CommitID: "c1"})
	st.Default, st.DefaultID = "", ""
	rows := PlanCleanup(cleanupRepo(), st)
	if len(rows) != 1 || rows[0].Branch != "" || rows[0].Deletable() ||
		!strings.Contains(rows[0].Reason, "git remote set-head origin --auto") {
		t.Fatalf("rows = %+v", rows)
	}

	r := cleanupRepo()
	r.PrimaryRemote = nil
	st.Remote = ""
	if rows := PlanCleanup(r, st); len(rows) != 1 || !strings.Contains(rows[0].Reason, "no remote") {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPlanCleanupKeepsRefDeletesDuringRebaseOrBisect(t *testing.T) {
	squashed := git.CleanupBranch{Name: "squashed", CommitID: "s1", Upstream: "origin/squashed", Gone: true, Contained: true}
	merged := git.CleanupBranch{Name: "merged", CommitID: "m1", MergedDefault: true, MergedHead: true}
	for op, word := range map[domain.OperationInProgress]string{domain.Rebasing: "rebase", domain.Bisecting: "bisect"} {
		st := cleanupState(squashed, merged)
		st.InProgress = op
		got := planned(PlanCleanup(cleanupRepo(), st))
		if !strings.Contains(got["squashed"], word+" in progress") {
			t.Errorf("%s: squashed = %q", word, got["squashed"])
		}
		// git branch -d refuses a branch in use by a rebase or bisect itself.
		if got["merged"] != "delete" {
			t.Errorf("%s: merged = %q", word, got["merged"])
		}
	}
}

// TestCleanupKeepsBranchOfStoppedRebase reproduces a branch with a gone
// upstream, merged into the default branch but not into HEAD, that is
// being rebased: its worktree's HEAD is detached, so the branch is not
// checked out, but deleting it would break the rebase.
func TestCleanupKeepsBranchOfStoppedRebase(t *testing.T) {
	tu.IsolateGit(t)
	dir := tu.Canonical(t, t.TempDir())
	up := filepath.Join(dir, "up.git")
	tu.Git(t, "", "init", "-q", "--bare", up)
	clone := filepath.Join(dir, "clone")
	tu.Git(t, "", "clone", "-q", up, clone)
	tu.Commit(t, clone, "a", "a\n", "init")
	tu.Git(t, clone, "push", "-q", "origin", "main")
	tu.Git(t, clone, "remote", "set-head", "origin", "--auto")
	tu.Git(t, clone, "switch", "-q", "-c", "done")
	tu.Commit(t, clone, "b", "b\n", "done one")
	tu.Commit(t, clone, "c", "c\n", "done two")
	tu.Git(t, clone, "push", "-q", "-u", "origin", "done")
	other := filepath.Join(dir, "other")
	tu.Git(t, "", "clone", "-q", up, other)
	tu.Git(t, other, "merge", "-q", "--no-ff", "-m", "merge done", "origin/done")
	tu.Git(t, other, "push", "-q", "origin", "main", ":done")
	tu.Git(t, clone, "fetch", "-q", "--prune")
	if out, err := tu.TryGit(clone, "rebase", "-x", "false", "HEAD~1"); err == nil {
		t.Fatalf("the rebase must stop: %s", out)
	}

	c := git.New("git")
	v := c.Validate(context.Background(), clone)
	r := domain.Repository{ID: domain.RepositoryID(clone), StoreID: domain.StoreID(v.Location.CommonDir),
		Availability: v.Availability, Location: v.Location, PrimaryRemote: &domain.Remote{Name: "origin"}}
	st, err := c.ReadBranchCleanup(context.Background(), r.Location, "origin")
	if err != nil {
		t.Fatal(err)
	}
	rows := PlanCleanup(r, st)
	if got := planned(rows)["done"]; !strings.Contains(got, "rebase in progress") {
		t.Fatalf("done = %q", got)
	}
	tu.Git(t, clone, "rev-parse", "--verify", "-q", "refs/heads/done")
}

func TestCleanupTargetsReadEachStoreOnce(t *testing.T) {
	a, linked, b, missing := cleanupRepo(), cleanupRepo(), cleanupRepo(), cleanupRepo()
	linked.ID = "/w/linked"
	b.ID, b.StoreID = "/w/b", "/w/b/.git"
	missing.ID, missing.StoreID, missing.Availability = "/w/m", "/w/m/.git", domain.Missing
	all := []domain.Repository{a, linked, b, missing}
	read, blocked := CleanupTargets(all, all)
	if len(read) != 2 || read[0].ID != a.ID || read[1].ID != b.ID {
		t.Fatalf("read = %+v", read)
	}
	if len(blocked) != 1 || blocked[0].Repository.ID != missing.ID || !strings.Contains(blocked[0].Reason, "missing") {
		t.Fatalf("blocked = %+v", blocked)
	}
}

func TestCleanupJobRechecksBeforeDeleting(t *testing.T) {
	r := cleanupRepo()
	a := git.CleanupBranch{Name: "a", CommitID: "c1", MergedDefault: true, MergedHead: true}
	planned := []CleanupCandidate{{Repository: r, Branch: "a", CommitID: "c1"}}
	tests := []struct {
		name   string
		state  git.BranchCleanup
		err    error
		status domain.OperationStatus
		reason string
	}{
		{"deleted", cleanupState(a), nil, domain.StatusSucceeded, ""},
		{"gone", cleanupState(), nil, domain.StatusSkipped, "no longer exists"},
		{"moved", cleanupState(func() git.CleanupBranch { b := a; b.CommitID = "c2"; return b }()), nil, domain.StatusSkipped, "moved since the preview"},
		{"checked out meanwhile", cleanupState(func() git.CleanupBranch { b := a; b.Worktree = "/w/other"; return b }()), nil, domain.StatusSkipped, "checked out in /w/other"},
		{"refused by Git", cleanupState(a), errors.New("not fully merged"), domain.StatusFailed, "not fully merged"},
		{"squash-merged", cleanupState(func() git.CleanupBranch {
			b := a
			b.Upstream, b.Gone, b.MergedDefault, b.MergedHead, b.Contained = "origin/a", true, false, false, true
			return b
		}()), nil, domain.StatusSucceeded, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeGit{cleanup: tc.state, deleteErr: tc.err}
			js := CleanupJobs(f, []domain.Repository{r}, planned, time.Second)
			if len(js) != 1 || !js[0].Mutating || js[0].StoreID != r.StoreID || js[0].Operation != domain.OperationCleanup {
				t.Fatalf("jobs = %+v", js)
			}
			ev := runJobs(t, js)[0].Result
			if ev.Status != tc.status || (tc.reason != "" && (ev.Error == nil || !strings.Contains(ev.Error.Error(), tc.reason))) {
				t.Fatalf("result = %v %v", ev.Status, ev.Error)
			}
			want := "a"
			if tc.name == "squash-merged" {
				want = "a@c1"
			}
			if deleted := len(f.deleted) == 1 && f.deleted[0] == want; deleted != (tc.status == domain.StatusSucceeded) {
				t.Fatalf("deleted = %v", f.deleted)
			}
		})
	}
}

// TestCleanupTargetsKeepPinsOfTheWholeStore checks that a branch pinned by
// any entry of a store is kept, also when another entry reads the store.
func TestCleanupTargetsKeepPinsOfTheWholeStore(t *testing.T) {
	a, linked := cleanupRepo(), cleanupRepo()
	a.BranchPolicy.Pinned = []string{"main"}
	linked.ID, linked.BranchPolicy.Pinned = "/w/linked", []string{"keep"}
	read, _ := CleanupTargets([]domain.Repository{a, linked}, []domain.Repository{a})
	if len(read) != 1 {
		t.Fatalf("read = %+v", read)
	}
	rows := PlanCleanup(read[0], cleanupState(git.CleanupBranch{Name: "keep", CommitID: "c1", MergedDefault: true, MergedHead: true}))
	if got := planned(rows)["keep"]; got != "pinned" {
		t.Fatalf("keep = %q", got)
	}
}
