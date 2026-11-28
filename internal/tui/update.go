package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/config"
	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/jobs"
	"github.com/mmo-con/headquarters/internal/launcher"
)

// Toast durations for informational and error messages.
const (
	toastInfo  = 4 * time.Second
	toastError = 10 * time.Second
	spinEvery  = 100 * time.Millisecond
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	m.reportStartup()
	m.requestHistory()
	m.requestIncoming()
	return m, tea.Batch(cmd, m.housekeeping())
}

// housekeeping starts the spinner while work is pending and schedules the
// expiry of the current message.
func (m *Model) housekeeping() tea.Cmd {
	var cmds []tea.Cmd
	if m.busy() && !m.spinning {
		m.spinning = true
		cmds = append(cmds, tea.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} }))
	}
	if m.message != "" && m.msgTimer != m.msgSeq {
		m.msgTimer = m.msgSeq
		seq, d := m.msgSeq, toastInfo
		if m.messageErr {
			d = toastError
		}
		cmds = append(cmds, tea.Tick(d, func(time.Time) tea.Msg { return toastExpiredMsg{seq: seq} }))
	}
	return tea.Batch(cmds...)
}

// reportStartup shows once how long loading and inspecting the first
// inventory took, unless a message such as a configuration warning is shown.
func (m *Model) reportStartup() {
	if m.startupReported || m.loadingInventory || m.pendingInspect() > 0 {
		return
	}
	m.startupReported = true
	if m.message == "" {
		noun := "repositories"
		if len(m.repos) == 1 {
			noun = "repository"
		}
		m.setMessage(fmt.Sprintf("loaded %d %s in %s", len(m.repos), noun, shortDuration(m.now().Sub(m.started))))
	}
}

// shortDuration formats d as milliseconds below one second, else as seconds
// with one decimal.
func shortDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinMsg:
		m.frame++
		if m.busy() {
			return m, tea.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })
		}
		m.spinning = false
		return m, nil
	case toastExpiredMsg:
		if msg.seq == m.msgSeq {
			m.message = ""
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.adjustRepoViewport()
		return m, nil
	case tickMsg:
		return m, tick()
	case inventoryMsg:
		m.applyInventory(msg)
		return m, nil
	case reloadFailedMsg:
		m.loadingInventory = false
		m.setError("configuration not reloaded: " + firstLine(msg.err.Error()))
		m.warnings = append(m.warnings, msg.err.Error())
		return m, nil
	case eventsMsg:
		m.applyEvents(msg.events)
		return m, m.waitEvents()
	case handoverDoneMsg:
		return m, m.finishHandover(msg)
	case copyDoneMsg:
		m.finishCopy(msg)
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) applyInventory(msg inventoryMsg) {
	m.loadingInventory = false
	if msg.loaded != nil {
		// A changed default view takes effect; otherwise the view the user
		// switched to with v is kept.
		if view := msg.loaded.Config.UI.View; view != m.app.Config.UI.View {
			m.tree = view == "tree"
		}
		// Likewise for the sort order chosen with s.
		if sort := msg.loaded.Config.UI.Sort; sort != m.app.Config.UI.Sort {
			m.sortKey = configuredSort(msg.loaded.Config.UI)
		}
		m.app.Activate(msg.loaded)
	}
	m.theme = newTheme(m.app.Config.UI.ThemeFor(m.dark))
	m.icons = iconSet(m.app.Config.UI.Icons)
	m.repos = msg.result.Repositories
	m.warnings = msg.result.Warnings
	m.index = make(map[domain.RepositoryID]int, len(m.repos))
	for i, r := range m.repos {
		m.index[r.ID] = i
	}
	for id := range m.marks {
		if _, ok := m.index[id]; !ok {
			delete(m.marks, id)
		}
	}
	m.rebuildFamilies()
	m.recompute()
	for _, id := range m.inspections.Forget() {
		delete(m.routes, id)
	}
	m.ops.DropUpdate()
	if p := m.historyPending; p != nil {
		m.forget(p.batch)
		m.historyPending = nil
	}
	m.history = map[domain.RepositoryID]historyEntry{}
	m.dropIncoming()
	m.refresh(m.allIDs())
	if len(m.warnings) > 0 {
		m.setError(fmt.Sprintf("%d configuration warning(s); press e for details", len(m.warnings)))
	}
}

