package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/config"
	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	"github.com/mmo-con/headquarters/internal/launcher"
	tu "github.com/mmo-con/headquarters/internal/testutil"
)

// pump forwards scheduler events to the model until no inspection, batch, or
// confirmed update is pending, like waitEvents does in the running program.
func pump(t *testing.T, m *Model) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for m.pendingInspect() > 0 || m.ops.Running() || m.cleanupLoading() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for jobs")
		}
		m.Update(eventsMsg{events: m.app.Scheduler.Events.Next()})
	}
}

func row(t *testing.T, m *Model, id domain.RepositoryID) (int, app.BatchRow) {
	t.Helper()
	for i, r := range m.ops.Batch().Rows {
		if r.Job.RepositoryID == id {
			return i, r
		}
	}
	t.Fatalf("%s is not part of the batch", id)
	return 0, app.BatchRow{}
}

// TestFetchUpdateAndWorklistWithRealRepositories drives the user interface
// through fetch, safe update, a lazygit visit that resolves a blocker, and
// the continued update.
func TestFetchUpdateAndWorklistWithRealRepositories(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	quiet := tu.InitRepo(t, filepath.Join(base, "quiet-origin"))
	for _, name := range []string{"behind", "dirty"} {
		tu.Git(t, base, "clone", "-q", origin, name)
	}
	tu.Git(t, base, "clone", "-q", quiet, "current")
	behind, dirty, current := filepath.Join(base, "behind"), filepath.Join(base, "dirty"), filepath.Join(base, "current")
	tu.Commit(t, origin, "new.txt", "new\n", "upstream change")
	tu.WriteFile(t, filepath.Join(dirty, "README"), "local edit\n")

	cfg := config.Default()
	for _, p := range []string{behind, dirty, current} {
		cfg.Repositories = append(cfg.Repositories, config.Repository{Path: p, Enabled: true, Family: "Work"})
	}
	cfg.Lazygit.Command = "true" // stands in for lazygit; the handover itself is not executed
	a := app.New("", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m.Update(inventoryMsg{result: a.LoadInventory(context.Background())})
	pump(t, m)
	ids := map[string]domain.RepositoryID{}
	for _, r := range m.repos {
		ids[filepath.Base(r.Path)] = r.ID
		if !r.IsAvailable() || r.Inspected.Err != nil {
			t.Fatalf("%s: %v %v", r.Path, r.Availability, r.Inspected.Err)
		}
	}

	keys(m, "F")
	if m.mode != modeOperations || m.ops.Batch() == nil || m.ops.Batch().Op != domain.OperationFetch || len(m.ops.Batch().Rows) != 3 {
		t.Fatalf("F must fetch all visible repositories without a confirmation: mode=%v", m.mode)
	}
	pump(t, m)
	for _, r := range m.ops.Batch().Rows {
		if r.Result.Status != domain.StatusSucceeded {
			t.Fatalf("fetch %s: %s %v", r.Job.RepositoryID, r.Result.Status, r.Result.Error)
		}
	}
	if b := m.repo(ids["behind"]).Upstream.Value.Behind; b != 1 {
		t.Fatalf("after fetch the repository must be reinspected: behind=%d", b)
	}

	// The update fetches again, so it applies upstream commits that were not
	// known when it was confirmed.
	tu.Commit(t, origin, "newer.txt", "newer\n", "upstream change after the fetch")
	keys(m, "esc", "U")
	if m.mode != modeUpdatePreview || len(m.preview.ok) != 1 || len(m.preview.skip) != 2 {
		t.Fatalf("confirmation: ok=%d skip=%d", len(m.preview.ok), len(m.preview.skip))
	}
	if v := m.View(); strings.Contains(v, "git ") || !strings.Contains(v, "Fetch and update 3 repositories?") {
		t.Fatalf("the confirmation must describe the update instead of Git commands:\n%s", v)
	}
	keys(m, "y")
	if m.mode != modeOperations || m.ops.Batch().Op != domain.OperationFetch || !m.ops.UpdatePending() {
		t.Fatalf("a confirmed update must fetch first: mode=%v", m.mode)
	}
	pump(t, m)
	if m.ops.Batch().Op != domain.OperationUpdate {
		t.Fatalf("the update must start after the fetch: %s", m.ops.Batch().Op)
	}
	if len(m.ops.Batch().Rows) != 2 {
		t.Fatalf("the worklist must hold the update and the blocked repository, not the current one: %d rows", len(m.ops.Batch().Rows))
	}
	if _, r := row(t, m, ids["behind"]); r.Result.Status != domain.StatusSucceeded {
		t.Fatalf("update: %s %v", r.Result.Status, r.Result.Error)
	}
	if tu.Git(t, behind, "rev-parse", "HEAD") != tu.Git(t, origin, "rev-parse", "HEAD") {
		t.Fatal("behind was not fast-forwarded")
	}
	idx, r := row(t, m, ids["dirty"])
	if r.Result.Status != domain.StatusSkipped || !strings.Contains(r.Result.Error.Error(), "local changes") {
		t.Fatalf("blocked row: %s %v", r.Result.Status, r.Result.Error)
	}

	keys(m, "u")
	if m.mode != modeUpdatePreview || len(m.preview.ok) != 0 {
		t.Fatalf("an unresolved blocker must stay blocked in the continued preview: ok=%d", len(m.preview.ok))
	}
	keys(m, "esc")

	m.opSelected = idx
	cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	store := m.repo(ids["dirty"]).StoreID
	if cmd == nil || m.visit == nil || m.ops.StoreActivity(store) != "an interactive session" {
		t.Fatal("Enter must hand the selected row over to lazygit")
	}
	if m.app.Scheduler.Locks.TryLock(store) {
		t.Fatal("the store must be locked during the handover")
	}
	tu.Git(t, dirty, "checkout", "--", "README") // the user discards the change in lazygit
	m.Update(handoverDoneMsg{label: "lazygit", repo: ids["dirty"], store: store})
	pump(t, m)
	if fu, ok := m.followUp(m.ops.Batch().Rows[idx]); fu != "now eligible" || !ok {
		t.Fatalf("follow-up = %q", fu)
	}

	keys(m, "u", "y")
	pump(t, m)
	if _, r := row(t, m, ids["dirty"]); r.Result.Status != domain.StatusSucceeded {
		t.Fatalf("continued update: %s %v", r.Result.Status, r.Result.Error)
	}
	if tu.Git(t, dirty, "rev-parse", "HEAD") != tu.Git(t, origin, "rev-parse", "HEAD") {
		t.Fatal("dirty was not fast-forwarded after the blocker was resolved")
	}
	keys(m, "u")
	if m.message != "nothing left to update" {
		t.Fatalf("message = %q", m.message)
	}
}

func TestActionsOnUnavailableRepositories(t *testing.T) {
	m := newModel(t)
	cases := []struct {
		keys []string
		want string
	}{
		{[]string{"enter"}, "api is not a repository"},
		{[]string{"o", "enter"}, "api is not a repository"},
		{[]string{"f"}, "no available repositories to fetch"},
	}
	for _, tc := range cases {
		m.mode, m.message = modeNormal, ""
		keys(m, tc.keys...)
		if m.message != tc.want || !m.messageErr {
			t.Errorf("%v: message = %q", tc.keys, m.message)
		}
	}
	for _, r := range m.repos {
		if m.ops.StoreBusy(r.StoreID) {
			t.Fatalf("store of %s must stay free", r.Name)
		}
	}
	if m.ops.Batch() != nil {
		t.Fatal("nothing may be started for unavailable repositories")
	}
}

// TestConfirmedUpdateFetchesFirst checks that a confirmed update fetches its
// repositories, updates with the fetched state without opening a view, and
// skips a repository whose fetch failed even though it looked eligible.
func TestConfirmedUpdateFetchesFirst(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	tu.Commit(t, origin, "second.txt", "second\n", "second")
	for _, name := range []string{"behind", "broken"} {
		tu.Git(t, base, "clone", "-q", origin, name)
	}
	behind, broken := filepath.Join(base, "behind"), filepath.Join(base, "broken")
	tu.Git(t, broken, "reset", "-q", "--hard", "HEAD~1")
	tu.Git(t, broken, "remote", "set-url", "origin", filepath.Join(base, "gone"))
	tu.Commit(t, origin, "third.txt", "third\n", "third")
	brokenHead := tu.Git(t, broken, "rev-parse", "HEAD")

	cfg := config.Default()
	for _, p := range []string{behind, broken} {
		cfg.Repositories = append(cfg.Repositories, config.Repository{Path: p, Enabled: true})
	}
	a := app.New("", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m.Update(inventoryMsg{result: a.LoadInventory(context.Background())})
	pump(t, m)

	keys(m, "U")
	if len(m.preview.ok) != 1 || m.preview.ok[0].Repository.Path != broken {
		t.Fatalf("before the fetch only broken is known to be behind: ok=%+v", m.preview.ok)
	}
	brokenID := m.preview.ok[0].Repository.ID
	keys(m, "y")
	if m.mode != modeOperations || m.preview != nil || m.ops.Batch().Op != domain.OperationFetch || !m.ops.UpdatePending() {
		t.Fatalf("the update must fetch first: mode=%v", m.mode)
	}
	// Leaving the operations view must keep it closed when the update
	// starts, so that no key meant for another view acts on it.
	keys(m, "esc")
	pump(t, m)
	if m.mode != modeNormal || m.ops.Batch().Op != domain.OperationUpdate {
		t.Fatalf("the update must start on its own without opening a view: mode=%v op=%s", m.mode, m.ops.Batch().Op)
	}
	if tu.Git(t, behind, "rev-parse", "HEAD") != tu.Git(t, origin, "rev-parse", "HEAD") {
		t.Fatal("behind must be fast-forwarded to the fetched state")
	}
	_, r := row(t, m, brokenID)
	if r.Result.Status != domain.StatusSkipped || !strings.HasPrefix(r.Result.Error.Error(), "fetch failed") {
		t.Fatalf("a failed fetch must skip the repository: %s %v", r.Result.Status, r.Result.Error)
	}
	if tu.Git(t, broken, "rev-parse", "HEAD") != brokenHead {
		t.Fatal("a repository whose fetch failed must not be updated")
	}
}

// TestDetailPanelShowsRecentCommits checks that the recent commits of the
// selected repository are read in the background, cached for its HEAD, and
// shortened oldest first when the panel is small.
func TestDetailPanelShowsRecentCommits(t *testing.T) {
	tu.IsolateGit(t)
	repo := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "repo"))
	for _, s := range []string{"first", "second", "third", "fourth"} {
		tu.Commit(t, repo, s+".txt", s+"\n", "commit "+s)
	}
	cfg := config.Default()
	cfg.Repositories = []config.Repository{{Path: repo, Enabled: true}}
	a := app.New("", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.Update(inventoryMsg{result: a.LoadInventory(context.Background())})
	pump(t, m)
	if m.historyPending == nil && len(m.history) == 0 {
		t.Fatal("the selected repository's history must be requested")
	}
	for m.historyPending != nil {
		m.Update(eventsMsg{events: m.app.Scheduler.Events.Next()})
	}
	r := m.current()
	v := strings.Join(m.detailLines(r, 80).fit(100), "\n")
	for _, want := range []string{"RECENT COMMITS", "commit fourth", "commit third", "commit second"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the detail panel must show %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "commit first") {
		t.Fatalf("the detail panel must show only the configured number of commits:\n%s", v)
	}

	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if m.historyPending != nil {
		t.Fatal("a cached history must not be read again while HEAD is unchanged")
	}

	full := len(m.detailLines(r, 80).fit(100))
	small := strings.Join(m.detailLines(r, 80).fit(full-1), "\n")
	if strings.Contains(small, "commit second") || !strings.Contains(small, "commit third") || !strings.Contains(small, "── STATUS") {
		t.Fatalf("the oldest commit must be dropped first:\n%s", small)
	}

	m.app.Config.UI.DetailCommits = 1
	m.history = map[domain.RepositoryID]historyEntry{}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if m.historyPending != nil {
		t.Fatal("one commit comes from the inspection and needs no Git process")
	}
	if v := strings.Join(m.detailLines(r, 80).fit(100), "\n"); !strings.Contains(v, "LAST COMMIT") || !strings.Contains(v, "commit fourth") {
		t.Fatalf("one commit is the latest commit:\n%s", v)
	}
}

// drainIncoming applies events until the pending incoming request is done.
func drainIncoming(t *testing.T, m *Model) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for m.incomingPending != nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for incoming commits")
		}
		m.Update(eventsMsg{events: m.app.Scheduler.Events.Next()})
	}
}

