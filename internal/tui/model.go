// Package tui implements the Bubble Tea user interface. It consumes the
// application services and never constructs Git commands itself.
package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/clipboard"
	"github.com/mmo-con/headquarters/internal/config"
	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/jobs"
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeFamily
	modeHelp
	modeError
	modeSort
	modeOperations
	modeUpdatePreview
	modeCleanupPreview
	modeLaunchers
	modeCopy
	modeConfirmQuit
)

type repoRow struct {
	id     domain.RepositoryID
	family domain.FamilyID
	count  int // nonzero for a family header
}

// historyEntry holds the recent commits of a repository for the detail
// panel. It stays valid while HEAD points to head and the configured count
// is n.
type historyEntry struct {
	head    string
	n       int
	commits []domain.CommitSummary
	err     error
}

// historyRequest is the history job for the selected repository.
type historyRequest struct {
	batch uint64
	repo  domain.RepositoryID
	head  string
	n     int
}

// incomingKey identifies the incoming commits of a repository by the
// commits of its range, so a cached list stays valid while they do.
type incomingKey struct {
	repo domain.RepositoryID
	rng  app.IncomingRange
}

// incomingEntry holds up to n incoming commits of a range.
type incomingEntry struct {
	n       int
	commits []domain.CommitSummary
	err     error
}

// incomingRequest is the incoming job for the selected item.
type incomingRequest struct {
	batch uint64
	key   incomingKey
	n     int
}

// updatePreview is the update confirmation. It exists while the confirmation
// is open, also behind the quit confirmation, and holds the plan with the
// state the user confirms.
type updatePreview struct {
	targets  []domain.RepositoryID
	ok, skip []app.UpdateCandidate
	// hidden counts the marked targets hidden from the list.
	hidden int
	// ret is the mode that closing the confirmation returns to.
	ret mode
}

// visit remembers which batch row was opened in lazygit.
type visit struct {
	batch *app.Batch
	row   int
}

// Model is the Bubble Tea model of headquarters.
type Model struct {
	app *app.Application

	repos   []domain.Repository
	index   map[domain.RepositoryID]int
	visible []domain.RepositoryID // shown repositories, independent of the viewport
	rows    []repoRow
	cursor  int
	repoTop int
	marks   map[domain.RepositoryID]bool

	tree     bool
	expanded map[domain.FamilyID]bool
	// filterExpanded holds expansion changes made while a text or quick
	// filter is active. They are discarded when the filter is cleared, so
	// expanded is restored.
	filterExpanded map[domain.FamilyID]bool

	filter      app.Filter
	families    []domain.FamilyID
	familyIdx   int
	familyQuery string
	sortKey     app.SortKey

	mode mode
	// overlayIdx is the selected line of the open list overlay or the scroll
	// position of the help.
	overlayIdx int
	// opSelected is the batch row selected in the operations view; it stays
	// on its repository when the row moves to another status group.
	opSelected int
	width      int
	height     int

	loadingInventory bool
	// started is the start of the model; the time until the first inventory
	// is inspected is reported once.
	started         time.Time
	startupReported bool
	// routes holds the handler of every batch whose events the model still
	// needs. Events of other batches, such as inspections of an earlier
	// inventory or a superseded history request, are ignored.
	routes map[uint64]route
	// inspections runs the inspection batches of the current inventory.
	inspections *app.Inspections
	warnings    []string
	message     string
	messageErr  bool

	// ops runs the fetch and update batches.
	ops *app.Operations

	// clipboard copies text and returns the name of the tool used.
	clipboard func(ctx context.Context, text string) (string, error)

	preview    *updatePreview
	cleanup    *cleanupPreview
	quitReturn mode
	visit      *visit

	// history caches recent commits for the detail panel; only the selected
	// repository is ever requested, one at a time.
	history        map[domain.RepositoryID]historyEntry
	historyPending *historyRequest
	// incoming caches the incoming commits of the selected repository, the
	// selected confirmation row, or the selected batch row; one at a time.
	incoming        map[incomingKey]incomingEntry
	incomingPending *incomingRequest

	fatal error
	now   func() time.Time

	theme theme
	// dark reports whether the terminal background is dark; it selects
	// the variant of a theme family.
	dark     bool
	icons    icons
	frame    int
	spinning bool
	msgSeq   uint64
	msgTimer uint64
}

