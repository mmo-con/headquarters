package git_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	tu "github.com/mmo-con/headquarters/internal/testutil"
)

func validate(t *testing.T, c *git.Client, path string) domain.Location {
	t.Helper()
	v := c.Validate(context.Background(), path)
	if v.Availability != domain.Available {
		t.Fatalf("validate %s: %v %v", path, v.Availability, v.Err)
	}
	return v.Location
}

func inspect(t *testing.T, c *git.Client, loc domain.Location) domain.Inspection {
	t.Helper()
	in, err := c.Inspect(context.Background(), loc, git.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestOpen(t *testing.T) {
	tu.IsolateGit(t)
	if c, _, err := git.Open(context.Background(), "git"); err != nil || c == nil {
		t.Fatalf("Open: client=%v err=%v", c, err)
	}
	if c, _, err := git.Open(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil || c != nil {
		t.Fatalf("Open of a missing executable: client=%v err=%v", c, err)
	}
}

func TestInspectWorktreeStates(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	tu.Commit(t, repo, "a.txt", "a\n", "add a")
	tu.Commit(t, repo, "b.txt", "b\n", "add b")
	tu.Commit(t, repo, "c.txt", "c\n", "add c")
	loc := validate(t, c, repo)

	in := inspect(t, c, loc)
	if !in.Worktree.Valid || !in.Worktree.Value.Clean() {
		t.Fatalf("expected clean worktree: %+v", in.Worktree)
	}
	if in.LastCommit.Value == nil || in.LastCommit.Value.Subject != "add c" || in.LastCommit.Value.Author != "Test" {
		t.Fatalf("last commit = %+v", in.LastCommit.Value)
	}
	if !in.Remotes.Valid || len(in.Remotes.Value) != 0 {
		t.Fatalf("remotes = %+v", in.Remotes)
	}
	if in.FetchHead.Value != nil || !in.FetchHead.Valid {
		t.Fatalf("fetch head = %+v", in.FetchHead)
	}

	tu.WriteFile(t, filepath.Join(repo, "a.txt"), "changed\n")
	tu.WriteFile(t, filepath.Join(repo, "staged.txt"), "s\n")
	tu.Git(t, repo, "add", "staged.txt")
	os.Remove(filepath.Join(repo, "b.txt"))
	tu.Git(t, repo, "mv", "c.txt", "renamed.txt")
	tu.WriteFile(t, filepath.Join(repo, "untracked.txt"), "u\n")

	w := inspect(t, c, loc).Worktree.Value
	if w.Staged != 2 || w.Modified != 1 || w.Deleted != 1 || w.Renamed != 1 || w.Untracked != 1 || w.Conflicts != 0 {
		t.Fatalf("counts = %+v", w)
	}

	in, err := c.Inspect(context.Background(), loc, git.InspectOptions{Untracked: domain.UntrackedNo})
	if err != nil {
		t.Fatal(err)
	}
	if in.Worktree.Value.Untracked != 0 || in.Worktree.Value.UntrackedChecked {
		t.Fatalf("untracked=no: %+v", in.Worktree.Value)
	}
}

func TestRecentCommits(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	tu.Commit(t, repo, "a.txt", "a\n", "add a")
	tu.Commit(t, repo, "b.txt", "b\n", "add b")
	tu.WriteFile(t, filepath.Join(repo, "c.txt"), "c\n")
	tu.Git(t, repo, "add", "c.txt")
	tu.Git(t, repo, "commit", "-q", "-m", "add c", "-m", "body line\nwith a newline")
	tu.WriteFile(t, filepath.Join(repo, "HEAD"), "a file named like the ref\n")
	loc := validate(t, c, repo)

	commits, err := c.RecentCommits(context.Background(), loc, 3)
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, cm := range commits {
		subjects = append(subjects, cm.Subject)
		if len(cm.ID) < 40 || cm.ShortID == "" || cm.Author != "Test" || cm.Time.IsZero() {
			t.Fatalf("incomplete commit: %+v", cm)
		}
	}
	if got := strings.Join(subjects, "|"); got != "add c|add b|add a" {
		t.Fatalf("subjects = %q, want the newest three without bodies", got)
	}
	if commits[0].ID != tu.Git(t, repo, "rev-parse", "HEAD") {
		t.Fatal("the history must start at HEAD")
	}
	if all, err := c.RecentCommits(context.Background(), loc, 10); err != nil || len(all) != 4 {
		t.Fatalf("a short history returns every commit: %d %v", len(all), err)
	}
}

// TestIncomingCommits lists the upstream commits that HEAD lacks from local
// refs, for a clone that is behind, up to date, diverged, or truncated.
func TestIncomingCommits(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	loc := validate(t, c, clone)
	incoming := func(n int) []string {
		t.Helper()
		in := inspect(t, c, loc)
		commits, err := c.IncomingCommits(context.Background(), loc, in.Head.Value.CommitID, in.Upstream.Value.CommitID, n)
		if err != nil {
			t.Fatal(err)
		}
		var subjects []string
		for _, cm := range commits {
			if len(cm.ID) < 40 || cm.ShortID == "" || cm.Author != "Test" || cm.Time.IsZero() {
				t.Fatalf("incomplete commit: %+v", cm)
			}
			subjects = append(subjects, cm.Subject)
		}
		return subjects
	}

	if got := incoming(10); len(got) != 0 {
		t.Fatalf("an up-to-date branch has no incoming commits: %q", got)
	}
	for _, s := range []string{"one", "two", "three"} {
		tu.Commit(t, origin, s+".txt", s+"\n", "upstream "+s)
	}
	if got := incoming(10); len(got) != 0 {
		t.Fatalf("commits that were not fetched must not be listed: %q", got)
	}
	tu.Git(t, clone, "fetch", "-q")
	if got := strings.Join(incoming(10), "|"); got != "upstream three|upstream two|upstream one" {
		t.Fatalf("behind: %q, want every incoming commit, newest first", got)
	}
	if got := strings.Join(incoming(2), "|"); got != "upstream three|upstream two" {
		t.Fatalf("truncated: %q, want the newest two", got)
	}
	tu.Commit(t, clone, "local.txt", "local\n", "local work")
	if got := strings.Join(incoming(10), "|"); got != "upstream three|upstream two|upstream one" {
		t.Fatalf("diverged: %q, want only the upstream commits", got)
	}
}

func TestInspectStashes(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	repo := tu.InitRepo(t, filepath.Join(base, "repo"))
	tu.Git(t, repo, "worktree", "add", "-q", "-b", "side", filepath.Join(base, "wt"))

	withHeader := git.New("git")
	if _, err := withHeader.CheckVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A client without a checked version counts with a separate process.
	clients := map[string]*git.Client{"status header": withHeader, "rev-list": git.New("git")}
	check := func(want int) {
		t.Helper()
		for name, c := range clients {
			for _, path := range []string{repo, filepath.Join(base, "wt")} {
				in := inspect(t, c, validate(t, c, path))
				if !in.Stashes.Valid || in.Stashes.Value != want {
					t.Errorf("%s, %s: stashes = %+v, want %d", name, filepath.Base(path), in.Stashes, want)
				}
			}
		}
	}
	check(0)
	for i := range 2 {
		tu.WriteFile(t, filepath.Join(repo, "README"), fmt.Sprintf("change %d\n", i))
		tu.Git(t, repo, "stash", "-q")
	}
	check(2)
}

func TestInspectMergeConflictAndOperations(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	tu.Git(t, repo, "checkout", "-q", "-b", "other")
	tu.Commit(t, repo, "README", "other\n", "other")
	tu.Git(t, repo, "checkout", "-q", "main")
	tu.Commit(t, repo, "README", "main\n", "main")
	_, _ = tu.TryGit(repo, "merge", "other")
	loc := validate(t, c, repo)
	in := inspect(t, c, loc)
	if in.Operation.Value != domain.Merging || in.Worktree.Value.Conflicts != 1 {
		t.Fatalf("operation=%v worktree=%+v", in.Operation.Value, in.Worktree.Value)
	}
	tu.Git(t, repo, "merge", "--abort")

	tu.Git(t, repo, "bisect", "start")
	if got := inspect(t, c, loc).Operation.Value; got != domain.Bisecting {
		t.Fatalf("operation = %v, want bisecting", got)
	}
	tu.Git(t, repo, "bisect", "reset")

	_, _ = tu.TryGit(repo, "rebase", "other")
	if got := inspect(t, c, loc).Operation.Value; got != domain.Rebasing {
		t.Fatalf("operation = %v, want rebasing", got)
	}
}

func TestInspectHeadsAndUpstreams(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	loc := validate(t, c, clone)

	in := inspect(t, c, loc)
	u := in.Upstream.Value
	if !u.Configured || u.Ref != "origin/main" || u.Ahead != 0 || u.Behind != 0 {
		t.Fatalf("upstream = %+v", u)
	}
	if len(in.Remotes.Value) != 1 || in.Remotes.Value[0].URL != origin {
		t.Fatalf("remotes = %+v", in.Remotes.Value)
	}

	tu.Commit(t, origin, "o.txt", "o\n", "upstream change")
	tu.Git(t, clone, "fetch", "-q")
	u = inspect(t, c, loc).Upstream.Value
	if u.Behind != 1 || u.Ahead != 0 {
		t.Fatalf("behind: %+v", u)
	}
	tu.Commit(t, clone, "l.txt", "l\n", "local change")
	u = inspect(t, c, loc).Upstream.Value
	if !u.Diverged() {
		t.Fatalf("diverged: %+v", u)
	}

	tu.Git(t, clone, "checkout", "-q", "-b", "local-only")
	if in := inspect(t, c, loc); in.Upstream.Value.Configured || in.Head.Value.Branch != "local-only" {
		t.Fatalf("no upstream: %+v %+v", in.Head.Value, in.Upstream.Value)
	}

	tu.Git(t, origin, "branch", "feature")
	tu.Git(t, clone, "fetch", "-q")
	tu.Git(t, clone, "checkout", "-q", "-b", "feature", "--track", "origin/feature")
	tu.Git(t, origin, "branch", "-D", "feature")
	tu.Git(t, clone, "fetch", "-q", "--prune")
	if u := inspect(t, c, loc).Upstream.Value; !u.Gone {
		t.Fatalf("upstream gone: %+v", u)
	}

	tu.Git(t, clone, "checkout", "-q", "--detach", "HEAD")
	if h := inspect(t, c, loc).Head.Value; !h.Detached {
		t.Fatalf("detached: %+v", h)
	}

	tu.Git(t, clone, "remote", "add", "upstream", "https://user:secret@example.com/x.git")
	if r := inspect(t, c, loc).Remotes.Value; len(r) != 2 {
		t.Fatalf("multiple remotes: %+v", r)
	}

	unborn := filepath.Join(base, "unborn")
	tu.Git(t, "", "init", "-q", unborn)
	in = inspect(t, c, validate(t, c, unborn))
	if !in.Head.Value.Unborn || !in.LastCommit.Valid || in.LastCommit.Value != nil {
		t.Fatalf("unborn: %+v %+v", in.Head, in.LastCommit)
	}
}

func TestInspectOtherBranches(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, origin, "branch", "gone")
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	tu.Git(t, clone, "branch", "--track", "gone", "origin/gone")
	tu.Git(t, origin, "branch", "-D", "gone")
	tu.Commit(t, origin, "o.txt", "o\n", "upstream change")
	tu.Git(t, clone, "fetch", "-q", "--prune")
	tu.Git(t, clone, "checkout", "-q", "-b", "feature")
	tu.Commit(t, clone, "f.txt", "f\n", "feature work")
	tu.Git(t, clone, "branch", "local-only")
	tu.Git(t, clone, "worktree", "add", "-q", "-b", "in-worktree", filepath.Join(base, "wt"))
	loc := validate(t, c, clone)

	in, err := c.Inspect(context.Background(), loc, git.InspectOptions{Branches: true})
	if err != nil {
		t.Fatal(err)
	}
	if in.LastCommit.Value == nil || in.LastCommit.Value.Subject != "feature work" || in.LastCommit.Value.Author != "Test" {
		t.Fatalf("last commit = %+v", in.LastCommit.Value)
	}
	got := map[string]domain.BranchState{}
	for _, b := range in.Branches.Value {
		got[b.Name] = b
	}
	if len(got) != 5 || !got["feature"].Current || !got["gone"].Gone || got["local-only"].Upstream != "" ||
		got["main"].Behind != 1 || got["main"].Upstream != "origin/main" || got["in-worktree"].Worktree == "" {
		t.Fatalf("branches = %+v", in.Branches.Value)
	}

	in, err = c.Inspect(context.Background(), loc, git.InspectOptions{})
	if err != nil || in.Branches.Valid || in.LastCommit.Value == nil || in.LastCommit.Value.Subject != "feature work" {
		t.Fatalf("without branches: %+v %+v %v", in.Branches, in.LastCommit, err)
	}

	tu.Git(t, clone, "checkout", "-q", "--detach", "HEAD~1")
	in, err = c.Inspect(context.Background(), loc, git.InspectOptions{Branches: true})
	if err != nil || in.LastCommit.Value == nil || in.LastCommit.Value.Subject != "initial commit" {
		t.Fatalf("detached: %+v %v", in.LastCommit, err)
	}
	if b := in.Branches.Value; len(b) != 5 {
		t.Fatalf("detached branches = %+v", b)
	}
}

func TestValidateClassification(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	repo := tu.InitRepo(t, filepath.Join(base, "repo"))
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	tu.Git(t, "", "init", "-q", "--bare", filepath.Join(base, "bare.git"))
	if err := os.MkdirAll(filepath.Join(base, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	tu.Git(t, repo, "config", "--global", "safe.bareRepository", "explicit")

	tests := []struct {
		path string
		want domain.Availability
	}{
		{repo, domain.Available},
		{filepath.Join(repo, "sub"), domain.NotWorktreeRoot},
		{filepath.Join(base, "bare.git"), domain.BareRepository},
		{filepath.Join(base, "plain"), domain.NotARepository},
		{filepath.Join(base, "missing"), domain.Missing},
	}
	for _, tc := range tests {
		v := c.Validate(context.Background(), tc.path)
		if v.Availability != tc.want {
			t.Errorf("%s: got %v (%v), want %v", tc.path, v.Availability, v.Err, tc.want)
		}
	}
	if v := c.Validate(context.Background(), filepath.Join(repo, "sub")); !strings.Contains(v.Err.Error(), repo) {
		t.Errorf("error must name the worktree root: %v", v.Err)
	}
}

// TestValidateWithoutGitResult checks that a Git process that does not finish
// is a failed validation, not proof that the path is no repository.
func TestValidateWithoutGitResult(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, script string
		timeout      time.Duration
		want         domain.Availability
	}{
		{"exit status", "exit 128", 0, domain.NotARepository},
		{"signal", "kill -KILL $$", 0, domain.ValidationFailed},
		{"timeout", "exec sleep 30", 200 * time.Millisecond, domain.ValidationFailed},
	}
	for _, tc := range tests {
		exe := filepath.Join(t.TempDir(), "git")
		if err := os.WriteFile(exe, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if tc.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, tc.timeout)
			defer cancel()
		}
		v := git.New(exe).Validate(ctx, dir)
		if v.Availability != tc.want || v.Err == nil {
			t.Errorf("%s: got %v (%v), want %v", tc.name, v.Availability, v.Err, tc.want)
		}
	}
}

func TestLinkedWorktreesShareStore(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	main := tu.InitRepo(t, filepath.Join(base, "main"))
	wt := filepath.Join(base, "wt")
	tu.Git(t, main, "worktree", "add", "-q", wt)

	a, b := validate(t, c, main), validate(t, c, wt)
	if a.CommonDir != b.CommonDir {
		t.Fatalf("common dirs differ: %s vs %s", a.CommonDir, b.CommonDir)
	}
	if a.Markers.FetchHead == b.Markers.FetchHead {
		t.Fatal("FETCH_HEAD must be resolved per worktree")
	}
	if b.WorktreeRoot != wt {
		t.Fatalf("worktree root = %s", b.WorktreeRoot)
	}
}

func TestInspectionTakesNoOptionalLocks(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	loc := validate(t, c, repo)
	index := filepath.Join(repo, ".git", "index")
	// Make the index stat information stale so that a normal git status would
	// refresh and rewrite it.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(repo, "README"), future, future); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(index)
	time.Sleep(20 * time.Millisecond)
	inspect(t, c, loc)
	after, _ := os.Stat(index)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("inspection rewrote the index")
	}
}

func TestInheritedGitDirDoesNotRedirect(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	a := tu.InitRepo(t, filepath.Join(base, "a"))
	b := tu.InitRepo(t, filepath.Join(base, "b"))
	tu.Git(t, b, "checkout", "-q", "-b", "branch-b")
	loc := validate(t, c, a)
	t.Setenv("GIT_DIR", filepath.Join(b, ".git"))
	t.Setenv("GIT_WORK_TREE", b)
	if h := inspect(t, c, loc).Head.Value; h.Branch != "main" {
		t.Fatalf("inspection was redirected to %q", h.Branch)
	}
}

// markerProgram returns a program that creates the file marker when it runs
// and fails.
func markerProgram(t *testing.T) (program, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "ran")
	program = filepath.Join(dir, "program")
	if err := os.WriteFile(program, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return program, marker
}

func assertNotRun(t *testing.T, marker, what string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("%s ran a program from the repository configuration", what)
	}
}