// selectRepo moves the cursor to the repository and loads what its detail
// panel needs.
func selectRepo(t *testing.T, m *Model, id domain.RepositoryID) {
	t.Helper()
	for i, r := range m.rows {
		if r.id == id {
			m.moveRepoCursor(i)
			m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
			drainIncoming(t, m)
			return
		}
	}
	t.Fatalf("%s is not shown", id)
}

func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime()
}

// panelSection returns the lines of a titled section of the detail panel.
func panelSection(m *Model, title string) string {
	var out []string
	in := false
	for _, l := range strings.Split(ansi.Strip(strings.Join(m.detailLines(m.current(), 100).fit(100), "\n")), "\n") {
		if strings.HasPrefix(l, "── ") {
			in = strings.HasPrefix(l, "── "+title+" ")
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// TestIncomingCommitsWithRealRepositories checks the incoming commits in the
// detail panel, the update confirmation, and the operations view, read from
// local refs without fetching.
func TestIncomingCommitsWithRealRepositories(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	origin := tu.InitRepo(t, filepath.Join(base, "origin"))
	for _, name := range []string{"behind", "current", "diverged"} {
		tu.Git(t, base, "clone", "-q", origin, name)
	}
	standalone := tu.InitRepo(t, filepath.Join(base, "standalone"))
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		tu.Commit(t, origin, n+".txt", n+"\n", "upstream "+n)
	}
	behind, diverged := filepath.Join(base, "behind"), filepath.Join(base, "diverged")
	tu.Git(t, behind, "fetch", "-q")
	tu.Git(t, diverged, "fetch", "-q")
	tu.Commit(t, diverged, "local.txt", "local\n", "local work")
	tu.Commit(t, origin, "6.txt", "6\n", "upstream 6 unfetched")
	fetchHead := filepath.Join(behind, ".git", "FETCH_HEAD")
	fetchedAt := modTime(t, fetchHead)
	tracking := tu.Git(t, behind, "rev-parse", "origin/main")

	cfg := config.Default()
	cfg.UI.IncomingCommits = 3
	for _, p := range []string{behind, filepath.Join(base, "current"), diverged, standalone} {
		cfg.Repositories = append(cfg.Repositories, config.Repository{Path: p, Enabled: true})
	}
	a := app.New("", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.Update(inventoryMsg{result: a.LoadInventory(context.Background())})
	pump(t, m)
	ids := map[string]domain.RepositoryID{}
	for _, r := range m.repos {
		ids[filepath.Base(r.Path)] = r.ID
	}

	selectRepo(t, m, ids["behind"])
	s := panelSection(m, "INCOMING COMMITS")
	for _, want := range []string{"upstream 5", "upstream 4", "upstream 3", "Test", "… and 2 more"} {
		if !strings.Contains(s, want) {
			t.Fatalf("a repository behind its upstream must list %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "upstream 2") || strings.Contains(s, "unfetched") {
		t.Fatalf("the list must be truncated and contain only fetched commits:\n%s", s)
	}
	if v := ansi.Strip(strings.Join(m.detailLines(m.current(), 100).fit(100), "\n")); strings.Index(v, "── INCOMING COMMITS") > strings.Index(v, "── RECENT COMMITS") {
		t.Fatalf("incoming commits must come before the recent commits:\n%s", v)
	}
	if modTime(t, fetchHead) != fetchedAt || tu.Git(t, behind, "rev-parse", "origin/main") != tracking {
		t.Fatal("loading incoming commits must not fetch")
	}

	selectRepo(t, m, ids["diverged"])
	if s := panelSection(m, "INCOMING COMMITS"); !strings.Contains(s, "upstream 5") || !strings.Contains(s, "… and 2 more") || strings.Contains(s, "local work") {
		t.Fatalf("a diverged repository must list only the upstream commits:\n%s", s)
	}
	for _, name := range []string{"current", "standalone"} {
		selectRepo(t, m, ids[name])
		if s := panelSection(m, "INCOMING COMMITS"); s != "" || m.incomingPending != nil {
			t.Fatalf("%s has no incoming commits and needs no Git process:\n%s", name, s)
		}
	}

	m.repo(ids["standalone"]).Upstream = domain.Known[domain.UpstreamState]{}
	selectRepo(t, m, ids["behind"])
	keys(m, "U")
	drainIncoming(t, m)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, m.icons.behind+"5 · fetched") || !strings.Contains(v, m.icons.behind+"5 · diverged from upstream") ||
		!strings.Contains(v, m.icons.behind+"? · state is unknown") {
		t.Fatalf("the confirmation must show the incoming count per repository and an unknown state as unknown:\n%s", v)
	}
	if !strings.Contains(v, "upstream 5") || !strings.Contains(v, "… and 2 more") {
		t.Fatalf("the confirmation must list the incoming commits of the selected repository:\n%s", v)
	}

	keys(m, "y")
	pump(t, m)
	if tu.Git(t, behind, "rev-parse", "HEAD") != tu.Git(t, origin, "rev-parse", "HEAD") {
		t.Fatal("behind was not fast-forwarded")
	}
	m.opSelected, _ = row(t, m, ids["behind"])
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	drainIncoming(t, m)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "upstream 6 unfetched") || !strings.Contains(v, "… and 3 more") {
		t.Fatalf("the operations view must list the commits the update applied:\n%s", v)
	}
}

// TestInteractiveFetchRetry checks that t hands a failed fetch over to the
// terminal and that the failure is forgotten once the user fetched there.
func TestInteractiveFetchRetry(t *testing.T) {
	tu.IsolateGit(t)
	path := tu.InitRepo(t, filepath.Join(tu.Canonical(t, t.TempDir()), "api"))
	cfg := config.Default()
	cfg.Repositories = []config.Repository{{Path: path, Enabled: true}}
	a := app.New("", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(inventoryMsg{result: a.LoadInventory(context.Background())})
	pump(t, m)
	r := m.repos[0]

	offline(m)
	if n, ok := m.ops.Fetch(m.repos, []domain.Repository{r}); !ok {
		t.Fatal(n.Text)
	}
	m.mode, m.opSelected = modeOperations, 0
	keys(m, "t")
	if m.message != "select a failed fetch to retry it in the terminal" || m.ops.StoreActivity(r.StoreID) != "fetch" {
		t.Fatalf("a running fetch must not be retried: message = %q", m.message)
	}

	setRow(m, 0, domain.StatusFailed, errors.New("authentication failed"))
	if _, ok := m.ops.LastResult(r.ID); !ok {
		t.Fatal("the failed fetch must be remembered")
	}
	keys(m, "t")
	if m.ops.StoreActivity(r.StoreID) != "an interactive session" {
		t.Fatalf("t must hand the failed fetch over to the terminal: message = %q", m.message)
	}
	m.Update(handoverDoneMsg{label: "git fetch", repo: r.ID, store: r.StoreID})
	pump(t, m)
	if m.message != "git fetch finished" || m.ops.StoreBusy(r.StoreID) {
		t.Fatalf("message = %q, busy = %v", m.message, m.ops.StoreBusy(r.StoreID))
	}
	if _, ok := m.ops.LastResult(r.ID); ok {
		t.Fatal("a fetch in the terminal must replace the failed result")
	}
}

func TestLauncherMenu(t *testing.T) {
	m := newModel(t)
	api := m.current()
	api.Availability = domain.Available
	m.app.Launchers = launcher.NewCustom([]config.Launcher{
		{Name: "shell", Command: []string{"true"}}, {Name: "broken", Command: []string{"nope-xyz"}},
	})
	choose := func(item int) {
		m.message, m.messageErr = "", false
		keys(m, "o")
		for range item {
			keys(m, "j")
		}
		keys(m, "enter")
	}

	choose(1)
	if !m.messageErr || !strings.HasPrefix(m.message, "launcher broken: ") {
		t.Errorf("broken launcher: message = %q", m.message)
	}
	choose(2)
	if m.message != "no remote configured" {
		t.Errorf("browser without remote: message = %q", m.message)
	}
	api.PrimaryRemote = &domain.Remote{Name: "origin", URL: "/srv/git/api.git"}
	choose(2)
	if m.message != "cannot derive an HTTPS URL from /srv/git/api.git" {
		t.Errorf("browser with local remote: message = %q", m.message)
	}

	offline(m)
	if n, ok := m.ops.Fetch(m.repos, []domain.Repository{*api}); !ok {
		t.Fatal(n.Text)
	}
	choose(0)
	if m.message != "fetch in progress for api" || m.ops.StoreActivity(api.StoreID) != "fetch" {
		t.Errorf("a busy store must refuse the handover: message = %q", m.message)
	}
	setRow(m, 0, domain.StatusSucceeded, nil)
	choose(0)
	if m.messageErr || m.ops.StoreActivity(api.StoreID) != "an interactive session" {
		t.Errorf("the launcher must get the terminal: message = %q", m.message)
	}
}

func TestFailedTerminalRestoreQuits(t *testing.T) {
	m := newModel(t)
	store := m.current().StoreID
	m.ops.BeginHandover(store)
	_, cmd := m.Update(handoverDoneMsg{label: "lazygit", store: store, err: errors.New("tty lost"), fatal: true})
	if err := m.Err(); err == nil || err.Error() != "restoring the terminal after lazygit failed: tty lost" {
		t.Fatalf("Err() = %v", err)
	}
	if cmd == nil {
		t.Fatal("the program must quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || m.ops.StoreBusy(store) {
		t.Fatal("the program must quit and release the store")
	}
}

// TestDeleteMergedBranchesWithRealRepositories drives the preview and the
// deletion of merged branches: a merged and a rebase-merged branch are
// deleted, and a branch whose upstream was deleted without a merge is listed
// with its reason.
func TestDeleteMergedBranchesWithRealRepositories(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	up := filepath.Join(base, "up.git")
	tu.Git(t, "", "init", "-q", "--bare", up)
	clone := filepath.Join(base, "clone")
	tu.Git(t, "", "clone", "-q", up, clone)
	tu.Commit(t, clone, "a", "a\n", "init")
	tu.Git(t, clone, "push", "-q", "origin", "main")
	tu.Git(t, clone, "remote", "set-head", "origin", "--auto")
	for _, b := range []string{"done", "rebased", "dropped"} {
		tu.Git(t, clone, "switch", "-q", "-c", b, "main")
		tu.Commit(t, clone, b, b+"\n", b+" work")
		tu.Git(t, clone, "push", "-q", "-u", "origin", b)
	}
	tu.Git(t, clone, "switch", "-q", "main")
	tu.Git(t, clone, "merge", "-q", "--no-ff", "-m", "merge done", "done")
	tu.Git(t, clone, "push", "-q", "origin", "main")
	srv := filepath.Join(base, "server")
	tu.Git(t, "", "clone", "-q", up, srv)
	tu.Git(t, srv, "cherry-pick", "origin/rebased")
	tu.Git(t, srv, "push", "-q", "origin", "main", ":done", ":rebased", ":dropped")
	tu.Git(t, clone, "fetch", "-q", "--prune")

	cfg := config.Default()
	cfg.Repositories = []config.Repository{{Path: clone, Enabled: true, Family: "Work"}}
	client, version, err := git.Open(context.Background(), "git")
	if err != nil {
		t.Fatal(err)
	}
	// Older Git keeps the rebase-merged branch, with a reason naming 2.38.
	mergeTree := version.AtLeast(git.Version{Major: 2, Minor: 38})
	a := app.New("", cfg, client)
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m.Update(inventoryMsg{result: a.LoadInventory(context.Background())})
	pump(t, m)

	keys(m, "d")
	batch := m.cleanup.batch
	keys(m, "esc")
	if m.mode != modeNormal || m.cleanup != nil || m.routes[batch] != nil || m.ops.Batch() != nil {
		t.Fatal("Esc must close the preview, stop reading, and delete nothing")
	}

	keys(m, "d")
	if m.mode != modeCleanupPreview || !m.cleanupLoading() {
		t.Fatalf("d must open the preview and read the branches: mode=%v", m.mode)
	}
	keys(m, "enter")
	if m.mode != modeCleanupPreview || m.ops.Batch() != nil {
		t.Fatal("the preview must not be confirmed before the branches are read")
	}
	pump(t, m)
	v := ansi.Strip(m.View())
	head, dropped := "Delete 2 merged branches", "upstream gone, changes not in origin/main"
	if !mergeTree {
		head, dropped = "Delete 1 merged branch", "needs Git 2.38"
	}
	if !strings.Contains(v, head) || !strings.Contains(v, "done") || !strings.Contains(v, "rebased") ||
		!strings.Contains(v, "dropped") || !strings.Contains(v, dropped) {
		t.Fatalf("preview:\n%s", v)
	}

	keys(m, "y")
	if m.mode != modeOperations || m.ops.Batch() == nil || m.ops.Batch().Op != domain.OperationCleanup {
		t.Fatalf("a confirmed preview must start the deletion: mode=%v", m.mode)
	}
	pump(t, m)
	status := map[string]domain.OperationStatus{}
	for _, r := range m.ops.Batch().Rows {
		status[r.Branch] = r.Result.Status
	}
	rebased, deleted := domain.StatusSucceeded, []string{"done", "rebased"}
	if !mergeTree {
		rebased, deleted = domain.StatusSkipped, []string{"done"}
	}
	if len(status) != 3 || status["done"] != domain.StatusSucceeded || status["rebased"] != rebased ||
		status["dropped"] != domain.StatusSkipped {
		t.Fatalf("rows = %v", status)
	}
	for _, b := range deleted {
		if out, _ := tu.TryGit(clone, "rev-parse", "--verify", "-q", "refs/heads/"+b); out != "" {
			t.Fatalf("%s must be deleted", b)
		}
	}
	tu.Git(t, clone, "rev-parse", "--verify", "-q", "refs/heads/dropped")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "rebased") || !strings.Contains(v, "dropped") {
		t.Fatalf("the operations view must name the branches:\n%s", v)
	}
}

func TestCleanupPreviewHintsAtMissingFetch(t *testing.T) {
	tu.IsolateGit(t)
	base := tu.Canonical(t, t.TempDir())
	up := filepath.Join(base, "up.git")
	tu.Git(t, "", "init", "-q", "--bare", up)
	clone := filepath.Join(base, "clone")
	tu.Git(t, "", "clone", "-q", up, clone)
	tu.Commit(t, clone, "a", "a\n", "init")
	tu.Git(t, clone, "push", "-q", "origin", "main")
	tu.Git(t, clone, "remote", "set-head", "origin", "--auto")
	tu.Git(t, clone, "fetch", "-q")
	local := tu.InitRepo(t, filepath.Join(base, "local"))

	cfg := config.Default()
	cfg.Repositories = []config.Repository{{Path: clone, Enabled: true}, {Path: local, Enabled: true}}
	client, _, err := git.Open(context.Background(), "git")
	if err != nil {
		t.Fatal(err)
	}
	a := app.New("", cfg, client)
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m.Update(inventoryMsg{result: a.LoadInventory(context.Background())})
	pump(t, m)

	const hint = "1 repository was not fetched since start"
	keys(m, "D")
	pump(t, m)
	if v := ansi.Strip(m.View()); !strings.Contains(v, hint) || !strings.Contains(v, "press f") {
		t.Fatalf("a repository fetched only before the start must be named, one without a remote not:\n%s", v)
	}
	keys(m, "esc")

	keys(m, "F")
	pump(t, m)
	keys(m, "esc", "D")
	pump(t, m)
	if v := ansi.Strip(m.View()); strings.Contains(v, "not fetched since start") {
		t.Fatalf("no hint after a fetch:\n%s", v)
	}
}
