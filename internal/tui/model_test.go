package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/config"
	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	"github.com/mmo-con/headquarters/internal/inventory"
	"github.com/mmo-con/headquarters/internal/jobs"
)

func repo(name string, family domain.FamilyID, dirty bool) domain.Repository {
	w := domain.WorktreeState{UntrackedChecked: true}
	if dirty {
		w.Modified = 1
	}
	return domain.Repository{
		ID: domain.RepositoryID("/r/" + name), StoreID: domain.StoreID("/r/" + name + "/.git"),
		Name: name, Path: "/r/" + name, Family: family,
		// Unavailable entries are not inspected, which keeps the test free of
		// Git processes.
		Availability: domain.NotARepository,
		Inspected:    domain.InspectionInfo{FinishedAt: time.Now(), Generation: 1},
		Inspection:   domain.Inspection{Worktree: domain.KnownValue(w)},
	}
}

func newModel(t *testing.T) *Model {
	t.Helper()
	a := app.New("/none", config.Default(), git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(inventoryMsg{result: inventory.Result{Repositories: []domain.Repository{
		repo("api", "Work", true), repo("frontend", "Work", false), repo("dotfiles", "Personal", false),
	}}})
	return m
}

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

// inspectionEvent returns a finished inspection of a batch that belongs to
// the current inventory.
func inspectionEvent(m *Model, id domain.RepositoryID, p app.InspectionPayload) eventsMsg {
	const batchID = 999
	m.routes[batchID] = m.applyInspection
	return eventsMsg{events: []jobs.Event{{BatchID: batchID,
		Result: domain.OperationResult{RepositoryID: id, Status: domain.StatusSucceeded}, Payload: p}}}
}

// offline replaces the operations of m with ones whose batches never run:
// their jobs are only listed, and setRow or events deliver their results.
func offline(m *Model) {
	next := uint64(1000)
	m.ops = app.NewOperations(m.app, func([]jobs.Job) uint64 {
		next++
		m.routes[next] = m.applyBatchEvent
		return next
	}, func() time.Time { return m.now() })
}

// fetchBatch starts an offline fetch with one row per repository ID; each
// repository has its own store.
func fetchBatch(t *testing.T, m *Model, ids ...domain.RepositoryID) *app.Batch {
	t.Helper()
	offline(m)
	var targets []domain.Repository
	for _, id := range ids {
		targets = append(targets, domain.Repository{ID: id, StoreID: domain.StoreID(id + "/.git"), Availability: domain.Available})
	}
	if n, ok := m.ops.Fetch(m.repos, targets); !ok {
		t.Fatal(n.Text)
	}
	return m.ops.Batch()
}

// setRow records a result for row i of the current batch without inspecting
// the repository again.
func setRow(m *Model, i int, s domain.OperationStatus, err error) {
	b := m.ops.Batch()
	row := b.Rows[i]
	m.ops.Handle(jobs.Event{BatchID: b.ID, JobID: i, Repositories: []domain.RepositoryID{row.Job.RepositoryID},
		Result: domain.OperationResult{RepositoryID: row.Job.RepositoryID, StoreID: row.Job.StoreID,
			Operation: b.Op, Status: s, Error: err}})
}

func names(m *Model) string {
	var out []string
	for _, id := range m.visible {
		out = append(out, m.repo(id).Name)
	}
	return strings.Join(out, " ")
}

func TestRepoViewportKeepsContextAndLetsCursorMoveBackUp(t *testing.T) {
	m := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 16}) // seven repository rows
	var repos []domain.Repository
	for i := range 24 {
		repos = append(repos, repo(fmt.Sprintf("repo-%02d", i), "Work", false))
	}
	m.Update(inventoryMsg{result: inventory.Result{Repositories: repos}})

	keys(m, "j", "j", "j", "j", "j")
	if m.current().Name != "repo-05" || m.repoTop != 1 || !strings.Contains(m.View(), "repo-07") {
		t.Fatalf("scroll should keep two rows below the cursor; cursor=%d top=%d", m.cursor, m.repoTop)
	}
	keys(m, "k")
	if m.current().Name != "repo-04" || m.repoTop != 1 {
		t.Fatalf("reversing direction must move the cursor within the view; cursor=%d top=%d", m.cursor, m.repoTop)
	}
	keys(m, "G", "k")
	if m.current().Name != "repo-22" || m.repoTop != 17 || !strings.Contains(m.View(), "repo-23") {
		t.Fatalf("moving up from the last repo must keep the last page in view; cursor=%d top=%d", m.cursor, m.repoTop)
	}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 16}) // eleven repository rows
	if m.current().Name != "repo-22" || m.repoTop != 13 {
		t.Fatalf("resize must preserve selection and fill the view; cursor=%d top=%d", m.cursor, m.repoTop)
	}
	keys(m, "/", "repo-22", "enter")
	if m.repoTop != 0 || names(m) != "repo-22" || !strings.Contains(m.View(), "repo-22") {
		t.Fatalf("filter must reset the viewport for a short list; top=%d, repos=%q", m.repoTop, names(m))
	}
}

func TestStaleFilterUsesTheClockOfTheModel(t *testing.T) {
	m := newModel(t)
	// Five days before the clock of the model, so the fetch is not stale
	// then, but long before the time the test runs.
	now := time.Date(2020, 1, 10, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	fetched := now.Add(-5 * 24 * time.Hour)
	r := repo("svc", "Work", false)
	r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "https://example.com/svc.git"}})
	r.LastFetch = domain.KnownValue(&fetched)
	m.Update(inventoryMsg{result: inventory.Result{Repositories: []domain.Repository{r}}})
	if h := m.headerLine(); !strings.Contains(h, " 8 stale 0 ") {
		t.Errorf("the header must count stale fetches at the time of the model: %q", h)
	}
	keys(m, "8")
	if got := names(m); got != "" {
		t.Errorf("is:stale must match at the time of the model, got %q", got)
	}
}

func TestQuickFilterHeaderMarksActiveFilter(t *testing.T) {
	m := newModel(t)
	if view := m.View(); !strings.Contains(view, "headquarters") {
		t.Fatalf("view lacks application name: %q", view)
	}
	if h := m.headerLine(); !strings.Contains(h, " 1 dirty 1 ") || strings.Contains(h, "[") {
		t.Fatalf("header = %q", h)
	}
	keys(m, "1")
	if h := m.headerLine(); !strings.Contains(h, "[1 dirty 1]") || strings.Contains(h, "[2") {
		t.Fatalf("the active quick filter must be bracketed: %q", h)
	}
	if h := m.headerLine(); !strings.Contains(h, " 8 stale 0 ") {
		t.Fatalf("the header must count stale fetches: %q", h)
	}
	th := m.theme
	if s := chipStyle(th, th.dirty, 1, false); s.GetReverse() || s.GetForeground() != th.dirty.GetForeground() {
		t.Error("a counter with matches must be colored text without a background")
	}
	if !chipStyle(th, th.dirty, 1, true).GetReverse() || !chipStyle(th, th.dirty, 0, true).GetReverse() {
		t.Error("only the active quick filter has a background")
	}
	if s := chipStyle(th, th.dirty, 0, false); s.GetReverse() || s.GetForeground() != th.dim.GetForeground() {
		t.Error("a counter without matches must be dim")
	}
}

func TestFilterFamilyAndSelection(t *testing.T) {
	m := newModel(t)
	if names(m) != "api dotfiles frontend" {
		t.Fatalf("visible = %q", names(m))
	}
	keys(m, "j", "j") // cursor on frontend
	keys(m, "/", "i", "s", ":", "d", "i", "r", "t", "y", "enter")
	if names(m) != "api" || m.mode != modeNormal {
		t.Fatalf("filter: %q", names(m))
	}
	keys(m, "esc")
	if names(m) != "api dotfiles frontend" {
		t.Fatalf("Esc must clear the filter: %q", names(m))
	}
	keys(m, "tab")
	if m.filter.Family != "Personal" || names(m) != "dotfiles" {
		t.Fatalf("family: %q %q", m.filter.Family, names(m))
	}
	keys(m, "tab", "tab")
	if m.filter.Family != "" {
		t.Fatalf("family cycle must return to All: %q", m.filter.Family)
	}
}

func TestFamilyTreeNavigationSortingAndFiltering(t *testing.T) {
	m := newModel(t)
	keys(m, "v")
	view := m.View()
	if names(m) != "" || m.currentRow().family != "Work" ||
		!strings.Contains(view, "▸ Personal (1)") || !strings.Contains(view, "▸ Work (2)") {
		t.Fatal("the tree must start with only collapsed family rows and select the current repo's family")
	}
	keys(m, "g", "enter", "G", "enter")
	if m.current() != nil || names(m) != "dotfiles api frontend" {
		t.Fatal("Enter on a collapsed family must show its repos and keep its header selected")
	}
	keys(m, "enter")
	if names(m) != "dotfiles" || !strings.Contains(m.View(), "▸ Work (2)") {
		t.Fatal("Enter on a family must collapse it and keep the family selected")
	}
	keys(m, "v", "v")
	if m.currentRow().family != "Work" || m.current() != nil || names(m) != "dotfiles" {
		t.Fatal("switching views must retain expansion states and recover the header selection")
	}
	keys(m, "enter", "s", "j", "j", "enter")
	// With no commit data, the sort falls back to name. Give frontend a
	// commit so that latest-commit sorting changes the order within Work.
	m.repo("/r/frontend").LastCommit = domain.KnownValue(&domain.CommitSummary{Time: time.Now()})
	m.recompute()
	if names(m) != "dotfiles frontend api" {
		t.Fatalf("sort must apply within each family: %s", names(m))
	}
	keys(m, "/", "front", "enter")
	if names(m) != "frontend" || strings.Contains(m.View(), "Personal") || !strings.Contains(m.View(), "▾ Work (1)") {
		t.Fatal("the tree must show only families with matches and count matching repositories")
	}
	keys(m, "G")
	if m.current().Name != "frontend" {
		t.Fatal("End must reach the last repository even when a header takes up a row")
	}
}