func (m *Model) applyEvents(events []jobs.Event) {
	reinspect := map[domain.RepositoryID]bool{}
	for _, ev := range events {
		if r := m.routes[ev.BatchID]; r != nil {
			r(ev, reinspect)
		}
	}
	if len(reinspect) > 0 {
		ids := make([]domain.RepositoryID, 0, len(reinspect))
		for id := range reinspect {
			ids = append(ids, id)
		}
		m.ops.AwaitInspection(m.refresh(ids))
	}
	app.RefreshStoreFetchTimes(m.repos)
	m.recompute()
	m.advanceUpdate()
}

// applyBatchEvent passes an event of a fetch, update, or cleanup batch to the
// operations. The route ends with the batch.
func (m *Model) applyBatchEvent(ev jobs.Event, reinspect map[domain.RepositoryID]bool) {
	ids, summary := m.ops.Handle(ev)
	for _, id := range ids {
		reinspect[id] = true
	}
	m.notify(summary)
	if b := m.ops.Batch(); b == nil || b.ID != ev.BatchID || b.Done() {
		delete(m.routes, ev.BatchID)
	}
}

// advanceUpdate starts a confirmed update once its fetch and the following
// inspections are finished. No dialog opens here, because a key pressed for
// the current view would then confirm it; an open operations view shows the
// new batch.
func (m *Model) advanceUpdate() {
	n, started := m.ops.Advance(m.repos, m.inspections.Running)
	m.notify(n)
	if started {
		m.showBatch(false)
	}
}

// showBatch selects the first row of a new batch in the operations view; open
// opens the view, otherwise only an open view is updated.
func (m *Model) showBatch(open bool) {
	if open || m.mode == modeOperations {
		m.mode, m.opSelected = modeOperations, m.firstOpRow()
	}
}

// openUpdatePreview shows the confirmation of an update of targets with their
// current state; Esc returns to ret.
func (m *Model) openUpdatePreview(targets []domain.Repository, hidden int, ret mode) {
	if len(targets) == 0 {
		m.setMessage("no repository selected")
		return
	}
	p := &updatePreview{hidden: hidden, ret: ret, targets: make([]domain.RepositoryID, 0, len(targets))}
	for _, r := range targets {
		p.targets = append(p.targets, r.ID)
	}
	p.ok, p.skip = m.ops.Plan(targets)
	m.preview = p
	m.mode, m.overlayIdx = modeUpdatePreview, 0
}

// closePreview closes the update confirmation.
func (m *Model) closePreview() {
	m.mode = m.preview.ret
	m.preview = nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return m.quit()
	}
	switch m.mode {
	case modeConfirmQuit:
		return m.keyConfirmQuit(key)
	case modeFilter:
		return m.keyFilter(msg)
	case modeFamily:
		m.keyFamily(msg)
		return nil
	case modeHelp:
		switch key {
		case "j", "down":
			m.overlayIdx++
		case "k", "up":
			m.overlayIdx = max(m.overlayIdx-1, 0)
		default:
			m.mode = modeNormal
		}
		return nil
	case modeError:
		return m.keyError(key)
	case modeSort:
		return m.keySort(key)
	case modeOperations:
		return m.keyOperations(key)
	case modeUpdatePreview:
		return m.keyUpdatePreview(key)
	case modeCleanupPreview:
		return m.keyCleanupPreview(key)
	case modeLaunchers:
		return m.keyLaunchers(key)
	case modeCopy:
		return m.keyCopy(key)
	}
	return m.keyNormal(key)
}

