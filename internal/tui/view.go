package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/domain"
)

// wideLayout is the minimum width for table and details side by side.
const wideLayout = 130

// maxDetailWidth limits the detail panel beside the table, which gets the
// remaining width for its columns.
const maxDetailWidth = 70

func (m *Model) View() string {
	main := m.mainArea(max(m.height-2, 4))
	if modal := m.modal(len(main)); modal != nil {
		main = m.overlay(main, modal)
	}
	lines := append([]string{m.headerLine()}, main...)
	lines = append(lines, m.footerLine())
	return strings.Join(lines, "\n")
}

// mainArea returns the repository and detail panels for h lines. Beside the
// table, the detail panel takes 40 % of the width and at most
// maxDetailWidth columns; if the table needs less than the rest, the panel
// moves left to the table and takes the remaining width.
func (m *Model) mainArea(h int) []string {
	active := m.mode == modeNormal || m.mode == modeFilter
	t := m.measureTable()
	if m.width >= wideLayout {
		dw := min(m.width*40/100, maxDetailWidth)
		if need := m.tableNeed(t); need < m.width-dw {
			dw = m.width - need
		}
		rw := m.width - dw
		return joinColumns(m.reposBox(t, rw, h, active), m.detailsBox(dw, h))
	}
	dh := stackedDetailsHeight(h)
	return append(m.reposBox(t, m.width, h-dh, active), m.detailsBox(m.width, dh)...)
}

func stackedDetailsHeight(h int) int {
	if h < 20 {
		return max(h/3, 4)
	}
	return 9
}

// tableRows is the number of repository rows that fit into the table.
func (m *Model) tableRows() int {
	h := max(m.height-2, 4)
	if m.width < wideLayout {
		h -= stackedDetailsHeight(h)
	}
	return max(h-3, 1)
}

func (m *Model) pageSize() int { return max(m.tableRows()-1, 1) }

// quickFilter is a header counter that doubles as a filter shortcut.
type quickFilter struct {
	key, token, label string
	style             lipgloss.Style
}

func (m *Model) quickFilters() []quickFilter {
	t := m.theme
	return []quickFilter{
		{"1", "is:dirty", "dirty", t.dirty},
		{"2", "is:behind", "behind", t.behind},
		{"3", "is:ahead", "ahead", t.ahead},
		{"4", "is:diverged", "diverged", t.err},
		{"5", "is:error", "errors", t.err},
		{"6", "is:branches", "branches", t.warn},
		{"7", "is:stashed", "stashed", t.warn},
		{"8", "is:stale", "stale", t.warn},
	}
}

func (m *Model) headerLine() string {
	t := m.theme
	left := t.title.Render(" headquarters ")
	scope := app.Filter{Family: m.filter.Family}
	for _, q := range m.quickFilters() {
		n := 0
		f := app.Filter{Family: scope.Family, Text: q.token}
		for i := range m.repos {
			if m.matches(f, &m.repos[i], false) {
				n++
			}
		}
		label := fmt.Sprintf("%s %s %d", q.key, q.label, n)
		open, end := " ", " "
		active := strings.TrimSpace(m.filter.Text) == q.token
		if active {
			open, end = "[", "]"
		}
		left += " " + chipStyle(t, q.style, n, active).Render(open+label+end)
	}

	var right string
	switch {
	case m.loadingInventory:
		right = t.key.Render(m.spinnerFrame()) + " loading inventory"
	case m.ops.Batch() != nil && !m.ops.Batch().Done():
		b, done := m.ops.Batch(), 0
		for _, r := range b.Rows {
			if r.Result.Status.Final() {
				done++
			}
		}
		right = t.key.Render(m.spinnerFrame()) + " " + string(b.Op) + " " +
			m.progressBar(done, len(b.Rows), 12) + fmt.Sprintf(" %d/%d", done, len(b.Rows))
	case m.pendingInspect() > 0:
		right = t.key.Render(m.spinnerFrame()) + fmt.Sprintf(" inspecting %d", m.pendingInspect())
	}
	right += " "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return fitANSI(left, m.width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// chipStyle styles a header counter: colored text if it has matches, dim
// otherwise. Only the active quick filter has a background; the caller also
// brackets it, so it is recognizable without color.
func chipStyle(t theme, style lipgloss.Style, n int, active bool) lipgloss.Style {
	chip := style
	if n == 0 {
		chip = t.dim
	}
	return chip.Reverse(active)
}

func (m *Model) hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, m.theme.key.Render(pairs[i])+" "+m.theme.dim.Render(pairs[i+1]))
	}
	return strings.Join(parts, m.theme.dim.Render(" │ "))
}