func TestFamilyTreeBatchScope(t *testing.T) {
	m := newModel(t)
	keys(m, " ", "v", "g", "enter") // mark api, then expand only Personal
	if ts, hidden := m.targets(false); len(ts) != 1 || ts[0].Name != "api" || hidden != 1 {
		t.Fatalf("marked targets must include the collapsed repo: targets=%+v, hidden=%d", ts, hidden)
	}
	if ts, hidden := m.targets(true); len(ts) != 1 || ts[0].Name != "dotfiles" || hidden != 0 {
		t.Fatal("F and U must act on expanded repos only, regardless of marks")
	}
	keys(m, "u")
	if len(m.preview.skip) != 1 || m.preview.skip[0].Repository.Name != "api" || m.preview.hidden != 1 {
		t.Fatal("u must include explicitly marked collapsed repos")
	}
	if !strings.Contains(m.View(), "hidden by filters or collapsed groups") {
		t.Fatal("the update confirmation must explain hidden marks")
	}
	keys(m, "esc", "U")
	if len(m.preview.skip) != 1 || m.preview.skip[0].Repository.Name != "dotfiles" || m.preview.hidden != 0 {
		t.Fatal("U must confirm the expanded repos, regardless of marks")
	}
	keys(m, "esc", "A", "u")
	if m.mode != modeUpdatePreview || len(m.preview.skip) != 1 || m.preview.skip[0].Repository.Name != "dotfiles" {
		t.Fatal("u without marks on a family row must confirm the family's repositories")
	}
	keys(m, "esc", "j", "u")
	if len(m.preview.skip) != 1 || m.preview.skip[0].Repository.Name != "dotfiles" {
		t.Fatal("u without marks must confirm the repository under the cursor")
	}
	keys(m, "esc", "a")
	if len(m.marks) != 1 || !m.marks["/r/dotfiles"] {
		t.Fatal("mark-all must mark only the expanded repos")
	}
}

func TestTreeFilterOpensFamiliesWithMatches(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "/", "api", "enter")
	if names(m) != "api" || !strings.Contains(m.View(), "▾ Work (1)") {
		t.Fatalf("a text filter must open families with matches: %q", names(m))
	}
	keys(m, "esc")
	if names(m) != "" {
		t.Fatalf("clearing the filter must collapse the families again: %q", names(m))
	}
	keys(m, "1")
	if names(m) != "api" {
		t.Fatalf("a quick filter must open families with matches: %q", names(m))
	}
}

func TestTreeExpansionDuringFilterIsTemporary(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "g", "enter") // expand Personal only
	keys(m, "/", "o", "enter")
	if names(m) != "dotfiles frontend" {
		t.Fatalf("the filter must open all families with matches: %q", names(m))
	}
	keys(m, "g", "enter")
	if names(m) != "frontend" {
		t.Fatalf("Enter during a filter must collapse the family: %q", names(m))
	}
	keys(m, "esc")
	if names(m) != "dotfiles" {
		t.Fatalf("clearing the filter must restore the previous expansion: %q", names(m))
	}
	keys(m, "/", "o", "enter")
	if names(m) != "dotfiles frontend" {
		t.Fatalf("a new filter must start without earlier temporary changes: %q", names(m))
	}
}

func TestTreeExpandAndCollapseKeys(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "g", "l")
	if names(m) != "dotfiles" {
		t.Fatalf("l must expand the family: %q", names(m))
	}
	keys(m, "G", "right")
	if names(m) != "dotfiles api frontend" {
		t.Fatalf("→ must expand the family: %q", names(m))
	}
	keys(m, "G", "k", "k", "h")
	if names(m) != "dotfiles" || m.currentRow().family != "Work" || m.currentRow().count == 0 {
		t.Fatalf("h on a family row must collapse it: %q", names(m))
	}
	keys(m, "g", "left")
	if names(m) != "" {
		t.Fatalf("← must collapse the family: %q", names(m))
	}
}

func TestTreeCollapseFromRepositoryRow(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "g", "enter", "G", "enter", "G") // cursor on frontend
	keys(m, "h")
	if row := m.currentRow(); names(m) != "dotfiles" || row.count == 0 || row.family != "Work" {
		t.Fatalf("h on a repository must collapse its family and select the family row: %q %+v", names(m), row)
	}
	keys(m, "g", "j", "left") // cursor on dotfiles
	if row := m.currentRow(); names(m) != "" || row.count == 0 || row.family != "Personal" {
		t.Fatalf("← on a repository must collapse its family and select the family row: %q %+v", names(m), row)
	}
}

func TestTreeToggleAllFamilies(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "z")
	if names(m) != "dotfiles api frontend" {
		t.Fatalf("z with all families collapsed must expand all: %q", names(m))
	}
	keys(m, "z")
	if names(m) != "" {
		t.Fatalf("z with expanded families must collapse all: %q", names(m))
	}
	keys(m, "g", "enter", "z")
	if names(m) != "" {
		t.Fatalf("z with one expanded family must collapse all: %q", names(m))
	}
}

func TestSpaceOnFamilyRowMarksItsRepositories(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "G", " ")
	if len(m.marks) != 2 || !m.marks["/r/api"] || !m.marks["/r/frontend"] {
		t.Fatalf("Space on a collapsed family must mark its repositories: %v", m.marks)
	}
	keys(m, "G", " ")
	if len(m.marks) != 0 {
		t.Fatalf("Space on a fully marked family must unmark its repositories: %v", m.marks)
	}
	keys(m, "/", "front", "enter", "g", " ")
	if len(m.marks) != 1 || !m.marks["/r/frontend"] {
		t.Fatalf("Space on a family must mark only matching repositories: %v", m.marks)
	}
}

func TestFetchAndUpdateOnFamilyRow(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "G") // collapsed Work row
	if ts, hidden := m.targets(false); len(ts) != 2 || hidden != 0 {
		t.Fatalf("f on a family row must target its repositories: %d targets, %d hidden", len(ts), hidden)
	}
	keys(m, "u")
	if m.mode != modeUpdatePreview || len(m.preview.skip) != 2 || m.preview.hidden != 0 {
		t.Fatal("u on a family row must confirm its repositories without hidden marks")
	}
	keys(m, "esc")
	m.marks["/r/dotfiles"] = true
	if ts, hidden := m.targets(false); len(ts) != 1 || ts[0].Name != "dotfiles" || hidden != 1 {
		t.Fatal("marks must take precedence over the family row")
	}
}

func TestFamilyJumpKeys(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "z", "g", "]") // rows: Personal, dotfiles, Work, api, frontend
	if m.cursor != 2 {
		t.Fatalf("] must move to the next family row: %d", m.cursor)
	}
	keys(m, "]")
	if m.cursor != 2 {
		t.Fatalf("] on the last family must stay: %d", m.cursor)
	}
	keys(m, "G", "[")
	if m.cursor != 2 {
		t.Fatalf("[ on a repository must move to the previous family row: %d", m.cursor)
	}
	keys(m, "[")
	if m.cursor != 0 {
		t.Fatalf("[ must move to the previous family row: %d", m.cursor)
	}
}

func TestTreeKeysDoNothingInList(t *testing.T) {
	m := newModel(t)
	keys(m, "j", "h", "l", "left", "right", "z", "[", "]")
	if m.tree || m.cursor != 1 || names(m) != "api dotfiles frontend" || len(m.marks) != 0 {
		t.Fatalf("tree keys must do nothing in the list: cursor=%d names=%q", m.cursor, names(m))
	}
}

func TestFamilyPickerCombinesWithFiltersAndPreservesMarks(t *testing.T) {
	m := newModel(t)
	keys(m, "a", "1", "t", "wO")
	if m.mode != modeFamily || !strings.Contains(m.View(), "Search: wO") || m.filter.Family != "" {
		t.Fatal("typing in the picker must show the query without applying a family")
	}
	keys(m, "enter")
	if m.mode != modeNormal || m.filter.Family != "Work" || m.filter.Text != "is:dirty" || names(m) != "api" {
		t.Fatalf("pick Work: family=%q, text=%q, repos=%q", m.filter.Family, m.filter.Text, names(m))
	}
	if _, hidden := m.targets(false); len(m.marks) != 3 || hidden != 2 {
		t.Fatalf("picking a family must preserve all marks: marks=%v hidden=%d", m.marks, hidden)
	}
	keys(m, "tab")
	if m.filter.Family != "" || m.filter.Text != "is:dirty" {
		t.Fatalf("Tab must cycle from the chosen family to All without clearing text: %+v", m.filter)
	}
}