func (m *Model) keyNormal(key string) tea.Cmd {
	switch key {
	case "q":
		return m.quit()
	case "j", "down":
		m.moveRepoCursor(m.cursor + 1)
	case "k", "up":
		m.moveRepoCursor(m.cursor - 1)
	case "g", "home":
		m.moveRepoCursor(0)
	case "G", "end":
		m.moveRepoCursor(len(m.rows) - 1)
	case "pgdown":
		m.moveRepoCursor(m.cursor + m.pageSize())
	case "pgup":
		m.moveRepoCursor(m.cursor - m.pageSize())
	case "/":
		m.mode = modeFilter
	case "esc":
		if m.filter.Text != "" {
			m.filter.Text = ""
			m.recompute()
		}
	case "tab":
		m.familyIdx = (m.familyIdx + 1) % (len(m.families) + 1)
		m.recompute()
	case "shift+tab":
		m.familyIdx = (m.familyIdx + len(m.families)) % (len(m.families) + 1)
		m.recompute()
	case "t":
		m.mode, m.overlayIdx, m.familyQuery = modeFamily, m.familyIdx, ""
	case "v":
		m.tree = !m.tree
		m.recompute()
	case "s":
		m.mode, m.overlayIdx = modeSort, int(m.sortKey)
	case "r":
		m.refresh(m.visible)
		m.setMessage(fmt.Sprintf("refreshing %d repositories", len(m.visible)))
	case "R":
		if m.loadingInventory {
			m.setMessage("the inventory is still loading")
			return nil
		}
		if m.ops.UpdatePending() {
			m.setMessage("an update is waiting for its fetch; reload afterward")
			return nil
		}
		m.loadingInventory = true
		m.setMessage("reloading configuration and rebuilding inventory")
		return m.reload()
	case "e":
		m.mode, m.overlayIdx = modeError, 0
	case "b":
		if m.ops.Batch() != nil {
			m.mode, m.opSelected = modeOperations, m.firstOpRow()
		} else {
			m.setMessage("no operations yet")
		}
	case "?":
		m.mode, m.overlayIdx = modeHelp, 0
	case "1", "2", "3", "4", "5", "6", "7", "8":
		q := m.quickFilters()[key[0]-'1']
		if strings.TrimSpace(m.filter.Text) == q.token {
			m.filter.Text = ""
		} else {
			m.filter.Text = q.token
		}
		m.recompute()
	case "enter":
		if row := m.currentRow(); row.count > 0 {
			m.setExpanded(row.family, !m.isExpanded(row.family))
			m.recompute()
			return nil
		}
		return m.openLazygit(m.current())
	case "l", "right":
		if row := m.currentRow(); m.tree && row.count > 0 {
			m.setExpanded(row.family, true)
			m.recompute()
		}
	case "h", "left":
		// On a repository, the family row keeps the cursor after recompute.
		if row := m.currentRow(); m.tree && (row.count > 0 || row.id != "") {
			m.setExpanded(row.family, false)
			m.recompute()
		}
	case "z":
		if m.tree {
			anyExpanded := false
			for _, row := range m.rows {
				anyExpanded = anyExpanded || (row.count > 0 && m.isExpanded(row.family))
			}
			for _, f := range m.families {
				m.setExpanded(f, !anyExpanded)
			}
			m.recompute()
		}
	case "[":
		if m.tree {
			m.moveToFamilyRow(-1)
		}
	case "]":
		if m.tree {
			m.moveToFamilyRow(1)
		}
	case "o":
		if m.current() != nil {
			m.mode, m.overlayIdx = modeLaunchers, 0
		}
	case "y":
		m.openCopy()
	case " ":
		if row := m.currentRow(); row.count > 0 {
			m.toggleFamilyMarks(row.family)
			m.moveRepoCursor(m.cursor + 1)
		} else if r := m.current(); r != nil {
			if m.marks[r.ID] {
				delete(m.marks, r.ID)
			} else {
				m.marks[r.ID] = true
			}
			m.moveRepoCursor(m.cursor + 1)
		}
	case "a":
		for _, id := range m.visible {
			m.marks[id] = true
		}
	case "A":
		m.marks = map[domain.RepositoryID]bool{}
		m.recompute()
	case "f", "F":
		targets, hidden := m.targets(key == "F")
		if len(targets) == 0 {
			m.setMessage("no repository selected")
			return nil
		}
		m.startFetch(targets, hidden)
	case "u", "U":
		targets, hidden := m.targets(key == "U")
		m.openUpdatePreview(targets, hidden, modeNormal)
	case "d", "D":
		targets, hidden := m.targets(key == "D")
		m.openCleanupPreview(targets, hidden)
	}
	return nil
}

// quit ends the program. While a batch runs, it asks first,
// because quitting cancels the batch.
func (m *Model) quit() tea.Cmd {
	if m.mode == modeConfirmQuit || !m.ops.Running() {
		return tea.Quit
	}
	m.quitReturn = m.mode
	m.mode = modeConfirmQuit
	return nil
}

func (m *Model) keyConfirmQuit(key string) tea.Cmd {
	switch key {
	case "enter", "y", "q":
		return tea.Quit
	case "esc", "n":
		m.mode = m.quitReturn
	}
	return nil
}

func (m *Model) keyFilter(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		m.mode = modeNormal
	case tea.KeyEsc:
		m.filter.Text = ""
		m.mode = modeNormal
	default:
		if !editQuery(&m.filter.Text, msg) {
			return nil
		}
	}
	m.recompute()
	return nil
}

func editQuery(query *string, msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyBackspace:
		if r := []rune(*query); len(r) > 0 {
			*query = string(r[:len(r)-1])
		}
	case tea.KeyCtrlU:
		*query = ""
	case tea.KeySpace:
		*query += " "
	case tea.KeyRunes:
		*query += string(msg.Runes)
	default:
		return false
	}
	return true
}