// New creates the model.
func New(a *app.Application) *Model {
	m := &Model{
		app:              a,
		index:            map[domain.RepositoryID]int{},
		marks:            map[domain.RepositoryID]bool{},
		expanded:         map[domain.FamilyID]bool{},
		history:          map[domain.RepositoryID]historyEntry{},
		incoming:         map[incomingKey]incomingEntry{},
		routes:           map[uint64]route{},
		loadingInventory: true,
		started:          time.Now(),
		width:            100,
		height:           30,
		now:              time.Now,
		dark:             true,
		theme:            newTheme(a.Config.UI.ThemeFor(true)),
		icons:            iconSet(a.Config.UI.Icons),
		tree:             a.Config.UI.View == "tree",
		sortKey:          configuredSort(a.Config.UI),
		clipboard:        clipboard.System().Copy,
	}
	m.ops = app.NewOperations(a, func(js []jobs.Job) uint64 { return m.submit(js, m.applyBatchEvent) },
		func() time.Time { return m.now() })
	m.inspections = app.NewInspections(a, func(js []jobs.Job) uint64 { return m.submit(js, m.applyInspection) })
	return m
}

// SetDarkBackground sets whether the terminal background is dark, which
// selects the variant of a theme family. The default is dark.
func (m *Model) SetDarkBackground(dark bool) {
	m.dark = dark
	m.theme = newTheme(m.app.Config.UI.ThemeFor(dark))
}

// configuredSort returns the sort order of [ui].sort; the configuration
// validates the name.
func configuredSort(ui config.UI) app.SortKey {
	k, _ := app.SortKeyNamed(ui.Sort)
	return k
}

// Err returns a fatal error that ended the program, if any.
func (m *Model) Err() error { return m.fatal }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadInventory(), m.waitEvents(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

// loadInventory builds the inventory of the active configuration. The
// configuration and the client are captured here, in the update loop, so the
// command does not access the Application concurrently.
func (m *Model) loadInventory() tea.Cmd {
	cfg, client := m.app.Config, m.app.Git
	return func() tea.Msg {
		return inventoryMsg{result: app.BuildInventory(context.Background(), cfg, client)}
	}
}

// reload loads the configuration and builds its inventory in the background.
// The update loop activates both together when inventoryMsg arrives.
func (m *Model) reload() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		l, err := a.LoadConfig(context.Background())
		if err != nil {
			return reloadFailedMsg{err: err}
		}
		return inventoryMsg{loaded: l, result: app.BuildInventory(context.Background(), l.Config, l.Git)}
	}
}

// waitEvents is the single forwarder from the scheduler to Bubble Tea. Job
// workers push to an unbounded queue and never block on the user interface.
func (m *Model) waitEvents() tea.Cmd {
	q := m.app.Scheduler.Events
	return func() tea.Msg {
		ev := q.Next()
		if ev == nil {
			return nil
		}
		return eventsMsg{events: ev}
	}
}

func (m *Model) repo(id domain.RepositoryID) *domain.Repository {
	if i, ok := m.index[id]; ok {
		return &m.repos[i]
	}
	return nil
}

func (m *Model) currentRow() repoRow {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return repoRow{}
	}
	return m.rows[m.cursor]
}

func (m *Model) current() *domain.Repository {
	return m.repo(m.currentRow().id)
}

func (m *Model) lastOp(id domain.RepositoryID) *domain.OperationResult {
	if r, ok := m.ops.LastResult(id); ok {
		return &r
	}
	return nil
}

// setMessage and setError show a toast. External text in msg is made
// printable.
func (m *Model) setMessage(msg string) {
	m.message, m.messageErr = domain.Printable(msg), false
	m.msgSeq++
}