func TestFamilyPickerCancelAndClear(t *testing.T) {
	m := newModel(t)
	keys(m, "tab", "t", "does not match", "enter")
	if m.mode != modeFamily || !strings.Contains(m.View(), "No matching families") {
		t.Fatal("Enter with no matches must leave the picker open")
	}
	keys(m, "esc")
	if m.mode != modeNormal || m.filter.Family != "Personal" || names(m) != "dotfiles" {
		t.Fatal("Esc must leave the applied family unchanged")
	}
	keys(m, "t")
	if m.familyQuery != "" {
		t.Fatal("a reopened picker must start with an empty query")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	keys(m, "enter")
	if m.mode != modeNormal || m.filter.Family != "" || names(m) != "api dotfiles frontend" {
		t.Fatal("choosing All families must remove the family filter")
	}
}

func TestMarksTargetsAndHiddenCount(t *testing.T) {
	m := newModel(t)
	keys(m, " ", " ") // mark api and dotfiles
	if len(m.marks) != 2 {
		t.Fatalf("marks = %v", m.marks)
	}
	keys(m, "/", "f", "r", "o", "n", "t", "enter")
	targets, hidden := m.targets(false)
	if len(targets) != 2 || hidden != 2 {
		t.Fatalf("targets=%d hidden=%d", len(targets), hidden)
	}
	if !strings.Contains(m.View(), "2 marked · A: clear marks") {
		t.Fatal("repository panel must show the count and shortcut even when all marks are hidden")
	}
	keys(m, "u")
	if m.mode != modeUpdatePreview || m.preview.hidden != 2 {
		t.Fatalf("u with marks must open the confirmation: mode=%v hidden=%d", m.mode, m.preview.hidden)
	}
	if !strings.Contains(m.View(), "Includes 2 marked repositories hidden by the filter") {
		t.Fatal("confirmation must mention hidden marks")
	}
	keys(m, "esc", "A")
	if t2, _ := m.targets(false); len(t2) != 1 || len(m.marks) != 0 {
		t.Fatalf("without marks the cursor row is the target: %d", len(t2))
	}
	if strings.Contains(m.View(), "A: clear marks") {
		t.Fatal("repository panel must hide the shortcut once marks are cleared")
	}
}

func TestBatchEventsAreApplied(t *testing.T) {
	m := newModel(t)
	r := m.repos[0]
	b := fetchBatch(t, m, r.ID)
	if m.ops.StoreActivity(r.StoreID) != "fetch" {
		t.Fatal("queued fetch must mark the store busy")
	}
	m.Update(eventsMsg{events: []jobs.Event{{BatchID: b.ID, JobID: 0, Repositories: []domain.RepositoryID{r.ID},
		Result: domain.OperationResult{RepositoryID: r.ID, Operation: domain.OperationFetch, Status: domain.StatusFailed}}}})
	if !b.Done() || m.ops.StoreBusy(r.StoreID) || m.routes[b.ID] != nil {
		t.Fatal("batch must be finished")
	}
	if got := m.upstreamText(m.repo(r.ID)); got != "✗ fetch failed" {
		t.Fatalf("upstream = %q", got)
	}
	m.repo(r.ID).Availability = domain.Available
	if got := m.stateText(m.repo(r.ID)); !strings.HasPrefix(got, "✗ fetch failed") {
		t.Fatalf("state column = %q", got)
	}
	if !m.messageErr {
		t.Fatal("failed batch must be reported as an error")
	}
}

func TestStaleInspectionIsIgnored(t *testing.T) {
	m := newModel(t)
	id := m.repos[0].ID
	m.repo(id).Inspected.Generation = 5
	m.Update(inspectionEvent(m, id, app.InspectionPayload{Generation: 4, Inspection: domain.Inspection{
		Worktree: domain.KnownValue(domain.WorktreeState{Conflicts: 9})}}))
	if m.repo(id).Worktree.Value.Conflicts == 9 {
		t.Fatal("an older generation overwrote newer state")
	}
}

// countText joins the count cells of r with "|".
func countText(m *Model, r *domain.Repository) string {
	var out []string
	for _, c := range m.countSegs(r) {
		out = append(out, plain(c))
	}
	return strings.Join(out, "|")
}

func TestStatusTexts(t *testing.T) {
	m := newModel(t)
	r := repo("x", "", false)
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Operation = domain.KnownValue(domain.Merging)
	r.Worktree = domain.KnownValue(domain.WorktreeState{Conflicts: 1, Staged: 2, Modified: 3, Deleted: 1, Untracked: 4, UntrackedChecked: true})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true, Ahead: 1, Behind: 2})
	r.ExpectedRemote = &domain.ExpectedRemote{URL: "git@a:b.git"}
	r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "git@a:c.git"}})
	r.Branches = domain.KnownValue([]domain.BranchState{{Name: "wip"}})
	r.Stashes = domain.KnownValue(3)
	if got := m.stateText(&r); got != "merging  ✘1 conflicts  ≠remote  ⎇1" {
		t.Errorf("state = %q; divergence is shown by the red counts and the health symbol", got)
	}
	if got := countText(m, &r); got != "2|4|4|3|1|2" {
		t.Errorf("counts = %q; modified includes deletions", got)
	}
	if got := m.upstreamText(&r); got != "↑1 ↓2 diverged" {
		t.Errorf("upstream = %q", got)
	}

	r.Operation = domain.KnownValue(domain.NoOperation)
	r.ExpectedRemote, r.Branches = nil, domain.Known[[]domain.BranchState]{}
	r.Worktree = domain.KnownValue(domain.WorktreeState{})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	r.Stashes = domain.KnownValue(0)
	if got := m.stateText(&r) + "#" + countText(m, &r); got != "#–|–|off|–|–|–" {
		t.Errorf("a healthy repository has no state, known zeros are dashes, and unchecked untracked files are off: %q", got)
	}
	r.Upstream = domain.KnownValue(domain.UpstreamState{})
	if got := m.stateText(&r) + "#" + countText(m, &r); got != "∅ no upstream#–|–|off|–||" {
		t.Errorf("without an upstream, ahead and behind do not apply: %q", got)
	}
	r.Worktree = domain.Known[domain.WorktreeState]{}
	r.Upstream = domain.Known[domain.UpstreamState]{}
	r.Stashes = domain.Known[int]{}
	if got := m.stateText(&r) + "#" + countText(m, &r); got != "? unknown#?|?|?|?|?|?" {
		t.Errorf("unknown state must not be shown as zero: %q", got)
	}
	r.Availability = domain.Missing
	if got := m.stateText(&r) + "#" + countText(m, &r); got != "✗ missing#|||||" {
		t.Errorf("missing = %q", got)
	}

	m.icons = iconSet("ascii")
	r.Availability = domain.Available
	r.Worktree = domain.KnownValue(domain.WorktreeState{Modified: 1, UntrackedChecked: true})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true, Gone: true})
	r.Stashes = domain.KnownValue(1)
	if got := m.stateText(&r) + "#" + countText(m, &r); got != "- upstream gone#–|1|–|1||" {
		t.Errorf("ascii = %q", got)
	}
}

func TestStashHints(t *testing.T) {
	m := newModel(t)
	for i := range m.repos {
		r := &m.repos[i]
		r.Availability = domain.Available
		r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
		r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
		r.Stashes = domain.KnownValue(0)
	}
	api := m.repo("/r/api")
	api.Stashes = domain.KnownValue(2)
	keys(m, "7")
	if m.filter.Text != "is:stashed" || names(m) != "api" {
		t.Fatalf("7 must show the repositories with stash entries: %q", names(m))
	}
	detail := func() string { return strings.Join(m.detailLines(api, 60).fit(100), "\n") }
	if v := detail(); !strings.Contains(v, "stash     ≡2 entries") || strings.Contains(v, "shared") {
		t.Fatalf("detail:\n%s", v)
	}
	m.repo("/r/frontend").StoreID = api.StoreID
	if v := detail(); !strings.Contains(v, "stash     ≡2 entries  shared by all worktrees") {
		t.Fatalf("a store with linked worktrees must say that they share the stash:\n%s", v)
	}
	api.Stashes = domain.Known[int]{}
	if v := detail(); strings.Contains(v, "stash") {
		t.Fatalf("an unknown count must not be reported as a number of stash entries:\n%s", v)
	}
	if got := countText(m, api); got != "–|1|–|?|–|–" {
		t.Fatalf("the table must show an unknown count as ?: %q", got)
	}
}

func TestHealthPrefersTheMostImportantState(t *testing.T) {
	m := newModel(t)
	r := repo("x", "", false)
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Operation = domain.KnownValue(domain.NoOperation)
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	check := func(want string, wantHealthy bool) {
		t.Helper()
		if s, healthy := m.health(&r); s.text != want || healthy != wantHealthy {
			t.Errorf("health = %q %v, want %q %v", s.text, healthy, want, wantHealthy)
		}
	}
	check("✓", true)
	r.Stashes = domain.KnownValue(1)
	check("!", false)
	if s, _ := m.health(&r); s.style.GetForeground() != m.theme.warn.GetForeground() {
		t.Error("stash entries are unpublished work and must be highlighted as a warning")
	}
	r.Stashes = domain.KnownValue(0)
	r.Branches = domain.KnownValue([]domain.BranchState{{Name: "wip"}})
	check("!", false)
	r.Upstream.Value.Ahead = 1
	check("↑", false)
	r.Upstream.Value.Behind = 2
	check("✘", false)
	r.Upstream.Value.Ahead = 0
	check("↓", false)
	r.Worktree.Value.Untracked = 1
	check("●", false)
	r.Availability = domain.Missing
	check("✗", false)
}