func TestBackgroundGitIgnoresRepositoryFsmonitor(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	tu.Commit(t, origin, "b", "b\n", "upstream work")
	hook, marker := markerProgram(t)
	tu.Git(t, clone, "config", "core.fsmonitor", hook)
	loc := validate(t, c, clone)

	if err := c.Fetch(context.Background(), loc, git.FetchOptions{}); err != nil {
		t.Fatal(err)
	}
	in := inspect(t, c, loc)
	assertNotRun(t, marker, "the inspection")
	if err := c.MergeFastForwardOnly(context.Background(), loc, in.Upstream.Value.CommitID); err != nil {
		t.Fatal(err)
	}
	assertNotRun(t, marker, "the fast-forward")
}

func TestDetachedInspectionIgnoresShowSignature(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	// A commit with an OpenPGP signature header, written without a signing
	// program; verifying it would start gpg.program.
	object := "tree " + tu.Git(t, repo, "rev-parse", "HEAD^{tree}") + "\n" +
		"parent " + tu.Git(t, repo, "rev-parse", "HEAD") + "\n" +
		"author Test <test@example.com> 1700000000 +0000\n" +
		"committer Test <test@example.com> 1700000000 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n c2lnbmF0dXJl\n -----END PGP SIGNATURE-----\n" +
		"\nsigned subject\n"
	path := filepath.Join(t.TempDir(), "commit")
	tu.WriteFile(t, path, object)
	id := tu.Git(t, repo, "hash-object", "-t", "commit", "-w", path)
	tu.Git(t, repo, "checkout", "-q", "--detach", id)
	program, marker := markerProgram(t)
	tu.Git(t, repo, "config", "gpg.program", program)
	tu.Git(t, repo, "config", "log.showSignature", "true")

	in := inspect(t, c, validate(t, c, repo))
	if !in.LastCommit.Valid || in.LastCommit.Value == nil || in.LastCommit.Value.ID != id {
		t.Fatalf("latest commit = %+v, want %s", in.LastCommit.Value, id)
	}
	assertNotRun(t, marker, "the inspection of a detached HEAD")
}

