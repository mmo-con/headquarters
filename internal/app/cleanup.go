package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mmo-con/headquarters/internal/domain"
	"github.com/mmo-con/headquarters/internal/git"
	"github.com/mmo-con/headquarters/internal/jobs"
)

// CleanupCandidate is one row of the preview of deleting merged branches: a
// branch that is deleted, or a branch or repository that is skipped with a
// reason.
type CleanupCandidate struct {
	Repository domain.Repository
	// Branch is empty for a repository that cannot be planned at all.
	Branch   string
	CommitID string
	Reason   string
	// RefDelete deletes the branch with update-ref at CommitID instead of
	// git branch -d, whose merge check would refuse it.
	RefDelete bool
}

// Deletable reports whether the branch is deleted.
func (c CleanupCandidate) Deletable() bool { return c.Branch != "" && c.Reason == "" }

// CleanupTargets returns one available target per store, which reads the
// branches of its store, and blocked rows for the other targets that cannot
// be planned.
func CleanupTargets(all, targets []domain.Repository) (read []domain.Repository, blocked []CleanupCandidate) {
	// Branches belong to the store, so a branch pinned by any of its entries
	// is kept.
	pinned := map[domain.StoreID][]string{}
	for _, r := range all {
		for _, name := range r.BranchPolicy.Pinned {
			if !slices.Contains(pinned[r.StoreID], name) {
				pinned[r.StoreID] = append(pinned[r.StoreID], name)
			}
		}
	}
	seen := map[domain.StoreID]bool{}
	for _, r := range targets {
		switch {
		case !r.IsAvailable():
			blocked = append(blocked, CleanupCandidate{Repository: r, Reason: "repository is " + r.Availability.String()})
		case !seen[r.StoreID]:
			seen[r.StoreID] = true
			r.BranchPolicy.Pinned = append(slices.Clone(r.BranchPolicy.Pinned), pinned[r.StoreID]...)
			read = append(read, r)
		}
	}
	return read, blocked
}

// PlanCleanup decides for every local branch of the store of r, as read by
// st, whether it is deleted, listed with a reason, or not listed.
func PlanCleanup(r domain.Repository, st git.BranchCleanup) []CleanupCandidate {
	switch {
	case st.Remote == "":
		return []CleanupCandidate{{Repository: r, Reason: "no remote; the default branch is unknown"}}
	case st.Default == "":
		return []CleanupCandidate{{Repository: r, Reason: "default branch unknown: refs/remotes/" + st.Remote +
			"/HEAD is not set; run git remote set-head " + st.Remote + " --auto"}}
	}
	def := strings.TrimPrefix(st.Default, "refs/remotes/")
	local := strings.TrimPrefix(st.Default, "refs/remotes/"+st.Remote+"/")
	head := "HEAD"
	for _, b := range st.Branches {
		if b.Worktree == r.Location.WorktreeRoot {
			head = b.Name
		}
	}
	var deletable, skipped []CleanupCandidate
	for _, b := range st.Branches {
		if b.Name == local || !(b.MergedDefault || (b.Upstream != "" && b.Gone)) {
			continue
		}
		c := CleanupCandidate{Repository: r, Branch: b.Name, CommitID: b.CommitID}
		switch {
		case b.Worktree == r.Location.WorktreeRoot:
			c.Reason = "checked out"
		case b.Worktree != "":
			c.Reason = "checked out in " + b.Worktree
		case slices.Contains(r.BranchPolicy.Pinned, b.Name):
			c.Reason = "pinned"
		case !b.MergedDefault && !st.ContainmentChecked:
			c.Reason = "upstream gone; squash check needs Git 2.38"
		case !b.MergedDefault && !b.Contained:
			c.Reason = "upstream gone, changes not in " + def
		case b.Gone && (!b.MergedDefault || !b.MergedHead) && st.InProgress != domain.NoOperation:
			// The checked ref would be deleted without Git's check of the
			// worktrees, which refuses a branch that a rebase or bisect uses
			// while its worktree's HEAD is detached.
			c.Reason = operationName(st.InProgress) + " in progress; finish it first"
		case b.Gone:
			// Everything is in the default branch: by ancestry, or because a
			// squash or rebase merge left nothing to merge. git branch -d
			// checks HEAD or would refuse a squash-merged branch, so the
			// checked ref is deleted instead.
			c.RefDelete = !b.MergedDefault || !b.MergedHead
		case b.Upstream != "" && b.Ahead > 0:
			c.Reason = "has commits not pushed to " + b.Upstream
		case b.Upstream != "":
			// A branch that still exists on the server, such as develop or
			// a release branch, is in use even if it is merged.
			c.Reason = "upstream " + b.Upstream + " still exists"
		case b.CommitID == st.DefaultID:
			c.Reason = "no commits of its own"
		case !b.MergedHead:
			c.Reason = "merged into " + def + " but not into " + head + "; update " + head + " first"
		}
		if c.Reason == "" {
			deletable = append(deletable, c)
		} else {
			skipped = append(skipped, c)
		}
	}
	return append(deletable, skipped...)
}

