package git

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/mmo-con/headquarters/internal/domain"
)

// Status is the parsed output of "git status --porcelain=v2 --branch -z".
type Status struct {
	Head     domain.HeadState
	Upstream domain.UpstreamState
	Worktree domain.WorktreeState
	// Stashes is the "# stash" header, which Git prints with --show-stash
	// only if there are stash entries.
	Stashes int
}

// ParseStatus parses NUL-delimited porcelain v2 output. Unknown header lines
// are ignored as required by Git's documentation; unknown entry types are a
// compatibility error.
func ParseStatus(out []byte, untrackedChecked bool) (Status, error) {
	var st Status
	st.Worktree.UntrackedChecked = untrackedChecked
	hasAB := false
	records := bytes.Split(out, []byte{0})
	for i := 0; i < len(records); i++ {
		rec := string(records[i])
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			if err := parseHeader(rec, &st, &hasAB); err != nil {
				return Status{}, err
			}
		case '1':
			xy, err := field(rec, 1)
			if err != nil {
				return Status{}, err
			}
			sub, err := field(rec, 2)
			if err != nil {
				return Status{}, err
			}
			countChange(&st.Worktree, xy)
			// A submodule whose only change is its checked-out commit.
			if xy == ".M" && sub == "SC.." {
				st.Worktree.SubmoduleCommits++
			}
		case '2':
			xy, err := field(rec, 1)
			if err != nil {
				return Status{}, err
			}
			countChange(&st.Worktree, xy)
			st.Worktree.Renamed++
			i++ // the original path follows as a separate record
		case 'u':
			st.Worktree.Conflicts++
		case '?':
			st.Worktree.Untracked++
		case '!':
		default:
			return Status{}, fmt.Errorf("unsupported git status record type %q", rec[:1])
		}
	}
	if st.Upstream.Configured && !hasAB {
		st.Upstream.Gone = true
	}
	return st, nil
}

func parseHeader(rec string, st *Status, hasAB *bool) error {
	key, value, _ := strings.Cut(strings.TrimPrefix(rec, "# "), " ")
	switch key {
	case "branch.oid":
		if value == "(initial)" {
			st.Head.Unborn = true
		} else {
			st.Head.CommitID = value
		}
	case "branch.head":
		if value == "(detached)" {
			st.Head.Detached = true
		} else {
			st.Head.Branch = value
		}
	case "branch.upstream":
		st.Upstream.Configured = true
		st.Upstream.Ref = value
	case "branch.ab":
		*hasAB = true
		var a, b int
		parts := strings.Fields(value)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "+") || !strings.HasPrefix(parts[1], "-") {
			return fmt.Errorf("malformed branch.ab header %q", value)
		}
		var err error
		if a, err = strconv.Atoi(parts[0][1:]); err != nil {
			return fmt.Errorf("malformed branch.ab header %q", value)
		}
		if b, err = strconv.Atoi(parts[1][1:]); err != nil {
			return fmt.Errorf("malformed branch.ab header %q", value)
		}
		st.Upstream.Ahead, st.Upstream.Behind = a, b
	case "stash":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("malformed stash header %q", value)
		}
		st.Stashes = n
	}
	return nil
}

func field(rec string, n int) (string, error) {
	parts := strings.SplitN(rec, " ", n+2)
	if len(parts) <= n {
		return "", fmt.Errorf("malformed git status record %q", rec)
	}
	return parts[n], nil
}

func countChange(w *domain.WorktreeState, xy string) {
	if len(xy) != 2 {
		return
	}
	x, y := xy[0], xy[1]
	if x != '.' {
		w.Staged++
	}
	switch y {
	case 'M', 'T':
		w.Modified++
	case 'D':
		w.Deleted++
	}
}
