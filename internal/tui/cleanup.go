package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/jobs"
)

// cleanupPreview is the confirmation of deleting merged branches. It opens
// at once and reads the branches of every target store in the background; it
// can be confirmed only after every read is finished.
type cleanupPreview struct {
	// hidden counts the marked targets hidden from the list.
	hidden int
	batch  uint64
	// read holds one target per store; plans holds their rows in the same
	// order once they are read.
	read    []domain.Repository
	plans   [][]app.CleanupCandidate
	done    []bool
	pending int
	blocked []app.CleanupCandidate
}

// rows lists the deletable branches first, then the skipped branches and
// repositories, each in target order.
func (p *cleanupPreview) rows() []app.CleanupCandidate {
	var del, skip []app.CleanupCandidate
	for _, plan := range p.plans {
		for _, c := range plan {
			if c.Deletable() {
				del = append(del, c)
			} else {
				skip = append(skip, c)
			}
		}
	}
	return append(append(del, skip...), p.blocked...)
}

// cleanupLoading reports whether the preview of deleting merged branches is
// still reading branches.
func (m *Model) cleanupLoading() bool { return m.cleanup != nil && m.cleanup.pending > 0 }

// openCleanupPreview opens the confirmation of deleting the merged branches
// of targets and starts reading their branches.
func (m *Model) openCleanupPreview(targets []domain.Repository, hidden int) {
	if len(targets) == 0 {
		m.setMessage("no repository selected")
		return
	}
	read, blocked := app.CleanupTargets(m.repos, targets)
	p := &cleanupPreview{hidden: hidden, read: read, blocked: blocked,
		plans: make([][]app.CleanupCandidate, len(read)), done: make([]bool, len(read)), pending: len(read)}
	if len(read) > 0 {
		js := make([]jobs.Job, len(read))
		for i, r := range read {
			js[i] = app.CleanupReadJob(m.app.Git, r, m.app.Config.Git.InspectionTimeout)
		}
		p.batch = m.submit(js, m.applyCleanupRead)
	}
	m.cleanup = p
	m.mode, m.overlayIdx = modeCleanupPreview, 0
}

// applyCleanupRead plans the branches of one store of the open preview.
func (m *Model) applyCleanupRead(ev jobs.Event, _ map[domain.RepositoryID]bool) {
	p := m.cleanup
	if p == nil || ev.BatchID != p.batch || !ev.Result.Status.Final() || ev.JobID >= len(p.read) || p.done[ev.JobID] {
		return
	}
	r := p.read[ev.JobID]
	p.done[ev.JobID] = true
	p.pending--
	if plan, ok := ev.Payload.([]app.CleanupCandidate); ok && ev.Result.Error == nil {
		p.plans[ev.JobID] = plan
	} else {
		reason := "reading branches failed"
		if ev.Result.Error != nil {
			reason += ": " + firstLine(ev.Result.Error.Error())
		}
		p.plans[ev.JobID] = []app.CleanupCandidate{{Repository: r, Reason: reason}}
	}
	if p.pending == 0 {
		delete(m.routes, ev.BatchID)
	}
}

// closeCleanup closes the confirmation and stops reading branches.
func (m *Model) closeCleanup() {
	if m.cleanupLoading() {
		m.forget(m.cleanup.batch)
	}
	m.mode = modeNormal
	m.cleanup = nil
}

func (m *Model) keyCleanupPreview(key string) tea.Cmd {
	if m.moveOverlay(key, len(m.cleanup.rows())) {
		return nil
	}
	switch key {
	case "enter", "y":
		if m.cleanupLoading() {
			m.setMessage("still reading branches")
			return nil
		}
		n, started := m.ops.ConfirmCleanup(m.repos, m.cleanup.rows())
		m.notify(n)
		if n.Error {
			return nil
		}
		m.closeCleanup()
		if started {
			m.showBatch(true)
		}
	case "esc", "n", "q":
		m.closeCleanup()
	}
	return nil
}

// viewCleanupPreview confirms deleting merged branches.
func (m *Model) viewCleanupPreview(iw, rows int) (string, []string) {
	t, p := m.theme, m.cleanup
	var list []string
	deletable, repos := 0, map[domain.RepositoryID]bool{}
	for i, c := range p.rows() {
		name := runewidth.FillRight(runewidth.Truncate(domain.Printable(c.Repository.Name), 24, "…"), 24)
		branch := runewidth.Truncate(domain.Printable(c.Branch), 32, "…")
		text, style := fmt.Sprintf("%s delete  %s %s", m.icons.failed, name, branch), t.key
		if c.Deletable() {
			deletable++
			repos[c.Repository.ID] = true
		} else {
			if branch != "" {
				branch += "  "
			}
			text, style = fmt.Sprintf("– keep    %s %s%s", name, branch, c.Reason), t.warn
		}
		list = append(list, m.selectable(text, i == m.overlayIdx, iw, style))
	}
	var head []string
	switch {
	case p.pending > 0:
		head = append(head, t.bold.Render(fmt.Sprintf("%s Reading the branches of %d repositories…",
			m.spinnerFrame(), len(p.read))))
	case deletable == 0:
		head = append(head, t.bold.Render("No merged branches to delete."))
	default:
		head = append(head, t.bold.Render(fmt.Sprintf("Delete %d merged %s in %d %s?",
			deletable, plural(deletable, "branch", "branches"), len(repos), plural(len(repos), "repository", "repositories"))))
	}
	head = append(head,
		t.dim.Render("Deletes local branches whose changes are all in the default branch,"),
		t.dim.Render("also after squash and rebase merges; remote branches stay untouched."),
		t.dim.Render("Branches that are kept are listed with the reason."),
	)
	if n := notFetchedSince(p.read, m.started); n > 0 {
		head = append(head,
			t.warn.Render(fmt.Sprintf("%d %s not fetched since start; press f first to find", n,
				plural(n, "repository was", "repositories were"))),
			t.warn.Render("branches deleted on the server."))
	}
	if p.hidden > 0 {
		head = append(head, t.warn.Render(m.hiddenMarksNotice(p.hidden)))
	}
	if len(list) > 0 {
		head = append(head, "")
	}
	return "Delete merged branches", append(head, window(list, m.overlayIdx, min(len(list), max(rows-len(head), 3)))...)
}

// notFetchedSince counts the repositories with remotes whose store was not
// fetched since start. The preview reads local refs only, so a branch
// deleted on the server counts as gone only after a fetch with --prune.
func notFetchedSince(repos []domain.Repository, start time.Time) int {
	n := 0
	for _, r := range repos {
		if !r.Remotes.Valid || len(r.Remotes.Value) == 0 || !r.LastFetch.Valid {
			continue
		}
		if f := r.LastFetch.Value; f == nil || f.Before(start) {
			n++
		}
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