func TestFetchAndFastForward(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	loc := validate(t, c, clone)

	tu.Commit(t, origin, "o.txt", "o\n", "upstream")
	if err := c.Fetch(context.Background(), loc, git.FetchOptions{Prune: true}); err != nil {
		t.Fatal(err)
	}
	in := inspect(t, c, loc)
	if in.Upstream.Value.Behind != 1 || in.FetchHead.Value == nil {
		t.Fatalf("after fetch: %+v fetch=%v", in.Upstream.Value, in.FetchHead.Value)
	}
	if want := tu.Git(t, clone, "rev-parse", "@{upstream}"); in.Upstream.Value.CommitID != want {
		t.Fatalf("upstream commit = %q, want %q", in.Upstream.Value.CommitID, want)
	}
	if err := c.MergeFastForwardOnly(context.Background(), loc, in.Upstream.Value.CommitID); err != nil {
		t.Fatal(err)
	}
	if u := inspect(t, c, loc).Upstream.Value; u.Behind != 0 || u.Ahead != 0 {
		t.Fatalf("after merge: %+v", u)
	}

	tu.Commit(t, origin, "o2.txt", "o\n", "upstream 2")
	tu.Commit(t, clone, "l.txt", "l\n", "local")
	if err := c.Fetch(context.Background(), loc, git.FetchOptions{}); err != nil {
		t.Fatal(err)
	}
	head := tu.Git(t, clone, "rev-parse", "HEAD")
	target := inspect(t, c, loc).Upstream.Value.CommitID
	if err := c.MergeFastForwardOnly(context.Background(), loc, target); err == nil {
		t.Fatal("divergence must be rejected")
	}
	if tu.Git(t, clone, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD changed although the merge was rejected")
	}
	if code := git.ExitCode(c.MergeFastForwardOnly(context.Background(), loc, target)); code == nil || *code == 0 {
		t.Fatal("expected a non-zero exit code")
	}
	if err := c.MergeFastForwardOnly(context.Background(), loc, "@{upstream}"); err == nil || git.ExitCode(err) != nil {
		t.Fatalf("a merge target that is not an object name must be rejected without running Git: %v", err)
	}
}