func (m *Model) setError(msg string) {
	m.message, m.messageErr = domain.Printable(msg), true
	m.msgSeq++
}

// notify shows a notice of the application services, if it has a text.
func (m *Model) notify(n app.Notice) {
	switch {
	case n.Text == "":
	case n.Error:
		m.setError(n.Text)
	default:
		m.setMessage(n.Text)
	}
}

// busy reports whether a spinner should be animated.
func (m *Model) busy() bool {
	return m.loadingInventory || m.pendingInspect() > 0 || m.ops.Running() || m.cleanupLoading()
}

// pendingInspect returns the number of unfinished inspections.
func (m *Model) pendingInspect() int { return m.inspections.Pending() }

// recompute applies filter and sorting while keeping the cursor on the same
// repository where possible.
func (m *Model) recompute() {
	keep := m.currentRow()
	if !m.filterActive() {
		m.filterExpanded = nil
	}
	m.filter.Family = ""
	if m.familyIdx > 0 && m.familyIdx <= len(m.families) {
		m.filter.Family = m.families[m.familyIdx-1]
	}
	var list []*domain.Repository
	for i := range m.repos {
		r := &m.repos[i]
		if m.matches(m.filter, r, m.marks[r.ID]) {
			list = append(list, r)
		}
	}
	app.Sort(list, m.sortKey, m.healthOf)
	m.rows = m.rows[:0]
	m.visible = m.visible[:0]
	addRepo := func(r *domain.Repository) {
		m.rows = append(m.rows, repoRow{id: r.ID, family: r.Family})
		m.visible = append(m.visible, r.ID)
	}
	if m.tree {
		groups := make(map[domain.FamilyID][]*domain.Repository)
		for _, r := range list {
			groups[r.Family] = append(groups[r.Family], r)
		}
		for _, family := range m.families {
			group := groups[family]
			if len(group) == 0 {
				continue
			}
			m.rows = append(m.rows, repoRow{family: family, count: len(group)})
			if m.isExpanded(family) {
				for _, r := range group {
					addRepo(r)
				}
			}
		}
	} else {
		for _, r := range list {
			addRepo(r)
		}
	}

	to := m.cursor
	if keep.id != "" || keep.count > 0 {
		fallback := -1
		for i, row := range m.rows {
			if keep.id != "" && row.id == keep.id {
				fallback = i
				break
			}
			if fallback == -1 && row.family == keep.family && (row.count > 0 || (keep.count > 0 && !m.tree)) {
				fallback = i
			}
		}
		if fallback >= 0 {
			to = fallback
		}
	}
	m.moveRepoCursor(to)
}

func (m *Model) moveRepoCursor(to int) {
	m.cursor = min(max(to, 0), max(len(m.rows)-1, 0))
	m.adjustRepoViewport()
}

// filterActive reports whether a text or quick filter is set. It opens the
// families with matches in the tree.
// matches reports whether r passes f at the time of the model, the time
// that also decides the health and the fetch hints.
func (m *Model) matches(f app.Filter, r *domain.Repository, marked bool) bool {
	f.Now = m.now()
	return f.Matches(r, marked)
}

func (m *Model) filterActive() bool { return strings.TrimSpace(m.filter.Text) != "" }

// isExpanded reports whether a family is expanded in the tree. While a filter
// is active, families are expanded unless collapsed during the filter.
func (m *Model) isExpanded(f domain.FamilyID) bool {
	if m.filterActive() {
		if v, ok := m.filterExpanded[f]; ok {
			return v
		}
		return true
	}
	return m.expanded[f]
}

// setExpanded expands or collapses a family; while a filter is active, only
// until the filter is cleared.
func (m *Model) setExpanded(f domain.FamilyID, v bool) {
	if !m.filterActive() {
		m.expanded[f] = v
		return
	}
	if m.filterExpanded == nil {
		m.filterExpanded = map[domain.FamilyID]bool{}
	}
	m.filterExpanded[f] = v
}