func TestSelectedRowKeepsSemanticColorsReadable(t *testing.T) {
	m := newModel(t)
	th := m.theme
	if fg := th.onSelected(th.dim).GetForeground(); fg != th.selectedFg {
		t.Errorf("secondary text must use the selected foreground, got %v", fg)
	}
	if fg := th.onSelected(th.bold).GetForeground(); fg != th.selectedFg {
		t.Errorf("unstyled text must use the selected foreground, got %v", fg)
	}
	if fg := th.onSelected(th.err).GetForeground(); fg != th.err.GetForeground() {
		t.Errorf("semantic colors must be kept, got %v", fg)
	}
}

func TestFooterHintsFollowContext(t *testing.T) {
	m := newModel(t)
	hints := func() string { return strings.Join(m.normalHints(), " ") }
	if h := hints(); !strings.Contains(h, "Enter lazygit") || !strings.Contains(h, "F fetch shown") || strings.Contains(h, "clear") {
		t.Errorf("default hints = %q", h)
	}
	keys(m, " ", "/", "api", "enter")
	if h := hints(); !strings.Contains(h, "f fetch marked") || !strings.Contains(h, "A clear marks") ||
		!strings.Contains(h, "Esc clear filter") || strings.Contains(h, "fetch shown") {
		t.Errorf("hints with marks and filter = %q", h)
	}
	keys(m, "esc", "A", "v")
	if h := hints(); !strings.HasPrefix(h, "Enter expand") {
		t.Errorf("hints on a family row = %q", h)
	}

	fetchBatch(t, m, "a", "b")
	setRow(m, 0, domain.StatusRunning, nil)
	setRow(m, 1, domain.StatusFailed, nil)
	ops := func() string { return strings.Join(m.operationHints(), " ") }
	if h := ops(); !strings.Contains(h, "c cancel") || strings.Contains(h, "retry") {
		t.Errorf("running batch hints = %q", h)
	}
	setRow(m, 0, domain.StatusSucceeded, nil)
	m.opSelected = 1
	if h := ops(); strings.Contains(h, "cancel") || !strings.Contains(h, "x retry failed") || !strings.Contains(h, "t retry in terminal") {
		t.Errorf("finished batch hints = %q", h)
	}
}

func TestFamilySummaryCountsHealthLevels(t *testing.T) {
	m := newModel(t)
	got := plain(m.familySummary([]healthLevel{healthOK, healthDirty, healthError, healthDirty, healthUnknown}))
	if got != "✗1 error  ●2 dirty" {
		t.Errorf("summary = %q", got)
	}
	if got := plain(m.familySummary([]healthLevel{healthOK, healthOK})); got != "✓" {
		t.Errorf("a healthy family must show only the symbol: %q", got)
	}
	if got := plain(m.familySummary([]healthLevel{healthOK, healthUnknown})); got != "" {
		t.Errorf("a family that is still inspected must not be called healthy: %q", got)
	}
}

// viewLine returns the first line of the plain view that contains s.
func viewLine(m *Model, s string) string {
	for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

// colOf returns the display column at which s starts in l, or -1.
func colOf(l, s string) int {
	i := strings.Index(l, s)
	if i < 0 {
		return -1
	}
	return runewidth.StringWidth(l[:i])
}

func TestFamilyRowsStandOutFromRepositories(t *testing.T) {
	m := newModel(t)
	keys(m, "v", "g", "enter")
	family, repo := viewLine(m, "Personal (1)"), viewLine(m, "dotfiles")
	if c := colOf(family, "▾"); c != 3 {
		t.Errorf("the toggle must take the mark column, after border and cursor, got %d:\n%q", c, family)
	}
	if fc, rc := colOf(family, "Personal"), colOf(repo, "dotfiles"); rc-fc < 5 {
		t.Errorf("repository names must be indented clearly below the family name: %d vs %d", fc, rc)
	}
	tbl := m.measureTable()
	for i, row := range m.rows {
		if row.count > 0 {
			name := tbl.family[i][1]
			if name.text != "Personal" || name.style.GetForeground() != m.theme.title.GetForeground() {
				t.Errorf("the family name must use the title color: %+v", name)
			}
			break
		}
	}
}

func TestFamilySummaryIsAlignedAndOnlyForCollapsedFamilies(t *testing.T) {
	m := newModel(t)
	keys(m, "v")
	header, row := viewLine(m, "State"), viewLine(m, "Work (2)")
	if hc, sc := colOf(header, "State"), colOf(row, "✗2 error"); hc < 0 || sc != hc {
		t.Errorf("the summary must start in the State column (%d), got %d:\n%q\n%q", hc, sc, header, row)
	}
	keys(m, "G", "enter")
	if row := viewLine(m, "Work (2)"); strings.Contains(row, "error") {
		t.Errorf("an expanded family shows the health in its rows, not in a summary: %q", row)
	}
	if row := viewLine(m, "Personal (1)"); !strings.Contains(row, "✗1 error") {
		t.Errorf("a collapsed family must keep its summary: %q", row)
	}
}

func TestOnlyErrorsAndCriticalStatesEmphasizeNames(t *testing.T) {
	m := newModel(t)
	r := repo("x", "", true)
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Operation = domain.KnownValue(domain.NoOperation)
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	bold := func() bool { return m.rowCells(&r, time.Now())[1][0].style.GetBold() }
	if bold() {
		t.Error("a dirty repository must not have a bold name")
	}
	r.Worktree.Value.Conflicts = 1
	if !bold() {
		t.Error("a critical repository must have a bold name")
	}
	r.Availability = domain.Missing
	if !bold() {
		t.Error("an unavailable repository must have a bold name")
	}
}

func TestFamilyFilterHidesFamilyColumn(t *testing.T) {
	m := newModel(t)
	if !strings.Contains(viewLine(m, "Repository"), "Family") {
		t.Fatal("the flat list must show the family column")
	}
	keys(m, "tab")
	if h := viewLine(m, "Repository"); m.filter.Family == "" || strings.Contains(h, "Family") {
		t.Errorf("with a family filter, every row has the same family: %q", h)
	}
}

func TestStaleFetchNeedsAttention(t *testing.T) {
	m := newModel(t)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	r := repo("x", "", false)
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Operation = domain.KnownValue(domain.NoOperation)
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	r.Stashes = domain.KnownValue(0)
	r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "git@a:b.git"}})
	at := func(d time.Duration) domain.Known[*time.Time] {
		v := now.Add(-d)
		return domain.KnownValue(&v)
	}
	for _, c := range []struct {
		name    string
		fetch   domain.Known[*time.Time]
		remotes bool
		want    string
	}{
		{"recent", at(6 * 24 * time.Hour), true, ""},
		{"stale", at(8 * 24 * time.Hour), true, "stale fetch"},
		{"never", domain.KnownValue[*time.Time](nil), true, "never fetched"},
		{"unknown", domain.Known[*time.Time]{}, true, ""},
		{"no remote", domain.KnownValue[*time.Time](nil), false, ""},
	} {
		r.LastFetch = c.fetch
		r.Remotes = domain.KnownValue([]domain.Remote{})
		if c.remotes {
			r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "git@a:b.git"}})
		}
		s, healthy := m.health(&r)
		if got := m.stateText(&r); got != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got, c.want)
		}
		if c.want == "" && !healthy {
			t.Errorf("%s: the repository must stay healthy, got %q", c.name, s.text)
		}
		if c.want != "" && (s.text != "!" || s.style.GetForeground() != m.theme.warn.GetForeground()) {
			t.Errorf("%s: a stale fetch needs attention as a warning, got %q", c.name, s.text)
		}
	}
}

func TestOperationsAreGroupedByStatus(t *testing.T) {
	m := newModel(t)
	fetchBatch(t, m, "/r/api", "/r/frontend", "/r/dotfiles", "x")
	setRow(m, 0, domain.StatusSucceeded, nil)
	setRow(m, 2, domain.StatusFailed, nil)
	setRow(m, 3, domain.StatusRunning, nil)
	if got := fmt.Sprint(m.opOrder()); got != "[3 2 1 0]" {
		t.Fatalf("order = %s, want running, failed, queued, completed", got)
	}
	keys(m, "b")
	if m.opSelected != 3 {
		t.Fatalf("the first shown row must be selected, got %d", m.opSelected)
	}
	keys(m, "j", "j")
	if m.opSelected != 1 {
		t.Fatalf("j must follow the display order, got %d", m.opSelected)
	}
	setRow(m, 1, domain.StatusRunning, nil)
	keys(m, "j")
	if m.opSelected != 3 {
		t.Fatalf("the selection must stay on its row when it changes group, got %d", m.opSelected)
	}
	v := m.View()
	if !strings.Contains(v, "RUNNING 2") || !strings.Contains(v, "COMPLETED 1") || strings.Index(v, "FAILED") > strings.Index(v, "COMPLETED") {
		t.Fatal("groups must be titled and ordered")
	}
}