// TestFastForwardStopsAtPreviewedCommit verifies that the update applies the
// upstream commit read during inspection, not a later state of the upstream.
func TestFastForwardStopsAtPreviewedCommit(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	loc := validate(t, c, clone)

	tu.Commit(t, origin, "a.txt", "a\n", "previewed")
	tu.Git(t, clone, "fetch", "-q")
	previewed := inspect(t, c, loc).Upstream.Value.CommitID
	tu.Commit(t, origin, "b.txt", "b\n", "fetched later")
	tu.Git(t, clone, "fetch", "-q")
	if err := c.MergeFastForwardOnly(context.Background(), loc, previewed); err != nil {
		t.Fatal(err)
	}
	if head := tu.Git(t, clone, "rev-parse", "HEAD"); head != previewed {
		t.Fatalf("HEAD = %s, want the previewed commit %s", head, previewed)
	}
}

func TestInspectReadsLocalUpstreamCommit(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	tu.Git(t, repo, "checkout", "-q", "-b", "topic")
	tu.Git(t, repo, "branch", "-q", "--set-upstream-to=main")
	tu.Git(t, repo, "checkout", "-q", "main")
	tu.Commit(t, repo, "m.txt", "m\n", "main moves")
	tu.Git(t, repo, "checkout", "-q", "topic")
	loc := validate(t, c, repo)
	want := tu.Git(t, repo, "rev-parse", "main")
	for _, branches := range []bool{true, false} {
		in, err := c.Inspect(context.Background(), loc, git.InspectOptions{Branches: branches})
		if err != nil {
			t.Fatal(err)
		}
		if u := in.Upstream.Value; u.Ref != "main" || u.Behind != 1 || u.CommitID != want {
			t.Fatalf("branches=%v: upstream = %+v, want commit %s", branches, u, want)
		}
	}
}