// familyMembers returns the repositories of a family that match the filter,
// also when the family is collapsed.
func (m *Model) familyMembers(f domain.FamilyID) []*domain.Repository {
	var list []*domain.Repository
	for i := range m.repos {
		r := &m.repos[i]
		if r.Family == f && m.matches(m.filter, r, m.marks[r.ID]) {
			list = append(list, r)
		}
	}
	return list
}

// toggleFamilyMarks marks the matching repositories of a family, or unmarks
// them if all are marked.
func (m *Model) toggleFamilyMarks(f domain.FamilyID) {
	members := m.familyMembers(f)
	all := true
	for _, r := range members {
		all = all && m.marks[r.ID]
	}
	for _, r := range members {
		if all {
			delete(m.marks, r.ID)
		} else {
			m.marks[r.ID] = true
		}
	}
}

// moveToFamilyRow moves the cursor to the nearest family row in direction
// step (-1 or 1), if there is one.
func (m *Model) moveToFamilyRow(step int) {
	for i := m.cursor + step; i >= 0 && i < len(m.rows); i += step {
		if m.rows[i].count > 0 {
			m.moveRepoCursor(i)
			return
		}
	}
}

func (m *Model) adjustRepoViewport() {
	rows := m.tableRows()
	margin := min(2, (rows-1)/2)
	if m.cursor < m.repoTop+margin {
		m.repoTop = m.cursor - margin
	} else if m.cursor >= m.repoTop+rows-margin {
		m.repoTop = m.cursor - rows + margin + 1
	}
	m.repoTop = min(max(m.repoTop, 0), max(len(m.rows)-rows, 0))
}

func (m *Model) rebuildFamilies() {
	var current domain.FamilyID
	if m.familyIdx > 0 && m.familyIdx <= len(m.families) {
		current = m.families[m.familyIdx-1]
	}
	seen := map[domain.FamilyID]bool{}
	m.families = m.families[:0]
	for _, r := range m.repos {
		if !seen[r.Family] {
			seen[r.Family] = true
			m.families = append(m.families, r.Family)
		}
	}
	sortFamilies(m.families)
	m.familyIdx = 0
	for i, f := range m.families {
		if f == current {
			m.familyIdx = i + 1
		}
	}
	if m.mode == modeFamily {
		m.overlayIdx = min(m.overlayIdx, max(len(m.familyChoices())-1, 0))
	}
}

