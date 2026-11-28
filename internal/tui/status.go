package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/mmo-con/headquarters/internal/app"
	"github.com/mmo-con/headquarters/internal/domain"
)

func loaded(r *domain.Repository) bool {
	return !r.Inspected.FinishedAt.IsZero()
}

// stateSegs describes what the count columns cannot show: unavailable
// entries, loading and running operations, unknown state, conflicts and
// operations in progress, a missing or gone upstream, a remote mismatch, a
// stale fetch, and other branches. Divergence is shown by the ahead and
// behind counts in the error color and by the health symbol. It is empty for
// a repository without findings. Every symbol carries a count or text, so
// color is never the only carrier of information.
func (m *Model) stateSegs(r *domain.Repository) []seg {
	t, ic := m.theme, m.icons
	if !r.IsAvailable() {
		return []seg{{ic.unavailable + " " + r.Availability.String(), t.err}}
	}
	var out []seg
	add := func(ss ...seg) {
		if len(ss) == 0 {
			return
		}
		if len(out) > 0 {
			out = append(out, seg{"  ", t.plain})
		}
		out = append(out, ss...)
	}
	if op, status := m.runningOp(r); op != "" {
		if status == domain.StatusRunning {
			verb := map[domain.OperationType]string{domain.OperationFetch: "fetching", domain.OperationUpdate: "updating"}[op]
			add(seg{m.spinnerFrame() + " " + verb, t.key})
		} else {
			add(seg{ic.queued + " queued", t.dim})
		}
	} else if last := m.lastOp(r.ID); last != nil && last.Operation == domain.OperationFetch && last.Status == domain.StatusFailed {
		add(seg{ic.failed + " fetch failed", t.err})
	}
	if !loaded(r) {
		if len(out) == 0 {
			add(seg{m.spinnerFrame(), t.dim})
		}
		return out
	}
	if !r.Worktree.Valid || !r.Head.Valid || !r.Upstream.Valid {
		add(seg{"? unknown", t.warn})
	}
	if r.Operation.Valid && r.Operation.Value != domain.NoOperation {
		add(seg{r.Operation.Value.String(), t.err.Bold(true)})
	}
	if r.Worktree.Valid && r.Worktree.Value.Conflicts > 0 {
		add(seg{fmt.Sprintf("%s%d conflicts", ic.conflict, r.Worktree.Value.Conflicts), t.err})
	}
	if r.Head.Valid && r.Upstream.Valid {
		h, u := r.Head.Value, r.Upstream.Value
		switch {
		case h.Unborn:
			add(seg{"no commits", t.dim})
		case h.Detached:
			add(seg{ic.detached + " detached", t.warn})
		case !u.Configured:
			add(seg{ic.noUpstream + " no upstream", t.dim})
		case u.Gone:
			add(seg{ic.noUpstream + " upstream gone", t.warn})
		}
	}
	if r.RemoteMismatch() {
		add(seg{ic.mismatch + "remote", t.warn})
	}
	if f := m.fetchFinding(r); f != "" {
		add(seg{f, t.warn})
	}
	add(m.branchesMarker(r)...)
	return out
}

// countSegs returns the cells of the count columns: staged, modified
// (including deletions in the worktree), untracked, stash entries, ahead, and
// behind; local work comes before the relation to the upstream. "–" is a known zero and "?" an unknown value. A cell stays empty
// while the repository loads and where the count does not apply, such as
// ahead and behind without an upstream; the state column names the reason.
func (m *Model) countSegs(r *domain.Repository) [][]seg {
	t := m.theme
	out := make([][]seg, colBehind-colStaged+1)
	if !r.IsAvailable() || !loaded(r) {
		return out
	}
	num := func(n int, style lipgloss.Style) []seg {
		if n == 0 {
			return []seg{{"–", t.dim}}
		}
		return []seg{{strconv.Itoa(n), style}}
	}
	unknown := []seg{{"?", t.warn}}
	out[3] = unknown
	if r.Stashes.Valid {
		out[3] = num(r.Stashes.Value, t.warn)
	}
	if r.Worktree.Valid {
		w := r.Worktree.Value
		out[0] = num(w.Staged, t.ahead)
		out[1] = num(w.Modified+w.Deleted, t.dirty)
		out[2] = num(w.Untracked, t.dim)
		if !w.UntrackedChecked {
			out[2] = []seg{{"off", t.dim}}
		}
	} else {
		out[0], out[1], out[2] = unknown, unknown, unknown
	}
	if !r.Head.Valid || !r.Upstream.Valid {
		out[4], out[5] = unknown, unknown
		return out
	}
	h, u := r.Head.Value, r.Upstream.Value
	if h.Unborn || h.Detached || !u.Configured || u.Gone {
		return out
	}
	ahead, behind := t.ahead, t.behind
	if u.Diverged() {
		ahead, behind = t.err, t.err
	}
	out[4], out[5] = num(u.Ahead, ahead), num(u.Behind, behind)
	return out
}