func (m *Model) footerLine() string {
	var left string
	switch m.mode {
	case modeFilter:
		left = m.theme.key.Render(" /") + domain.Printable(m.filter.Text) + m.theme.key.Render("█") + "  " +
			m.hints("Enter", "apply", "Esc", "clear", "Ctrl+U", "erase")
	case modeFamily:
		left = " " + m.hints("↑/↓", "move", "Enter", "choose", "Esc", "cancel", "Ctrl+U", "erase search")
	case modeOperations:
		left = " " + m.hints(m.operationHints()...)
	case modeUpdatePreview:
		left = " " + m.hints("Enter/y", "fetch and update", "j/k", "move", "Esc/n", "cancel")
	case modeCleanupPreview:
		left = " " + m.hints("Enter/y", "delete", "j/k", "move", "Esc/n", "cancel")
	case modeConfirmQuit:
		left = " " + m.hints("Enter/y", "quit", "Esc/n", "keep running")
	case modeSort:
		left = " " + m.hints("j/k", "move", "Enter", "choose", "n…h", "sort at once", "Esc", "close")
	case modeLaunchers, modeCopy:
		left = " " + m.hints("j/k", "move", "Enter", "choose", "Esc", "close")
	case modeError:
		left = " " + m.hints("t", "fetch in terminal", "Esc", "close")
	case modeHelp:
		left = " " + m.hints("j/k", "scroll", "any other key", "close")
	default:
		left = " " + m.hints(m.normalHints()...)
	}
	if m.message == "" {
		return fitANSI(left, m.width)
	}
	style, icon := m.theme.success, m.icons.clean
	if m.messageErr {
		style, icon = m.theme.err, m.icons.failed
	}
	msg := " " + icon + " " + m.message + " "
	maxMsg := max(m.width/2, 20)
	if ansi.StringWidth(msg) > maxMsg {
		msg = ansi.Truncate(msg, maxMsg, "…")
	}
	right := style.Reverse(true).Render(msg)
	lw := m.width - ansi.StringWidth(right)
	return fitANSI(left, lw) + right
}

// normalHints lists the most important actions for the current row and
// selection; all others are in the help view.
func (m *Model) normalHints() []string {
	var pairs []string
	if row := m.currentRow(); row.count > 0 {
		action := "expand"
		if m.isExpanded(row.family) {
			action = "collapse"
		}
		pairs = append(pairs, "Enter", action)
	} else if m.current() != nil {
		pairs = append(pairs, "Enter", "lazygit", "y", "copy")
	}
	if len(m.marks) > 0 {
		pairs = append(pairs, "f", "fetch marked", "u", "update marked", "A", "clear marks")
	} else {
		pairs = append(pairs, "f", "fetch", "F", "fetch shown", "u", "update", "U", "update shown")
	}
	pairs = append(pairs, "/", "filter")
	if m.filter.Text != "" {
		pairs = append(pairs, "Esc", "clear filter")
	}
	return append(pairs, "?", "all keys")
}

// operationHints lists the actions that apply to the batch and the selected
// row of the operations view.
func (m *Model) operationHints() []string {
	pairs := []string{"j/k", "move", "Enter", "lazygit"}
	if b := m.ops.Batch(); b != nil {
		failed := false
		for _, r := range b.Rows {
			failed = failed || r.Result.Status == domain.StatusFailed || r.Result.Status == domain.StatusCancelled
		}
		switch {
		case !b.Done() || m.ops.UpdatePending():
			pairs = append(pairs, "c", "cancel")
		case failed:
			pairs = append(pairs, "x", "retry failed")
		}
		if b.Done() && b.Op == domain.OperationUpdate {
			pairs = append(pairs, "u", "continue")
		}
		if m.opSelected < len(b.Rows) {
			if row := b.Rows[m.opSelected]; row.Job.Operation == domain.OperationFetch && row.Result.Status == domain.StatusFailed {
				pairs = append(pairs, "t", "retry in terminal")
			}
		}
	}
	return append(pairs, "Esc", "close")
}