// CleanupReadJob reads the branches of the store of r for the preview and
// plans them; its payload is the []CleanupCandidate of PlanCleanup. It is
// read-only, runs in the inspection pool, and takes no lock.
func CleanupReadJob(client git.Interface, r domain.Repository, timeout time.Duration) jobs.Job {
	return jobs.Job{
		Kind:         jobs.KindInspect,
		RepositoryID: r.ID,
		StoreID:      r.StoreID,
		Timeout:      timeout,
		Run: func(ctx context.Context) (any, error) {
			st, err := client.ReadBranchCleanup(ctx, r.Location, primaryRemoteName(r))
			if err != nil {
				return nil, err
			}
			return PlanCleanup(r, st), nil
		},
	}
}

func primaryRemoteName(r domain.Repository) string {
	if r.PrimaryRemote == nil {
		return ""
	}
	return r.PrimaryRemote.Name
}

// operationName names a rebase or bisect in a reason.
func operationName(op domain.OperationInProgress) string {
	switch op {
	case domain.Rebasing:
		return "rebase"
	case domain.Bisecting:
		return "bisect"
	}
	return op.String()
}

// CleanupJobs creates one job per deletable branch. Each job reads the
// branches again while holding the store lock and skips the branch if it is
// gone, moved since the preview, or no longer deletable.
func CleanupJobs(client git.Interface, all []domain.Repository, deletable []CleanupCandidate, timeout time.Duration) []jobs.Job {
	var out []jobs.Job
	for _, c := range deletable {
		r, name, commit := c.Repository, c.Branch, c.CommitID
		out = append(out, jobs.Job{
			Kind:         jobs.KindUpdate,
			Operation:    domain.OperationCleanup,
			RepositoryID: r.ID,
			Repositories: StoreMembers(all, map[domain.StoreID]bool{r.StoreID: true}),
			StoreID:      r.StoreID,
			Mutating:     true,
			Timeout:      timeout,
			Run: func(ctx context.Context) (any, error) {
				st, err := client.ReadBranchCleanup(ctx, r.Location, primaryRemoteName(r))
				if err != nil {
					return nil, fmt.Errorf("recheck failed: %w", err)
				}
				i := slices.IndexFunc(st.Branches, func(b git.CleanupBranch) bool { return b.Name == name })
				if i < 0 {
					return nil, &jobs.SkipError{Reason: "skipped: branch no longer exists"}
				}
				if st.Branches[i].CommitID != commit {
					return nil, &jobs.SkipError{Reason: "skipped: branch moved since the preview; press d to try again"}
				}
				fresh := PlanCleanup(r, st)
				j := slices.IndexFunc(fresh, func(c CleanupCandidate) bool { return c.Branch == name })
				switch {
				case j < 0:
					return nil, &jobs.SkipError{Reason: "skipped: branch is no longer merged"}
				case !fresh[j].Deletable():
					return nil, &jobs.SkipError{Reason: "skipped: " + fresh[j].Reason}
				}
				if fresh[j].RefDelete {
					return nil, client.DeleteBranchRef(ctx, r.Location, name, commit)
				}
				return nil, client.DeleteBranch(ctx, r.Location, name)
			},
		})
	}
	return out
}