// runningOp returns the operation that is queued or running for r's store.
func (m *Model) runningOp(r *domain.Repository) (domain.OperationType, domain.OperationStatus) {
	b := m.ops.Batch()
	if b == nil || r.StoreID == "" {
		return "", ""
	}
	for _, row := range b.Rows {
		if row.Job.StoreID == r.StoreID && !row.Result.Status.Final() {
			return row.Job.Operation, row.Result.Status
		}
	}
	return "", ""
}

func (m *Model) upstreamSegs(r *domain.Repository) []seg {
	t, ic := m.theme, m.icons
	if op, status := m.runningOp(r); op != "" {
		if status == domain.StatusRunning {
			verb := map[domain.OperationType]string{domain.OperationFetch: "fetching", domain.OperationUpdate: "updating"}[op]
			return []seg{{m.spinnerFrame() + " " + verb, t.key}}
		}
		return []seg{{ic.queued + " queued", t.dim}}
	}
	if last := m.lastOp(r.ID); last != nil && last.Operation == domain.OperationFetch && last.Status == domain.StatusFailed {
		return []seg{{ic.failed + " fetch failed", t.err}}
	}
	if !r.IsAvailable() {
		return []seg{{"–", t.dim}}
	}
	if !loaded(r) {
		return []seg{{m.spinnerFrame(), t.dim}}
	}
	if !r.Head.Valid || !r.Upstream.Valid {
		return []seg{{"? unknown", t.warn}}
	}
	h, u := r.Head.Value, r.Upstream.Value
	switch {
	case h.Unborn:
		return []seg{{"no commits", t.dim}}
	case h.Detached:
		return []seg{{ic.detached + " detached", t.warn}}
	case !u.Configured:
		return []seg{{ic.noUpstream + " no upstream", t.dim}}
	case u.Gone:
		return []seg{{ic.noUpstream + " upstream gone", t.warn}}
	case u.Diverged():
		return []seg{{fmt.Sprintf("%s%d %s%d diverged", ic.ahead, u.Ahead, ic.behind, u.Behind), t.err}}
	case u.Behind > 0:
		return []seg{{fmt.Sprintf("%s%d", ic.behind, u.Behind), t.behind}}
	case u.Ahead > 0:
		return []seg{{fmt.Sprintf("%s%d", ic.ahead, u.Ahead), t.ahead}}
	}
	return []seg{{ic.upToDate, t.clean}}
}

// branchesMarker counts the other local branches that need attention. It is
// highlighted if any of them holds unpublished work.
func (m *Model) branchesMarker(r *domain.Repository) []seg {
	n := len(r.Branches.Value)
	if !r.IsAvailable() || n == 0 {
		return nil
	}
	style := m.theme.behind
	if r.UnpublishedBranches() > 0 {
		style = m.theme.warn
	}
	return []seg{{fmt.Sprintf("%s%d", m.icons.branches, n), style}}
}

// healthLevel is the health classification of the application (see
// app.HealthOf).
type healthLevel = app.Health

const (
	healthUnknown   = app.HealthUnknown
	healthError     = app.HealthError
	healthCritical  = app.HealthCritical
	healthDirty     = app.HealthDirty
	healthBehind    = app.HealthBehind
	healthAhead     = app.HealthAhead
	healthAttention = app.HealthAttention
	healthOK        = app.HealthOK
)

func (m *Model) healthOf(r *domain.Repository) healthLevel { return m.ops.Health(r, m.now()) }