func (m *Model) keyFamily(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
	case tea.KeyEnter:
		choices := m.familyChoices()
		if len(choices) > 0 {
			m.familyIdx = choices[m.overlayIdx]
			m.recompute()
			m.mode = modeNormal
		}
	case tea.KeyUp, tea.KeyDown:
		m.moveOverlay(msg.String(), len(m.familyChoices()))
	default:
		if editQuery(&m.familyQuery, msg) {
			m.overlayIdx = 0
		}
	}
}

// moveOpRow moves the selection of the operations view in display order.
// The selection is the index of a batch row, so it stays on the same
// repository when the row moves to another status group.
func (m *Model) moveOpRow(key string) bool {
	order := m.opOrder()
	pos := max(slices.Index(order, m.opSelected), 0)
	switch key {
	case "j", "down":
		pos = min(pos+1, len(order)-1)
	case "k", "up":
		pos = max(pos-1, 0)
	default:
		return false
	}
	if len(order) > 0 {
		m.opSelected = order[pos]
	}
	return true
}

func (m *Model) moveOverlay(key string, n int) bool {
	switch key {
	case "j", "down":
		if m.overlayIdx++; m.overlayIdx >= n {
			m.overlayIdx = 0
		}
	case "k", "up":
		if m.overlayIdx--; m.overlayIdx < 0 {
			m.overlayIdx = max(n-1, 0)
		}
	default:
		return false
	}
	return true
}

func (m *Model) keySort(key string) tea.Cmd {
	if m.moveOverlay(key, len(app.SortKeys)) {
		return nil
	}
	if i := slices.IndexFunc(config.SortOrders, func(name string) bool { return name[:1] == key }); i >= 0 {
		m.overlayIdx, key = i, "enter"
	}
	switch key {
	case "enter":
		m.sortKey = app.SortKeys[m.overlayIdx]
		m.recompute()
		m.mode = modeNormal
	case "esc", "q", "s":
		m.mode = modeNormal
	}
	return nil
}

func (m *Model) keyError(key string) tea.Cmd {
	switch key {
	case "esc", "q", "e":
		m.mode = modeNormal
	case "t":
		if r := m.current(); r != nil {
			m.mode = modeNormal
			return m.interactiveFetch(*r)
		}
	}
	return nil
}

func (m *Model) keyOperations(key string) tea.Cmd {
	if m.moveOpRow(key) {
		return nil
	}
	switch key {
	case "esc", "q", "b":
		m.mode = modeNormal
	case "c":
		update, batch := m.ops.Cancel()
		if update {
			m.setMessage("update cancelled")
		}
		if batch && m.message != "update cancelled" {
			m.setMessage("cancelling batch")
		}
	case "x":
		n, started := m.ops.Retry()
		m.notify(n)
		if started {
			m.showBatch(true)
		}
	case "enter":
		b := m.ops.Batch()
		if b == nil || m.opSelected >= len(b.Rows) {
			return nil
		}
		cmd := m.openLazygit(m.repo(b.Rows[m.opSelected].Job.RepositoryID))
		if cmd != nil {
			m.visit = &visit{batch: b, row: m.opSelected}
		}
		return cmd
	case "u":
		targets, n := m.ops.NotUpdated(m.repos)
		m.notify(n)
		if len(targets) > 0 {
			m.openUpdatePreview(targets, 0, modeOperations)
		}
	case "t":
		b := m.ops.Batch()
		if b == nil || m.opSelected >= len(b.Rows) {
			return nil
		}
		row := b.Rows[m.opSelected]
		if row.Job.Operation != domain.OperationFetch || row.Result.Status != domain.StatusFailed {
			m.setError("select a failed fetch to retry it in the terminal")
			return nil
		}
		if r := m.repo(row.Job.RepositoryID); r != nil {
			return m.interactiveFetch(*r)
		}
	}
	return nil
}

func (m *Model) keyUpdatePreview(key string) tea.Cmd {
	if m.moveOverlay(key, len(m.preview.ok)+len(m.preview.skip)) {
		return nil
	}
	switch key {
	case "enter", "y":
		m.startUpdate()
	case "esc", "n", "q":
		m.closePreview()
	}
	return nil
}

// startUpdate fetches the targets of the confirmed update; the operations
// start the update itself once the fetch and the following inspections are
// finished.
func (m *Model) startUpdate() {
	if !m.ops.Running() && m.loadingInventory {
		m.setError("the inventory is still loading")
		return
	}
	n, started := m.ops.ConfirmUpdate(m.repos, m.preview.targets)
	m.notify(n)
	if n.Error {
		return
	}
	m.closePreview()
	if started {
		m.showBatch(true)
	}
}