func TestDetailPanelGroupsStatusAndRepository(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	m := newModel(t)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	fetched := now.Add(-2 * time.Hour)
	r := repo("svc", "Work", false)
	r.Path = "/home/u/src/svc"
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true, Ref: "origin/main"})
	r.LastFetch = domain.KnownValue(&fetched)
	r.LastCommit = domain.KnownValue(&domain.CommitSummary{ShortID: "c28633b", Author: "Ada", Time: now.Add(-15 * time.Hour), Subject: "Ship it"})
	r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "git@a:b.git"}})
	r.ExpectedRemote = &domain.ExpectedRemote{URL: "git@a:c.git"}
	r.Branches = domain.KnownValue([]domain.BranchState{{Name: "wip", CommitTime: now}})
	lines := m.detailLines(&r, 60).fit(100)
	idx := func(prefix string) int {
		for i, l := range lines {
			if strings.HasPrefix(ansi.Strip(l), prefix) {
				return i
			}
		}
		return -1
	}
	v := ansi.Strip(strings.Join(lines, "\n"))
	status, commits, branches, repository := idx("── STATUS"), idx("── RECENT COMMITS"), idx("── OTHER BRANCHES"), idx("── REPOSITORY")
	if status != 0 || strings.Contains(v, "── BRANCH") || strings.Contains(v, "── WORKTREE") {
		t.Fatalf("branch and worktree must form the first section, STATUS:\n%s", v)
	}
	for _, want := range []string{"branch    main → origin/main  ✓", "fetched   2 h ago", "worktree  ✓ clean", "remote    ≠ expected origin: git@a:c.git"} {
		if i := idx(want); i <= status || i >= commits {
			t.Errorf("STATUS must contain the line %q:\n%s", want, v)
		}
	}
	if !(status < commits && commits < branches && branches < repository) {
		t.Fatalf("the sections must be STATUS, RECENT COMMITS, OTHER BRANCHES, REPOSITORY:\n%s", v)
	}
	if i := idx("c28633b    15 h  Ada           Ship it"); i <= commits {
		t.Errorf("a recent commit must show its age and author like an incoming commit:\n%s", v)
	}
	if strings.Contains(v, "── REMOTES") || strings.Contains(v, "── LINKED WORKTREES") {
		t.Fatalf("remotes and linked worktrees belong to REPOSITORY:\n%s", v)
	}
	for _, want := range []string{"path      ~/src/svc", "family    Work", "source    configured", "origin    git@a:b.git"} {
		if i := idx(want); i <= repository {
			t.Errorf("REPOSITORY must contain the line %q:\n%s", want, v)
		}
	}

	m.repos[0].StoreID, m.repos[0].Path = r.StoreID, "/home/u/src/svc-wt"
	lines = m.detailLines(&r, 60).fit(100)
	if i := idx("linked    ~/src/svc-wt"); i <= idx("── REPOSITORY") {
		t.Errorf("linked worktrees belong to REPOSITORY and are shortened to ~:\n%s", ansi.Strip(strings.Join(lines, "\n")))
	}

	box := m.detailsBox(60, 30)
	if bottom := ansi.Strip(box[len(box)-1]); strings.Contains(bottom, "configured") {
		t.Errorf("the source belongs to the REPOSITORY section, not the border: %q", bottom)
	}
}

func TestSourceLabel(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for _, c := range []struct {
		r    domain.Repository
		want string
	}{
		{domain.Repository{Source: domain.InventorySourceExplicit, DiscoveryRoot: "/home/u/src"}, "configured"},
		{domain.Repository{Source: domain.InventorySourceDiscovered, DiscoveryRoot: "/home/u/src"}, "discovered in ~/src"},
		{domain.Repository{Source: domain.InventorySourceDiscovered, DiscoveryRoot: "/home/user2"}, "discovered in /home/user2"},
	} {
		if got := sourceLabel(&c.r); got != c.want {
			t.Errorf("sourceLabel = %q, want %q", got, c.want)
		}
	}
}

func TestQuickFiltersToggle(t *testing.T) {
	m := newModel(t)
	keys(m, "1")
	if m.filter.Text != "is:dirty" || names(m) != "api" {
		t.Fatalf("quick filter: %q %q", m.filter.Text, names(m))
	}
	if !strings.Contains(m.headerLine(), "dirty 1") {
		t.Fatalf("header must count dirty repositories: %q", m.headerLine())
	}
	keys(m, "1")
	if m.filter.Text != "" {
		t.Fatal("pressing the key again must clear the quick filter")
	}
}

func TestToastExpires(t *testing.T) {
	m := newModel(t)
	m.setMessage("hello")
	m.Update(tickMsg{})
	seq := m.msgSeq
	m.Update(toastExpiredMsg{seq: seq - 1})
	if m.message == "" {
		t.Fatal("an outdated expiry must not hide a newer message")
	}
	m.Update(toastExpiredMsg{seq: seq})
	if m.message != "" {
		t.Fatal("message must expire")
	}
}

func TestUpdateWorklistAfterLazygit(t *testing.T) {
	m := newModel(t)
	now := time.Now().Add(time.Hour)
	m.now = func() time.Time { return now }
	for _, id := range []domain.RepositoryID{"/r/api", "/r/frontend"} {
		r := m.repo(id)
		r.Availability = domain.Available
		r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
		r.Operation = domain.KnownValue(domain.NoOperation)
		r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	}
	api := *m.repo("/r/api")

	offline(m)
	m.preview = &updatePreview{targets: []domain.RepositoryID{"/r/api", "/r/frontend"}}
	m.startUpdate()
	setRow(m, 0, domain.StatusSucceeded, nil)
	setRow(m, 1, domain.StatusSucceeded, nil)
	m.advanceUpdate()
	b := m.ops.Batch()
	if m.mode != modeOperations || b.Op != domain.OperationUpdate || len(b.Rows) != 1 || b.Rows[0].Job.RepositoryID != api.ID {
		t.Fatalf("blocked repositories must form the worklist, up-to-date ones not: mode=%v batch=%+v", m.mode, b)
	}
	if fu, _ := m.followUp(b.Rows[0]); fu != "" {
		t.Fatalf("no follow-up before a new inspection: %q", fu)
	}

	// Return from lazygit opened on the row, then a new inspection arrives.
	m.ops.BeginHandover(api.StoreID)
	m.visit = &visit{batch: b, row: 0}
	m.Update(handoverDoneMsg{label: "lazygit", repo: api.ID, store: api.StoreID})
	if m.mode != modeOperations || b.Rows[0].RevisitedAt.IsZero() {
		t.Fatal("the operations view must stay open and remember the visit")
	}
	inspect := func(w domain.WorktreeState) {
		now = now.Add(time.Minute)
		r := m.repo(api.ID)
		m.Update(inspectionEvent(m, api.ID, app.InspectionPayload{Generation: r.Inspected.Generation + 1, Inspection: domain.Inspection{
			Head:      domain.KnownValue(domain.HeadState{Branch: "main"}),
			Operation: domain.KnownValue(domain.NoOperation),
			Worktree:  domain.KnownValue(w),
			Upstream:  domain.KnownValue(domain.UpstreamState{Configured: true, CommitID: "up1", Behind: 2}),
		}}))
	}
	m.repo(api.ID).Availability = domain.Available
	inspect(domain.WorktreeState{Modified: 1})
	if fu, ok := m.followUp(b.Rows[0]); fu != "still blocked: worktree contains local changes" || ok {
		t.Fatalf("follow-up = %q", fu)
	}
	inspect(domain.WorktreeState{})
	if fu, ok := m.followUp(b.Rows[0]); fu != "now eligible" || !ok {
		t.Fatalf("follow-up = %q", fu)
	}
	if !strings.Contains(m.View(), "→ now eligible") {
		t.Fatal("operations view must show the follow-up")
	}

	keys(m, "u")
	if m.mode != modeUpdatePreview || len(m.preview.ok) != 1 || m.preview.ok[0].Repository.ID != api.ID {
		t.Fatalf("continue must open a new preview for open rows: mode=%v ok=%d", m.mode, len(m.preview.ok))
	}
	keys(m, "esc")
	if m.mode != modeOperations {
		t.Fatal("cancelling the continued preview must return to the operations view")
	}
}

func TestViewRendersAllModes(t *testing.T) {
	m := newModel(t)
	for _, width := range []int{80, 120, 160} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		for _, k := range []string{"", "?", "e", "s", "u", "d", "o", "y"} {
			if k != "" {
				keys(m, k)
			}
			for i, l := range strings.Split(m.View(), "\n") {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("width %d, mode %q: line %d is %d cells wide", width, k, i, w)
				}
			}
			if v := m.View(); strings.Count(v, "\n") != m.height-1 {
				t.Errorf("width %d, mode %q renders %d lines, want %d", width, k, strings.Count(v, "\n")+1, m.height)
			}
			if k == "" {
				continue
			}
			keys(m, "esc")
			if m.mode != modeNormal {
				t.Errorf("Esc must close mode after %q", k)
			}
		}
	}
}

func TestOtherBranchesMarkerAndDetails(t *testing.T) {
	m := newModel(t)
	r := repo("svc", "Work", false)
	r.Availability = domain.Available
	var bs []domain.BranchState
	bs = append(bs, domain.BranchState{Name: "main", Upstream: "origin/main", Behind: 2, CommitTime: time.Now()})
	if got := plain(m.branchesMarker(&domain.Repository{Availability: domain.Available,
		Inspection: domain.Inspection{Branches: domain.KnownValue(bs)}})); got != "⎇1" {
		t.Fatalf("marker = %q", got)
	}
	for i := 0; i < 11; i++ {
		bs = append(bs, domain.BranchState{Name: fmt.Sprintf("wip-%d", i), CommitTime: time.Now()})
	}
	r.Branches = domain.KnownValue(bs)
	if r.UnpublishedBranches() != 11 {
		t.Fatalf("unpublished = %d", r.UnpublishedBranches())
	}
	lines := m.otherBranchLines(&r)
	if len(lines) != 12 || !strings.Contains(lines[0], "↓2") {
		t.Fatalf("lines = %q", lines)
	}
	if v := strings.Join(m.detailLines(&r, 60).fit(100), "\n"); !strings.Contains(v, "wip-8") ||
		strings.Contains(v, "wip-9") || !strings.Contains(v, "+2 more") {
		t.Fatalf("the detail panel lists at most %d other branches:\n%s", maxBranchLines, v)
	}
}