func stashed(r *domain.Repository) bool { return r.Stashes.Valid && r.Stashes.Value > 0 }

// healthSeg returns the symbol of a health level, and its label for family
// summaries. Other attention is highlighted as a warning only if it involves
// unpublished work (including stash entries), a remote mismatch, a detached
// HEAD, a gone upstream, or a stale fetch.
func (m *Model) healthSeg(l healthLevel, warn bool) (s seg, label string) {
	t, ic := m.theme, m.icons
	switch l {
	case healthError:
		return seg{ic.failed, t.err}, "error"
	case healthCritical:
		return seg{ic.conflict, t.err}, "critical"
	case healthDirty:
		return seg{ic.modified, t.dirty}, "dirty"
	case healthBehind:
		return seg{ic.behind, t.behind}, "behind"
	case healthAhead:
		return seg{ic.ahead, t.ahead}, "ahead"
	case healthAttention:
		if warn {
			return seg{ic.attention, t.warn}, "attention"
		}
		return seg{ic.attention, t.behind}, "attention"
	case healthOK:
		return seg{ic.clean, t.clean}, "healthy"
	}
	return seg{"", t.dim}, ""
}

// health returns the symbol of the health column; healthy is true for an
// available, inspected repository without findings.
func (m *Model) health(r *domain.Repository) (s seg, healthy bool) {
	l := m.healthOf(r)
	warn := r.UnpublishedBranches() > 0 || r.RemoteMismatch() || stashed(r) || m.fetchFinding(r) != "" ||
		(r.Head.Valid && r.Head.Value.Detached) || (r.Upstream.Valid && r.Upstream.Value.Gone)
	s, _ = m.healthSeg(l, warn)
	return s, l == healthOK
}

// familyHealth returns the health levels of the repositories that match the
// filter, by family.
func (m *Model) familyHealth() map[domain.FamilyID][]healthLevel {
	out := map[domain.FamilyID][]healthLevel{}
	for i := range m.repos {
		r := &m.repos[i]
		if m.matches(m.filter, r, m.marks[r.ID]) {
			out[r.Family] = append(out[r.Family], m.healthOf(r))
		}
	}
	return out
}

// familySummary counts the repositories of a family by health level and
// lists the levels that need attention, most important first. A healthy
// family shows only the healthy symbol.
func (m *Model) familySummary(levels []healthLevel) []seg {
	counts := map[healthLevel]int{}
	for _, l := range levels {
		counts[l]++
	}
	var out []seg
	for l := healthError; l < healthOK; l++ {
		if counts[l] == 0 {
			continue
		}
		s, label := m.healthSeg(l, true)
		if len(out) > 0 {
			out = append(out, seg{"  ", m.theme.plain})
		}
		s.text = fmt.Sprintf("%s%d %s", s.text, counts[l], label)
		out = append(out, s)
	}
	if len(out) == 0 && counts[healthOK] == len(levels) && len(levels) > 0 {
		s, _ := m.healthSeg(healthOK, false)
		out = append(out, s)
	}
	return out
}

// maxBranchLines limits the other branches listed in the detail panel.
const maxBranchLines = 10

// otherBranchLines lists the other local branches that need attention, one
// line per branch; the detail panel limits how many are shown.
func (m *Model) otherBranchLines(r *domain.Repository) []string {
	t, ic := m.theme, m.icons
	now := m.now()
	var out []string
	for _, b := range r.Branches.Value {
		var state seg
		switch b.Reason() {
		case domain.BranchMissing:
			state = seg{ic.unavailable + " missing", t.warn}
		case domain.BranchNoUpstream:
			state = seg{ic.noUpstream + " no upstream", t.warn}
		case domain.BranchGone:
			state = seg{ic.noUpstream + " upstream gone", t.warn}
		case domain.BranchDiverged:
			state = seg{fmt.Sprintf("%s%d %s%d diverged", ic.ahead, b.Ahead, ic.behind, b.Behind), t.err}
		case domain.BranchAhead:
			state = seg{fmt.Sprintf("%s%d", ic.ahead, b.Ahead), t.ahead}
		default:
			state = seg{fmt.Sprintf("%s%d", ic.behind, b.Behind), t.behind}
		}
		l := []seg{{"  " + ic.branch + b.Name + "  ", t.branch}, state}
		if !b.Missing {
			l = append(l, seg{" · " + ago(now, b.CommitTime), t.dim})
		}
		out = append(out, line(l...))
	}
	return out
}