type launcherItem struct {
	label string
	run   func(r domain.Repository) tea.Cmd
}

func (m *Model) launcherItems() []launcherItem {
	var items []launcherItem
	for _, l := range m.app.Launchers {
		l := l
		items = append(items, launcherItem{label: l.Name, run: func(r domain.Repository) tea.Cmd {
			if l.Err != nil {
				m.setError(fmt.Sprintf("launcher %s: %v", l.Name, l.Err))
				return nil
			}
			return m.handover(r, l.Name, l.Command(r.Location.WorktreeRoot, r.WorkspaceDir()))
		}})
	}
	items = append(items, launcherItem{label: "Open remote in browser", run: func(r domain.Repository) tea.Cmd {
		if r.PrimaryRemote == nil {
			m.setError("no remote configured")
			return nil
		}
		u, ok := launcher.BrowserURL(r.PrimaryRemote.URL)
		if !ok {
			m.setError("cannot derive an HTTPS URL from " + domain.RedactURL(r.PrimaryRemote.URL))
			return nil
		}
		if err := launcher.OpenBrowser(u); err != nil {
			m.setError(err.Error())
			return nil
		}
		m.setMessage("opened " + u)
		return nil
	}})
	return items
}

func (m *Model) keyLaunchers(key string) tea.Cmd {
	items := m.launcherItems()
	if m.moveOverlay(key, len(items)) {
		return nil
	}
	switch key {
	case "enter":
		m.mode = modeNormal
		r := m.current()
		if r == nil || m.overlayIdx >= len(items) {
			return nil
		}
		if !r.IsAvailable() {
			m.setError(r.Name + " is " + r.Availability.String())
			return nil
		}
		return items[m.overlayIdx].run(*r)
	case "esc", "q", "o":
		m.mode = modeNormal
	}
	return nil
}

func (m *Model) startFetch(targets []domain.Repository, hidden int) {
	n, started := m.ops.Fetch(m.repos, targets)
	m.notify(n)
	if !started {
		return
	}
	m.showBatch(true)
	if hidden > 0 {
		m.setMessage(fmt.Sprintf("%s, including %d hidden marked", m.message, hidden))
	}
}

func (m *Model) openLazygit(r *domain.Repository) tea.Cmd {
	if r == nil {
		return nil
	}
	if !r.IsAvailable() {
		m.setError(r.Name + " is " + r.Availability.String())
		return nil
	}
	if !m.app.Lazygit.Available() {
		m.setError(m.app.Lazygit.Err.Error())
		return nil
	}
	return m.handover(*r, "lazygit", m.app.Lazygit.Command(r.Location.WorktreeRoot))
}

func (m *Model) interactiveFetch(r domain.Repository) tea.Cmd {
	if !r.IsAvailable() {
		m.setError(r.Name + " is " + r.Availability.String())
		return nil
	}
	return m.handover(r, "git fetch", m.app.InteractiveFetchCommand(r))
}

// handover gives the terminal to cmd. The store is locked so that no job runs
// for it while the child process owns the terminal.
func (m *Model) handover(r domain.Repository, label string, cmd *exec.Cmd) tea.Cmd {
	if busy, ok := m.ops.BeginHandover(r.StoreID); !ok {
		m.setError(fmt.Sprintf("%s in progress for %s", busy, r.Name))
		return nil
	}
	repo, store := r.ID, r.StoreID
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		var exitErr *exec.ExitError
		fatal := err != nil && cmd.ProcessState != nil && !errors.As(err, &exitErr)
		return handoverDoneMsg{label: label, repo: repo, store: store, err: err, fatal: fatal}
	})
}

func (m *Model) finishHandover(msg handoverDoneMsg) tea.Cmd {
	m.ops.EndHandover(msg.store)
	if v := m.visit; v != nil {
		m.visit = nil
		if b := m.ops.Batch(); v.batch == b && v.row < len(b.Rows) {
			b.Rows[v.row].RevisitedAt = m.now()
		}
	}
	if msg.fatal {
		m.fatal = fmt.Errorf("restoring the terminal after %s failed: %w", msg.label, msg.err)
		return tea.Quit
	}
	if msg.err != nil {
		m.setError(fmt.Sprintf("%s: %v", msg.label, msg.err))
	} else {
		m.setMessage(msg.label + " finished")
		if msg.label == "git fetch" {
			m.ops.ForgetResults(app.StoreMembers(m.repos, map[domain.StoreID]bool{msg.store: true}))
		}
	}
	m.refresh(app.StoreMembers(m.repos, map[domain.StoreID]bool{msg.store: true}))
	return nil
}