// TestSmallDetailPanelShortensOtherBranchesFirst checks that many other
// branches do not push the worktree state or an error out of the panel.
func TestDetailSectionsAndCommitLine(t *testing.T) {
	m := newModel(t)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	r := repo("svc", "Work", false)
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true, Ref: "origin/main"})
	r.LastCommit = domain.KnownValue(&domain.CommitSummary{ShortID: "c28633b", Author: "Sam",
		Time: now.Add(-5 * time.Hour), Subject: "Show delivery estimate"})
	lines := m.detailLines(&r, 40).fit(100)
	v := strings.Join(lines, "\n")
	if !strings.Contains(v, "c28633b     5 h  Sam           Show delivery estimate") {
		t.Fatalf("the commit must be one line of hash, age, author, and subject:\n%s", v)
	}
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l, "── STATUS ─") {
			found = true
			if w := lipgloss.Width(l); w != 40 {
				t.Errorf("a section rule must span the panel: width %d, want 40", w)
			}
		}
	}
	if !found {
		t.Fatalf("sections must be titled within a rule:\n%s", v)
	}

	for _, c := range []struct {
		d   time.Duration
		age string
	}{
		{10 * time.Second, "now"},
		{45 * time.Minute, "45 min"},
		{15 * time.Hour, "15 h"},
		{21 * 24 * time.Hour, "21 d"},
		{100 * 24 * time.Hour, "3 mo"},
		{800 * 24 * time.Hour, "2 y"},
	} {
		if got := commitAge(now, now.Add(-c.d)); got != c.age {
			t.Errorf("commitAge(-%v) = %q, want %q", c.d, got, c.age)
		}
	}

	// The table shows the age of the latest commit.
	m.repos[0].LastCommit = domain.KnownValue(&domain.CommitSummary{Time: now.Add(-21 * 24 * time.Hour)})
	if v := ansi.Strip(strings.Join(m.reposBox(m.measureTable(), 160, 10, true), "\n")); !strings.Contains(v, "21 d") || strings.Contains(v, "Nov  1") {
		t.Errorf("the table must show the commit age:\n%s", v)
	}
}