// branchSegs names the checked-out branch, or the commit of a detached HEAD.
// The table column is titled, so only the detail panel adds the branch
// symbol.
func (m *Model) branchSegs(r *domain.Repository, symbol bool) []seg {
	if !r.IsAvailable() || !r.Head.Valid {
		return []seg{{"–", m.theme.dim}}
	}
	prefix := ""
	if symbol {
		prefix = m.icons.branch
	}
	h := r.Head.Value
	if h.Detached {
		id := "detached"
		if len(h.CommitID) >= 7 {
			id = h.CommitID[:7]
		}
		return []seg{{prefix + "(" + id + ")", m.theme.warn}}
	}
	return []seg{{prefix + h.Branch, m.theme.branch}}
}

func (m *Model) stateText(r *domain.Repository) string    { return plain(m.stateSegs(r)) }
func (m *Model) upstreamText(r *domain.Repository) string { return plain(m.upstreamSegs(r)) }

func (m *Model) spinnerFrame() string {
	f := m.icons.spinner
	return f[m.frame%len(f)]
}

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

// commitAge is the age of a commit in the table and commit lists: "now", minutes, hours up to
// two days, days up to two months, then months and years.
func commitAge(now, t time.Time) string {
	d := now.Sub(t)
	days := int(d.Hours() / 24)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	case days < 60:
		return fmt.Sprintf("%d d", days)
	case days < 730:
		return fmt.Sprintf("%d mo", days/30)
	}
	return fmt.Sprintf("%d y", days/365)
}

// followUp describes the current state of a row of a finished update batch
// that was not updated. A row that became eligible or up to date is always
// reported; a row that is still blocked only after the repository was
// inspected again following a lazygit session opened from the row.
func (m *Model) followUp(row app.BatchRow) (text string, resolved bool) {
	if b := m.ops.Batch(); b == nil || b.Op != domain.OperationUpdate || !b.Done() ||
		row.Result.Status == domain.StatusSucceeded {
		return "", false
	}
	r := m.repo(row.Job.RepositoryID)
	if r == nil || !r.Inspected.FinishedAt.After(row.Result.FinishedAt) {
		return "", false
	}
	switch v := app.Eligibility(*r, m.ops.StoreBusy(r.StoreID)); {
	case v.Eligible():
		return "now eligible", true
	case v.UpToDate():
		return "now up to date", true
	case !row.RevisitedAt.IsZero() && r.Inspected.FinishedAt.After(row.RevisitedAt):
		return "still blocked: " + v.String(), false
	}
	return "", false
}

// fetchSeg describes the last fetch; old or missing fetches are highlighted.
func (m *Model) fetchSeg(now time.Time, r *domain.Repository) seg {
	s := m.fetchAge(now, r)
	switch {
	case !r.LastFetch.Valid:
		s.text = "fetch time unknown"
	case r.LastFetch.Value == nil:
		s.text = "never fetched"
	default:
		s.text = "fetched " + s.text
	}
	return s
}

// fetchAge is the age of the last fetch for the labeled detail line; old or
// missing fetches are highlighted.
func (m *Model) fetchAge(now time.Time, r *domain.Repository) seg {
	switch {
	case !r.LastFetch.Valid:
		return seg{"unknown", m.theme.dim}
	case r.LastFetch.Value == nil:
		return seg{"never", m.theme.warn}
	case now.Sub(*r.LastFetch.Value) > domain.StaleFetchAge:
		return seg{ago(now, *r.LastFetch.Value), m.theme.warn}
	}
	return seg{ago(now, *r.LastFetch.Value), m.theme.dim}
}

// fetchFinding names a stale fetch (see domain.Repository.FetchStale); it is
// empty otherwise.
func (m *Model) fetchFinding(r *domain.Repository) string {
	switch {
	case !r.FetchStale(m.now()):
		return ""
	case r.LastFetch.Value == nil:
		return "never fetched"
	}
	return "stale fetch"
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
