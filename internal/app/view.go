package app

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mmo-con/headquarters/internal/config"
	"github.com/mmo-con/headquarters/internal/domain"
)

// Filter selects visible repositories.
type Filter struct {
	Text   string
	Family domain.FamilyID // empty means all families
	// Now is the time against which is:stale is evaluated; zero means the
	// current time.
	Now time.Time
}

// StateKeywords lists the supported "is:" filter tokens.
var StateKeywords = []string{
	"is:dirty", "is:clean", "is:behind", "is:ahead", "is:diverged", "is:noupstream",
	"is:error", "is:unavailable", "is:mismatch", "is:marked", "is:branches", "is:unpublished", "is:stashed", "is:stale",
}

const familyPrefix = "family:"

// Matches reports whether r passes the filter. Text tokens are combined with
// AND; "is:" tokens filter by state, "family:" tokens match the family name,
// and other tokens match name or path, all case-insensitively.
func (f Filter) Matches(r *domain.Repository, marked bool) bool {
	if f.Family != "" && r.Family != f.Family {
		return false
	}
	for _, tok := range strings.Fields(strings.ToLower(f.Text)) {
		if strings.HasPrefix(tok, "is:") {
			if !f.matchState(tok, r, marked) {
				return false
			}
			continue
		}
		if name, ok := strings.CutPrefix(tok, familyPrefix); ok {
			if name == "" || !strings.EqualFold(string(r.Family), name) {
				return false
			}
			continue
		}
		if !strings.Contains(strings.ToLower(r.Name), tok) && !strings.Contains(strings.ToLower(r.Path), tok) {
			return false
		}
	}
	return true
}

func (f Filter) matchState(tok string, r *domain.Repository, marked bool) bool {
	w, u := r.Worktree, r.Upstream
	switch tok {
	case "is:dirty":
		return w.Valid && !w.Value.Clean()
	case "is:clean":
		return w.Valid && w.Value.Clean()
	case "is:behind":
		return u.Valid && u.Value.Behind > 0
	case "is:ahead":
		return u.Valid && u.Value.Ahead > 0
	case "is:diverged":
		return u.Valid && u.Value.Diverged()
	case "is:noupstream":
		return u.Valid && (!u.Value.Configured || u.Value.Gone)
	case "is:error":
		return r.EntryError != nil || r.Inspected.Err != nil
	case "is:unavailable":
		return !r.IsAvailable()
	case "is:mismatch":
		return r.RemoteMismatch()
	case "is:marked":
		return marked
	case "is:branches":
		return len(r.Branches.Value) > 0
	case "is:unpublished":
		return r.UnpublishedBranches() > 0
	case "is:stashed":
		return r.Stashes.Valid && r.Stashes.Value > 0
	case "is:stale":
		now := f.Now
		if now.IsZero() {
			now = time.Now()
		}
		return r.FetchStale(now)
	}
	return false
}

type SortKey int

const (
	SortName SortKey = iota
	SortFamily
	SortCommitTime
	SortWorktree
	SortUpstream
	SortHealth
)

var SortKeys = []SortKey{SortName, SortFamily, SortCommitTime, SortWorktree, SortUpstream, SortHealth}

// SortKeyNamed returns the sort order with the name used in [ui].sort;
// config.SortOrders lists the names in the order of SortKeys.
func SortKeyNamed(name string) (SortKey, bool) {
	for i, n := range config.SortOrders {
		if n == name {
			return SortKey(i), true
		}
	}
	return SortName, false
}

func (k SortKey) String() string {
	switch k {
	case SortFamily:
		return "family"
	case SortCommitTime:
		return "latest commit"
	case SortWorktree:
		return "worktree state"
	case SortUpstream:
		return "upstream state"
	case SortHealth:
		return "health"
	}
	return "name"
}

// Sort orders repositories in place. health classifies a repository for
// SortHealth; nil classifies it without operation results. Name and path
// break ties so that the order is stable.
func Sort(repos []*domain.Repository, key SortKey, health func(*domain.Repository) Health) {
	var ranks map[*domain.Repository]int
	if key == SortHealth {
		if health == nil {
			now := time.Now()
			health = func(r *domain.Repository) Health { return HealthOf(r, nil, now) }
		}
		ranks = make(map[*domain.Repository]int, len(repos))
		for _, r := range repos {
			ranks[r] = healthRank(health(r))
		}
	}
	sort.SliceStable(repos, func(i, j int) bool {
		a, b := repos[i], repos[j]
		switch key {
		case SortHealth:
			if ranks[a] != ranks[b] {
				return ranks[a] < ranks[b]
			}
		case SortFamily:
			if a.Family != b.Family {
				return a.Family < b.Family
			}
		case SortCommitTime:
			ta, tb := commitUnix(a), commitUnix(b)
			if ta != tb {
				return ta > tb
			}
		case SortWorktree:
			ra, rb := worktreeRank(a), worktreeRank(b)
			if ra != rb {
				return ra < rb
			}
		case SortUpstream:
			ra, rb := upstreamRank(a), upstreamRank(b)
			if ra != rb {
				return ra < rb
			}
		}
		na, nb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if na != nb {
			return na < nb
		}
		return a.Path < b.Path
	})
}

func commitUnix(r *domain.Repository) int64 {
	if r.LastCommit.Valid && r.LastCommit.Value != nil {
		return r.LastCommit.Value.Time.Unix()
	}
	return 0
}

// worktreeRank orders problems first: errors, conflicts, operations in
// progress, local changes, untracked files, clean.
func worktreeRank(r *domain.Repository) int {
	switch {
	case !r.IsAvailable() || !r.Worktree.Valid:
		return 0
	case r.Worktree.Value.Conflicts > 0:
		return 1
	case r.Operation.Valid && r.Operation.Value != domain.NoOperation:
		return 2
	case r.Worktree.Value.HasTrackedChanges():
		return 3
	case r.Worktree.Value.Untracked > 0:
		return 4
	}
	return 5
}

// upstreamRank orders diverged, behind, ahead, missing upstream, up to date.
func upstreamRank(r *domain.Repository) int {
	if !r.IsAvailable() || !r.Upstream.Valid {
		return 0
	}
	u := r.Upstream.Value
	switch {
	case u.Diverged():
		return 1
	case u.Behind > 0:
		return 2
	case u.Ahead > 0:
		return 3
	case !u.Configured || u.Gone:
		return 4
	}
	return 5
}

// ValidateFilter reports the first token of text that cannot match: an
// unknown "is:" token, "family:" without a name, or "is:marked" in a
// frontend without marks.
func ValidateFilter(text string, marks bool) error {
	for _, tok := range strings.Fields(strings.ToLower(text)) {
		switch {
		case tok == "is:marked" && !marks:
			return fmt.Errorf("%s: repositories cannot be marked here", tok)
		case strings.HasPrefix(tok, "is:") && !slices.Contains(StateKeywords, tok):
			return fmt.Errorf("unknown state %q; use one of %s", tok, strings.Join(StateKeywords, ", "))
		case tok == familyPrefix:
			return fmt.Errorf("%s needs a family name", tok)
		}
	}
	return nil
}