func TestSmallDetailPanelShortensOtherBranchesFirst(t *testing.T) {
	m := newModel(t)
	r := &m.repos[m.index[m.current().ID]]
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	r.Operation = domain.KnownValue(domain.Merging)
	var bs []domain.BranchState
	for i := 0; i < 10; i++ {
		bs = append(bs, domain.BranchState{Name: fmt.Sprintf("wip-%d", i), CommitTime: time.Now()})
	}
	r.Branches = domain.KnownValue(bs)
	fetchBatch(t, m, r.ID)
	setRow(m, 0, domain.StatusFailed, errors.New("offline"))
	v := strings.Join(m.detailsBox(120, 14), "\n")
	for _, want := range []string{"merging in progress", "offline", "wip-0", "more"} {
		if !strings.Contains(v, want) {
			t.Errorf("a small detail panel must show %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "wip-9") {
		t.Errorf("the other branches must be shortened:\n%s", v)
	}
}

// TestUpdateAfterFailedFetchSkipsStore checks that a failed fetch is the
// skip reason even if the old state looks up to date, so the repository
// stays on the worklist, and that the update starts without opening a view.
func TestUpdateAfterFailedFetchSkipsStore(t *testing.T) {
	m := newModel(t)
	r := &m.repos[0]
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Operation = domain.KnownValue(domain.NoOperation)
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	r.Worktree.Value.Modified = 0
	offline(m)
	m.preview = &updatePreview{targets: []domain.RepositoryID{r.ID}}
	m.startUpdate()
	m.mode = modeNormal // the user closed the operations view during the fetch
	setRow(m, 0, domain.StatusFailed, errors.New("remote unavailable"))
	m.advanceUpdate()
	if m.mode != modeNormal || m.ops.UpdatePending() {
		t.Fatalf("the update must not open a view on its own: mode=%v", m.mode)
	}
	if b := m.ops.Batch(); b.Op != domain.OperationUpdate || len(b.Rows) != 1 ||
		b.Rows[0].Result.Error.Error() != "fetch failed: remote unavailable" {
		t.Fatalf("rows = %+v", b.Rows)
	}
}

// TestCancelDuringFetchCancelsUpdate checks that c drops the confirmed update
// even if the remaining fetches still succeed.
func TestCancelDuringFetchCancelsUpdate(t *testing.T) {
	m := newModel(t)
	r := &m.repos[0]
	r.Availability = domain.Available
	offline(m)
	m.preview = &updatePreview{targets: []domain.RepositoryID{r.ID}}
	m.startUpdate()
	if m.mode != modeOperations || !m.ops.FetchForUpdate() {
		t.Fatalf("the confirmed update must fetch first: mode=%v", m.mode)
	}
	keys(m, "R")
	if m.loadingInventory {
		t.Fatal("R must not drop an update that waits for its fetch")
	}
	keys(m, "c")
	setRow(m, 0, domain.StatusSucceeded, nil)
	m.advanceUpdate()
	if m.ops.UpdatePending() || m.ops.Batch().Op != domain.OperationFetch || m.message != "update cancelled" {
		t.Fatalf("the update must not start after c: op=%s message=%q", m.ops.Batch().Op, m.message)
	}
}

func TestMonochromeSelectionKeepsDefaultColorsReadable(t *testing.T) {
	th := newTheme(config.ThemePresets["monochrome"])
	for _, s := range []lipgloss.Style{th.err, th.key, th.branch, th.dim} {
		if fg := th.onSelected(s).GetForeground(); fg != th.selectedFg {
			t.Errorf("the default foreground is not readable on the selected background, got %v", fg)
		}
	}
}

func TestLightBackgroundUsesLightThemeVariant(t *testing.T) {
	cfg, err := config.Parse([]byte("version = 1\n[ui.theme]\npreset = \"gruvbox\"\n"), "/x", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	a := app.New("/none", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.SetDarkBackground(false)
	light := lipgloss.Color(config.ResolveColor(config.ThemePresets["gruvbox-light"].Error))
	if fg := m.theme.err.GetForeground(); fg != light {
		t.Fatalf("a light background must use the light variant, got %v", fg)
	}
	m.Update(inventoryMsg{loaded: &app.Loaded{Config: cfg, Git: a.Git}})
	if fg := m.theme.err.GetForeground(); fg != light {
		t.Fatalf("a reloaded configuration must keep the detected background, got %v", fg)
	}
}

func TestOperationsKeepGroupTitleWhenScrolled(t *testing.T) {
	m := newModel(t)
	var ids []domain.RepositoryID
	for i := 0; i < 40; i++ {
		ids = append(ids, domain.RepositoryID(fmt.Sprint(i)))
	}
	fetchBatch(t, m, ids...)
	for i := range ids {
		setRow(m, i, domain.StatusCancelled, nil)
	}
	keys(m, "b")
	for i := 0; i < 30; i++ {
		keys(m, "j")
	}
	if v := m.View(); !strings.Contains(v, "CANCELLED 40") || !strings.Contains(v, "– 30 ") {
		t.Fatalf("the title of the shown group must stay visible:\n%s", v)
	}
}

func TestColumnsFollowContent(t *testing.T) {
	// The longest name, "Engine Portal" and a long branch fit without
	// truncation; the remaining space is not given to the name column. Count
	// columns are as wide as their full titles.
	ic := iconSet("unicode")
	natural := []int{0, 1, 24, 13, 26, 7, 1, 1, 1, 1, 2, 1, 6}
	cols := columns(166, natural, ic, false)
	for i, want := range []int{3, 1, 24, 13, 26, 16, 6, 8, 9, 7, 5, 6, 6} {
		if cols[i].width != want {
			t.Errorf("%s: width %d, want %d", cols[i].title, cols[i].width, want)
		}
	}
	if !cols[colStaged].right || cols[colName].right {
		t.Error("only count columns are right-aligned")
	}
	// If space is short, count columns switch to their symbols, the table
	// still fits, and the state shrinks last.
	for _, iw := range []int{64, 80, 100} {
		cols := columns(iw, natural, ic, false)
		if w := tableWidth(cols); w > iw {
			t.Errorf("iw %d: table width %d", iw, w)
		}
		if cols[colStaged].title != "✚" || cols[colBehind].title != "↓" || cols[colAhead].width != 2 {
			t.Errorf("iw %d: count columns must use their symbols: %+v", iw, cols)
		}
		if cols[colState].width < 6 {
			t.Errorf("iw %d: state shrinks last: %+v", iw, cols)
		}
	}
	// The tree names the family in its family rows, so the column is hidden
	// and its space goes to the full count titles.
	cols = columns(120, natural, ic, true)
	if !cols[colFamily].hidden || cols[colStaged].title != "Staged" || tableWidth(cols) > 120 {
		t.Errorf("tree: %+v", cols)
	}
}

// TestNamesStartInOneColumn checks that health symbols of different widths
// do not shift the names.
func TestNamesStartInOneColumn(t *testing.T) {
	m := newModel(t)
	m.icons = iconSet("ascii")
	r := &m.repos[m.index["/r/frontend"]]
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	r.Operation = domain.KnownValue(domain.NoOperation)
	if s, healthy := m.health(r); !healthy || s.text != "ok" {
		t.Fatalf("frontend must be healthy: %q", s.text)
	}
	col := map[string]int{}
	for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		for _, name := range []string{"frontend", "dotfiles"} {
			if _, seen := col[name]; seen || !strings.HasPrefix(l, "│") {
				continue
			}
			if i := strings.Index(l, " "+name+" "); i >= 0 {
				col[name] = runewidth.StringWidth(l[:i])
			}
		}
	}
	if len(col) != 2 || col["frontend"] != col["dotfiles"] {
		t.Fatalf("names must start in one column: %v", col)
	}
}

// TestBranchSymbolOnlyInDetails checks that the titled Branch column shows
// the name alone, while the detail panel keeps the symbol.
func TestBranchSymbolOnlyInDetails(t *testing.T) {
	m := newModel(t)
	m.icons = iconSet("nerd")
	r := repo("svc", "Work", false)
	r.Availability = domain.Available
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main"})
	r.Upstream = domain.KnownValue(domain.UpstreamState{Configured: true})
	if got := plain(m.rowCells(&r, time.Now())[colBranch-colHealth]); got != "main" {
		t.Errorf("branch cell = %q, want the name alone", got)
	}
	if v := strings.Join(m.detailLines(&r, 60).fit(100), "\n"); !strings.Contains(v, m.icons.branch+"main") {
		t.Errorf("the detail panel must keep the branch symbol:\n%s", v)
	}
}

// TestDetailPanelMovesToTheTable checks that on a wide terminal the
// repository panel is only as wide as its table and the detail panel takes
// all space left over, however wide the terminal is.
func TestDetailPanelMovesToTheTable(t *testing.T) {
	m := newModel(t)
	for _, width := range []int{220, 320} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		need := m.tableNeed(m.measureTable())
		if width-need <= maxDetailWidth {
			t.Fatalf("width %d: the test table must leave more than %d columns: need %d", width, maxDetailWidth, need)
		}
		found := false
		for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
			if i := strings.Index(l, "╮╭"); i >= 0 {
				found = true
				if got := runewidth.StringWidth(l[:i]) + 1; got != need || runewidth.StringWidth(l) != width {
					t.Errorf("width %d: the repository panel is %d wide, want %d:\n%s", width, got, need, l)
				}
			}
		}
		if !found {
			t.Fatalf("width %d: the panels must be side by side", width)
		}
	}
}

func TestConfiguredViewAndTreeMarkers(t *testing.T) {
	cfg := config.Default()
	cfg.UI.View, cfg.UI.Icons = "tree", "ascii"
	a := app.New("/none", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(inventoryMsg{result: inventory.Result{Repositories: []domain.Repository{repo("api", "Work", false)}}})
	if !m.tree || !strings.Contains(m.View(), "[+] Work (1)") {
		t.Fatal("view = \"tree\" must start in the tree with ASCII markers")
	}
	keys(m, "enter")
	if v := m.View(); !strings.Contains(v, "[-] Work (1)") || !strings.Contains(v, "`- x api") {
		t.Fatal("expanded family must use the ASCII markers")
	}

	// A reload keeps the view the user switched to unless the configured
	// default changes.
	keys(m, "v")
	reload := func(view string) {
		c := config.Default()
		c.UI.View = view
		m.Update(inventoryMsg{loaded: &app.Loaded{Config: c, Git: a.Git},
			result: inventory.Result{Repositories: []domain.Repository{repo("api", "Work", false)}}})
	}
	reload("tree")
	if m.tree {
		t.Fatal("an unchanged view setting must not override the user's choice")
	}
	reload("list")
	if m.tree {
		t.Fatal("view = \"list\" must show the list")
	}
	reload("tree")
	if !m.tree || !strings.Contains(m.View(), "▾ Work (1)") {
		t.Fatal("a changed view setting must take effect; the family stays expanded")
	}
}

func TestStartupTimeIsReportedOnce(t *testing.T) {
	start := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	newStarted := func() *Model {
		a := app.New("/none", config.Default(), git.New("git"))
		t.Cleanup(a.Close)
		m := New(a)
		m.started = start
		m.now = func() time.Time { return start.Add(1500 * time.Millisecond) }
		return m
	}
	missing := repo("missing", "Work", false)
	missing.Availability, missing.Path = domain.Available, filepath.Join(t.TempDir(), "missing")
	inv := inventoryMsg{result: inventory.Result{Repositories: []domain.Repository{repo("api", "Work", false), missing}}}

	m := newStarted()
	m.Update(inv)
	if m.message != "" {
		t.Fatalf("the startup time must wait for the inspections, got %q", m.message)
	}
	pump(t, m)
	if m.message != "loaded 2 repositories in 1.5 s" || m.messageErr {
		t.Fatalf("the startup time must be reported after the inspections, got %q", m.message)
	}
	keys(m, "R")
	m.message = ""
	m.Update(inv)
	pump(t, m)
	if m.message != "" {
		t.Fatalf("the startup time must be reported only once, got %q after a reload", m.message)
	}

	m = newStarted()
	inv.result.Warnings = []string{"unknown key"}
	m.Update(inv)
	pump(t, m)
	if !m.messageErr || !strings.Contains(m.message, "configuration warning") {
		t.Fatalf("the startup time must not replace a configuration warning, got %q", m.message)
	}
}

func TestSortOverlayKeys(t *testing.T) {
	m := newModel(t)
	keys(m, "s", "h")
	if m.mode != modeNormal || m.sortKey != app.SortHealth {
		t.Fatalf("h must sort by health at once: mode %v, sort %s", m.mode, m.sortKey)
	}
	keys(m, "s", "n")
	if m.mode != modeNormal || m.sortKey != app.SortName {
		t.Fatalf("n must sort by name at once: mode %v, sort %s", m.mode, m.sortKey)
	}
	keys(m, "s", "k")
	if m.overlayIdx != len(app.SortKeys)-1 {
		t.Fatalf("k on the first entry must wrap to the last, got %d", m.overlayIdx)
	}
	keys(m, "j")
	if m.overlayIdx != 0 {
		t.Fatalf("j on the last entry must wrap to the first, got %d", m.overlayIdx)
	}
}

func TestConfiguredSort(t *testing.T) {
	repos := func() []domain.Repository {
		return []domain.Repository{repo("api", "Work", false), repo("dotfiles", "Personal", false)}
	}
	cfg := config.Default()
	cfg.UI.Sort = "family"
	a := app.New("/none", cfg, git.New("git"))
	t.Cleanup(a.Close)
	m := New(a)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(inventoryMsg{result: inventory.Result{Repositories: repos()}})
	if m.sortKey != app.SortFamily || m.visible[0] != "/r/dotfiles" {
		t.Fatalf("sort = \"family\" must sort by family at startup: %s %v", m.sortKey, m.visible)
	}

	// A reload keeps the sort the user chose unless the configured default
	// changes.
	m.sortKey = app.SortName
	reload := func(sort string) {
		c := config.Default()
		c.UI.Sort = sort
		m.Update(inventoryMsg{loaded: &app.Loaded{Config: c, Git: a.Git}, result: inventory.Result{Repositories: repos()}})
	}
	reload("family")
	if m.sortKey != app.SortName {
		t.Fatal("an unchanged sort setting must not override the user's choice")
	}
	reload("health")
	if m.sortKey != app.SortHealth {
		t.Fatalf("a changed sort setting must take effect, got %s", m.sortKey)
	}
}

// helpBox opens the help view in a terminal of the given width and returns
// its plain lines without the border.
func helpBox(t *testing.T, m *Model, width int) []string {
	t.Helper()
	m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	keys(m, "?")
	defer keys(m, "esc")
	box := m.modal(200)
	if len(box) < 3 {
		t.Fatalf("width %d: no help box", width)
	}
	var out []string
	for _, l := range box[1 : len(box)-1] {
		out = append(out, strings.Trim(ansi.Strip(l), "│"))
	}
	return out
}

// helpEntries lists every key binding and legend entry of the help view.
func helpEntries(m *Model) []Binding {
	var out []Binding
	for _, g := range Bindings {
		out = append(out, g.Bindings...)
	}
	return append(append(out, m.icons.healthLegend()...), m.icons.legend()...)
}

func TestHelpShowsEveryEntryOnOneLineInAWideTerminal(t *testing.T) {
	m := newModel(t)
	for _, set := range []string{"unicode", "nerd", "ascii"} {
		m.icons = iconSet(set)
		lines := helpBox(t, m, 120)
		for _, l := range lines {
			if strings.Contains(strings.ReplaceAll(l, "1 … 8", ""), "…") {
				t.Errorf("%s: truncated help line %q", set, l)
			}
		}
		for _, b := range helpEntries(m) {
			if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, b.Action) }) {
				t.Errorf("%s: no help line shows %q in full", set, b.Action)
			}
		}
	}
}

func TestHelpWrapsInsteadOfTruncatingInANarrowTerminal(t *testing.T) {
	m := newModel(t)
	for _, set := range []string{"unicode", "nerd", "ascii"} {
		m.icons = iconSet(set)
		lines := helpBox(t, m, 60)
		for _, l := range lines {
			if strings.Contains(strings.ReplaceAll(l, "1 … 8", ""), "…") {
				t.Errorf("%s: truncated help line %q", set, l)
			}
		}
		text := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
		for _, b := range helpEntries(m) {
			if !strings.Contains(text, strings.Join(strings.Fields(b.Action), " ")) {
				t.Errorf("%s: the help lost text of %q", set, b.Action)
			}
		}
	}
}