func TestFastForwardKeepsIgnoredFiles(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Commit(t, origin, ".gitignore", "local.conf\n", "ignore local.conf")
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	loc := validate(t, c, clone)

	tu.WriteFile(t, filepath.Join(origin, "local.conf"), "upstream\n")
	tu.Git(t, origin, "add", "-f", "local.conf")
	tu.Git(t, origin, "commit", "-q", "-m", "track local.conf")
	tu.WriteFile(t, filepath.Join(clone, "local.conf"), "local\n")
	if err := c.Fetch(context.Background(), loc, git.FetchOptions{}); err != nil {
		t.Fatal(err)
	}
	head := tu.Git(t, clone, "rev-parse", "HEAD")
	if err := c.MergeFastForwardOnly(context.Background(), loc, inspect(t, c, loc).Upstream.Value.CommitID); err == nil {
		t.Fatal("a fast-forward that overwrites an ignored file must fail")
	}
	if got, _ := os.ReadFile(filepath.Join(clone, "local.conf")); string(got) != "local\n" {
		t.Fatalf("ignored file was overwritten: %q", got)
	}
	if tu.Git(t, clone, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD changed although the merge was rejected")
	}
}

// TestFastForwardLeavesSubmodulesBehind verifies that a fast-forward that
// moves a submodule leaves it at its old commit and that the inspection
// reports this as a submodule commit, not only as a modified file.
func TestFastForwardLeavesSubmodulesBehind(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	base := tu.Canonical(t, t.TempDir())
	lib := tu.InitRepo(t, filepath.Join(base, "lib"))
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	tu.Git(t, origin, "commit", "-q", "-m", "add lib")
	tu.Git(t, base, "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", origin, "clone")
	clone := filepath.Join(base, "clone")
	loc := validate(t, c, clone)

	tu.Commit(t, lib, "l.txt", "l\n", "lib moves")
	tu.Git(t, filepath.Join(origin, "lib"), "pull", "-q", "origin", "HEAD")
	tu.Git(t, origin, "commit", "-q", "-am", "bump lib")
	if err := c.Fetch(context.Background(), loc, git.FetchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.MergeFastForwardOnly(context.Background(), loc, inspect(t, c, loc).Upstream.Value.CommitID); err != nil {
		t.Fatal(err)
	}
	if w := inspect(t, c, loc).Worktree.Value; w.SubmoduleCommits != 1 || w.Modified != 1 || w.Staged != 0 {
		t.Fatalf("worktree = %+v, want one submodule commit", w)
	}
}

// countingGit returns a client whose Git executable logs one line per process
// and then runs the real Git, and a function that returns and resets the count.
func countingGit(t *testing.T) (*git.Client, func() int) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	exe := filepath.Join(dir, "git")
	script := fmt.Sprintf("#!/bin/sh\necho x >> '%s'\nexec '%s' \"$@\"\n", log, real)
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return git.New(exe), func() int {
		data, _ := os.ReadFile(log)
		os.Remove(log)
		return strings.Count(string(data), "\n")
	}
}