func sortFamilies(f []domain.FamilyID) {
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && f[j] < f[j-1]; j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

func (m *Model) familyLabel(i int) string {
	if i == 0 {
		return "All families"
	}
	return string(m.families[i-1])
}

func (m *Model) familyChoices() []int {
	query := strings.ToLower(strings.TrimSpace(m.familyQuery))
	var choices []int
	for i := 0; i <= len(m.families); i++ {
		if strings.Contains(strings.ToLower(m.familyLabel(i)), query) {
			choices = append(choices, i)
		}
	}
	return choices
}

// refresh starts inspection of the given entries and returns the ID of the
// inspection batch, or 0 if nothing is inspected.
func (m *Model) refresh(ids []domain.RepositoryID) uint64 {
	var repos []domain.Repository
	seen := map[domain.RepositoryID]bool{}
	for _, id := range ids {
		if e, ok := m.history[id]; ok && e.err != nil {
			delete(m.history, id)
		}
		if r := m.repo(id); r != nil && !seen[id] {
			seen[id] = true
			repos = append(repos, *r)
		}
	}
	for k, e := range m.incoming {
		if seen[k.repo] && e.err != nil {
			delete(m.incoming, k)
		}
	}
	return m.inspections.Start(repos)
}

// route handles an event of a batch started by the model. Repositories
// that a finished operation changed are added to reinspect.
type route func(ev jobs.Event, reinspect map[domain.RepositoryID]bool)

// submit starts js and routes the events of the batch to r.
func (m *Model) submit(js []jobs.Job, r route) uint64 {
	id := m.app.Scheduler.Submit(js)
	m.routes[id] = r
	return id
}

// forget cancels a batch and ignores its further events.
func (m *Model) forget(id uint64) {
	m.app.Scheduler.Cancel(id)
	delete(m.routes, id)
}

// applyInspection applies a finished inspection. A failed inspection still
// carries the state it could determine.
func (m *Model) applyInspection(ev jobs.Event, _ map[domain.RepositoryID]bool) {
	if m.inspections.Handle(ev, m.repo, m.now()) {
		delete(m.routes, ev.BatchID)
	}
}

func (m *Model) allIDs() []domain.RepositoryID {
	ids := make([]domain.RepositoryID, len(m.repos))
	for i, r := range m.repos {
		ids[i] = r.ID
	}
	return ids
}

// targets returns the targets of a batch and the number of marked
// repositories hidden from the list. With visible, these are the visible
// repositories regardless of marks; otherwise the marked repositories if any
// are marked, else the repository under the cursor.
func (m *Model) targets(visible bool) (list []domain.Repository, hidden int) {
	if visible {
		for _, id := range m.visible {
			list = append(list, *m.repo(id))
		}
		return list, 0
	}
	if len(m.marks) > 0 {
		vis := map[domain.RepositoryID]bool{}
		for _, id := range m.visible {
			vis[id] = true
		}
		for i := range m.repos {
			if m.marks[m.repos[i].ID] {
				list = append(list, m.repos[i])
				if !vis[m.repos[i].ID] {
					hidden++
				}
			}
		}
		return list, hidden
	}
	if row := m.currentRow(); row.count > 0 {
		for _, r := range m.familyMembers(row.family) {
			list = append(list, *r)
		}
		return list, 0
	}
	if r := m.current(); r != nil {
		list = append(list, *r)
	}
	return list, 0
}

// historyCount returns the configured number of recent commits.
func (m *Model) historyCount() int { return m.app.Config.UI.DetailCommits }

// requestHistory reads the recent commits of the selected repository in the
// background unless they are cached for its current HEAD. A request for
// another repository is cancelled, so moving through the list starts at
// most one Git process at a time. A count of 1 needs no process, because
// inspection already reads the latest commit.
func (m *Model) requestHistory() {
	n := m.historyCount()
	r := m.current()
	if n <= 1 || r == nil || !r.IsAvailable() || !r.Head.Valid || r.Head.Value.Unborn || r.Head.Value.CommitID == "" {
		return
	}
	head := r.Head.Value.CommitID
	if e, ok := m.history[r.ID]; ok && e.head == head && e.n == n {
		return
	}
	if p := m.historyPending; p != nil {
		if p.repo == r.ID && p.head == head && p.n == n {
			return
		}
		m.forget(p.batch)
	}
	job := app.HistoryJob(m.app.Git, *r, n, m.app.Config.Git.InspectionTimeout)
	m.historyPending = &historyRequest{batch: m.submit([]jobs.Job{job}, m.applyHistory), repo: r.ID, head: head, n: n}
}

// applyHistory stores the result of the pending history request. The route
// of a superseded request is removed when it is cancelled.
func (m *Model) applyHistory(ev jobs.Event, _ map[domain.RepositoryID]bool) {
	p := m.historyPending
	if p == nil || !ev.Result.Status.Final() {
		return
	}
	m.historyPending = nil
	delete(m.routes, ev.BatchID)
	payload, ok := ev.Payload.(app.HistoryPayload)
	if !ok || payload.Head != p.head {
		return
	}
	m.history[p.repo] = historyEntry{head: p.head, n: p.n, commits: payload.Commits, err: ev.Result.Error}
}

// recentCommits returns the cached recent commits of r for its current HEAD.
func (m *Model) recentCommits(r *domain.Repository) (historyEntry, bool) {
	e, ok := m.history[r.ID]
	if !ok || !r.Head.Valid || e.head != r.Head.Value.CommitID || e.n != m.historyCount() {
		return historyEntry{}, false
	}
	return e, true
}