func TestHelpIsNoWiderThanItsContent(t *testing.T) {
	m := newModel(t)
	wide, wider := helpBox(t, m, 160), helpBox(t, m, 240)
	if w, ww := ansi.StringWidth(wide[0]), ansi.StringWidth(wider[0]); w != ww {
		t.Errorf("the help must not grow beyond its content: %d columns at width 160, %d at 240", w, ww)
	}
}

func TestHelpExplainsHealthColumn(t *testing.T) {
	m := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 80})
	for _, set := range []string{"unicode", "nerd", "ascii"} {
		m.icons = iconSet(set)
		keys(m, "?")
		v := ansi.Strip(m.View())
		if !strings.Contains(v, "Health column") {
			t.Fatalf("%s: the help must have a health legend:\n%s", set, v)
		}
		for _, b := range m.icons.healthLegend() {
			if !strings.Contains(v, b.Keys) || !strings.Contains(v, b.Action) {
				t.Errorf("%s: the help must explain %q (%s)", set, b.Keys, b.Action)
			}
		}
		keys(m, "esc")
	}
}

// TestBindingsDocumented keeps docs/keybindings.md in step with the help
// view: every key of a help group appears in the first column of the
// documentation section with the same title.
func TestBindingsDocumented(t *testing.T) {
	data, err := os.ReadFile("../../docs/keybindings.md")
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]map[string]bool{}
	var keys map[string]bool
	for _, line := range strings.Split(string(data), "\n") {
		if title, ok := strings.CutPrefix(line, "## "); ok {
			keys = map[string]bool{}
			sections[title] = keys
			continue
		}
		if keys == nil || !strings.HasPrefix(line, "| `") {
			continue
		}
		cell := strings.Split(line, "|")[1]
		for _, part := range strings.Split(cell, "`")[1:] {
			keys[part] = true
		}
	}
	aliases := strings.NewReplacer("S-Tab", "Shift+Tab")
	for _, g := range Bindings {
		doc, ok := sections[g.Title]
		if !ok {
			t.Errorf("docs/keybindings.md has no section %q", g.Title)
			continue
		}
		for _, b := range g.Bindings {
			for _, k := range strings.FieldsFunc(aliases.Replace(b.Keys), func(r rune) bool { return r == '/' || r == '…' }) {
				if k = strings.TrimSpace(k); !doc[k] {
					t.Errorf("section %q of docs/keybindings.md does not document key %q", g.Title, k)
				}
			}
		}
	}
}

func TestFailedInspectionAppliesPartialStateAndJobError(t *testing.T) {
	m := newModel(t)
	id := m.repos[0].ID
	ev := inspectionEvent(m, id, app.InspectionPayload{Generation: 5, Inspection: domain.Inspection{
		Stashes: domain.KnownValue(2)}})
	ev.events[0].Result.Status = domain.StatusFailed
	ev.events[0].Result.Error = errors.New("timed out after 1s")
	m.Update(ev)
	r := m.repo(id)
	if r.Inspected.Err == nil || r.Inspected.Err.Error() != "timed out after 1s" || r.Stashes.Value != 2 {
		t.Fatalf("err=%v stashes=%v", r.Inspected.Err, r.Stashes)
	}
	if m.pendingInspect() != 0 || len(m.routes) != 0 {
		t.Fatalf("finished inspection batch must be forgotten: pending=%d routes=%d", m.pendingInspect(), len(m.routes))
	}
}

// copyModel returns a model whose only repository is r, with a fake
// clipboard that records the copied texts or fails with err.
func copyModel(t *testing.T, r domain.Repository, err error) (*Model, *[]string) {
	t.Helper()
	m := newModel(t)
	r.PrimaryRemote = domain.SelectPrimaryRemote(r.Remotes, "")
	m.Update(inventoryMsg{result: inventory.Result{Repositories: []domain.Repository{r}}})
	var copied []string
	m.clipboard = func(_ context.Context, text string) (string, error) {
		if err != nil {
			return "", err
		}
		copied = append(copied, text)
		return "fake-copy", nil
	}
	return m, &copied
}

// copyEntry opens the copy menu, moves down n entries, and presses Enter,
// delivering the result of the copy command.
func copyEntry(m *Model, n int) {
	keys(m, "y")
	for range n {
		keys(m, "j")
	}
	if cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		m.Update(cmd())
	}
}

func copyRepo() domain.Repository {
	r := repo("api", "Work", false)
	r.Location.WorktreeRoot = "/r/api"
	r.Head = domain.KnownValue(domain.HeadState{Branch: "main", CommitID: "abc"})
	r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "git@host:o/api.git"}})
	return r
}

func TestCopyMenuCopiesRepositoryValues(t *testing.T) {
	m, copied := copyModel(t, copyRepo(), nil)
	keys(m, "y")
	view := ansi.Strip(m.View())
	for _, want := range []string{"Copy ─ api", "Path", "/r/api", "Branch", "main", "Remote URL", "git@host:o/api.git",
		"Web URL", "https://host/o/api"} {
		if !strings.Contains(view, want) {
			t.Errorf("copy menu lacks %q", want)
		}
	}
	keys(m, "esc")
	for i, want := range []string{"/r/api", "main", "git@host:o/api.git", "https://host/o/api"} {
		copyEntry(m, i)
		if m.mode != modeNormal || len(*copied) != i+1 || (*copied)[i] != want {
			t.Fatalf("entry %d: mode %v, copied %q", i, m.mode, *copied)
		}
		if !strings.Contains(m.message, want) || !strings.Contains(m.message, "fake-copy") || m.messageErr {
			t.Errorf("entry %d: message %q", i, m.message)
		}
	}
}

func TestCopyOnlyOnRepositoryRows(t *testing.T) {
	m, _ := copyModel(t, copyRepo(), nil)
	if h := strings.Join(m.normalHints(), " "); !strings.Contains(h, "y copy") {
		t.Errorf("hints on a repository row = %q", h)
	}
	keys(m, "v")
	if m.currentRow().count == 0 {
		t.Fatal("tree view must start on the family row")
	}
	keys(m, "y")
	if m.mode != modeNormal {
		t.Fatal("y on a family row must not open the copy menu")
	}
	if h := strings.Join(m.normalHints(), " "); strings.Contains(h, "copy") {
		t.Errorf("hints on a family row = %q", h)
	}
}

func TestCopyUnavailableEntries(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(r *domain.Repository)
		entry int
		want  string
	}{
		{"detached", func(r *domain.Repository) { r.Head.Value = domain.HeadState{CommitID: "abc", Detached: true} }, 1, "detached HEAD"},
		{"head unknown", func(r *domain.Repository) { r.Head = domain.Known[domain.HeadState]{} }, 1, "branch unknown"},
		{"no remote", func(r *domain.Repository) { r.Remotes = domain.KnownValue([]domain.Remote{}) }, 2, "no remote configured"},
		{"remotes unknown", func(r *domain.Repository) { r.Remotes = domain.Known[[]domain.Remote]{} }, 2, "remotes unknown"},
		{"no web URL", func(r *domain.Repository) {
			r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "/srv/git/api.git"}})
		}, 3, "no web URL for this remote"},
		{"control characters", func(r *domain.Repository) { r.Location.WorktreeRoot = "/r/a\nb" }, 0, "control characters"},
	}
	for _, tc := range cases {
		r := copyRepo()
		tc.edit(&r)
		m, copied := copyModel(t, r, nil)
		keys(m, "y")
		if view := ansi.Strip(m.View()); !strings.Contains(view, tc.want) {
			t.Errorf("%s: menu does not show %q", tc.name, tc.want)
		}
		keys(m, "esc")
		copyEntry(m, tc.entry)
		if len(*copied) != 0 || !m.messageErr || !strings.Contains(m.message, tc.want) {
			t.Errorf("%s: copied %q, message %q", tc.name, *copied, m.message)
		}
	}
}

func TestCopyUnbornBranch(t *testing.T) {
	r := copyRepo()
	r.Head = domain.KnownValue(domain.HeadState{Branch: "trunk", Unborn: true})
	m, copied := copyModel(t, r, nil)
	copyEntry(m, 1)
	if len(*copied) != 1 || (*copied)[0] != "trunk" {
		t.Fatalf("copied %q", *copied)
	}
}

func TestCopyRemoteURLWithoutCredentials(t *testing.T) {
	r := copyRepo()
	r.Remotes = domain.KnownValue([]domain.Remote{{Name: "origin", URL: "https://user:s3cret@host/o/api.git"}})
	m, copied := copyModel(t, r, nil)
	keys(m, "y")
	menu := m.View()
	keys(m, "esc")
	copyEntry(m, 2)
	copyEntry(m, 3)
	if strings.Join(*copied, " ") != "https://host/o/api.git https://host/o/api" {
		t.Fatalf("copied %q", *copied)
	}
	if strings.Contains(menu+m.message, "s3cret") {
		t.Fatal("the token must not be shown")
	}
}

func TestCopyReportsClipboardErrors(t *testing.T) {
	m, _ := copyModel(t, copyRepo(), errors.New("no clipboard tool found (xclip, xsel)"))
	copyEntry(m, 0)
	if !m.messageErr || !strings.Contains(m.message, "no clipboard tool found (xclip, xsel)") {
		t.Fatalf("message %q", m.message)
	}
}