// TestProcessBudget guards the cost model in
// docs/adr/0008-compatibility-performance-and-testing.md: one validation
// process per candidate, and three processes per inspection, four
// for a detached HEAD, and one more for the stash count if the Git version is
// older than 2.35 or unknown. Startup time grows with the number of
// processes, so this replaces a wall-clock measurement that depends on the
// machine.
func TestProcessBudget(t *testing.T) {
	tu.IsolateGit(t)
	c, processes := countingGit(t)
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Git(t, base, "clone", "-q", origin, "clone")
	clone := filepath.Join(base, "clone")
	tu.Git(t, clone, "branch", "feature")

	loc := validate(t, c, clone)
	if n := processes(); n != 1 {
		t.Fatalf("validation started %d processes, want 1", n)
	}
	if _, err := c.Inspect(context.Background(), loc, git.InspectOptions{Branches: true}); err != nil {
		t.Fatal(err)
	}
	if n := processes(); n != 4 {
		t.Fatalf("inspection without a known Git version started %d processes, want 4", n)
	}
	version, err := c.CheckVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	processes()
	want := 3
	if !version.AtLeast(git.Version{Major: 2, Minor: 35}) {
		want++
	}
	for _, branches := range []bool{true, false} {
		if _, err := c.Inspect(context.Background(), loc, git.InspectOptions{Branches: branches}); err != nil {
			t.Fatal(err)
		}
		if n := processes(); n != want {
			t.Fatalf("inspection with Git %s (branches=%v) started %d processes, want %d", version, branches, n, want)
		}
	}
	tu.Git(t, clone, "checkout", "-q", "--detach")
	if _, err := c.Inspect(context.Background(), loc, git.InspectOptions{Branches: true}); err != nil {
		t.Fatal(err)
	}
	if n := processes(); n != want+1 {
		t.Fatalf("inspection of a detached HEAD with Git %s started %d processes, want %d", version, n, want+1)
	}
}

func TestFetchFailureCapturesStderr(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	tu.Git(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "does-not-exist"))
	err := c.Fetch(context.Background(), validate(t, c, repo), git.FetchOptions{})
	if err == nil || git.Stderr(err) == "" || git.ExitCode(err) == nil {
		t.Fatalf("err=%v stderr=%q", err, git.Stderr(err))
	}
}

// sshScript installs a fake ssh command and returns the remote URL that uses it.
func sshScript(t *testing.T, repo, body string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-ssh")
	tu.WriteFile(t, script, "#!/bin/sh\n"+body)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	tu.Git(t, repo, "remote", "add", "origin", "ssh://example.invalid/repo.git")
}

func TestFetchCannotPromptOnTerminal(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	marker := filepath.Join(t.TempDir(), "opened-tty")
	sshScript(t, repo, "if exec 3<>/dev/tty; then echo yes > "+marker+"; read answer <&3; fi\necho 'cannot prompt' >&2\nexit 255\n")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	err := c.Fetch(ctx, validate(t, c, repo), git.FetchOptions{})
	if err == nil {
		t.Fatal("fetch must fail")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("fetch waited for a prompt")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the child process could open /dev/tty")
	}
	if strings.TrimSpace(git.Stderr(err)) == "" {
		t.Fatal("stderr must be captured")
	}
}

func TestTimeoutTerminatesProcessGroup(t *testing.T) {
	tu.IsolateGit(t)
	c := git.New("git")
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	pidFile := filepath.Join(t.TempDir(), "pid")
	sshScript(t, repo, "sleep 60 &\necho $! > "+pidFile+"\nwait\n")

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := c.Fetch(ctx, validate(t, c, repo), git.FetchOptions{}); err == nil {
		t.Fatal("fetch must fail on timeout")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("timeout was not enforced")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !running(pid) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild process %d survived the timeout", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// running reports whether pid is a live process. Zombies count as terminated:
// in containers whose PID 1 does not reap orphans, such as GitHub Actions job
// containers, a killed grandchild stays a zombie indefinitely.
func running(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true // no procfs, for example on macOS
	}
	// The state follows the command name, which is enclosed in parentheses.
	if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] != 'Z'
	}
	return true
}
