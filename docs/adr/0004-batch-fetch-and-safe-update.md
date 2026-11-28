# ADR 0004: Batch fetch and safe update

Status: Accepted, amended by [ADR 0011](0011-families-in-the-tree.md),
[ADR 0015](0015-showing-incoming-commits.md), and
[ADR 0018](0018-deleting-merged-branches.md)

## Context

Batch operations must be useful across many repositories without silently
changing local work or applying stale state. Linked worktrees can share
remote-tracking refs.

## Decision

Lowercase `f` and `u` target marked repositories, or the cursor row if none
are marked. Uppercase `F` and `U` target all visible rows regardless of
marks. Marks survive filter changes; the fetch message and update
confirmation state how many marked targets are hidden.
[ADR 0011](0011-families-in-the-tree.md) defines the targets of a family
row and which rows count as visible in the tree.

### Fetch

Fetch once per store with `git -C <root> fetch --all [--prune]`, then
inspect all its entries again. Fetches are bounded by `fetch_parallelism`
and a per-fetch timeout; a failure does not stop the batch. Failed and
cancelled fetches can be retried, including an interactive terminal retry
for prompts. Fetch needs no confirmation: it changes remote-tracking refs,
not local branches or the worktree.

### Safe update

Require a preview and confirmation for operations that change local
branches. `u` and `U` show targets and their pre-fetch state and describe
the action in plain language. The confirmation is for the intent to fetch,
then fast-forward what is safe; the commit list is not yet known.
[ADR 0015](0015-showing-incoming-commits.md) shows the incoming commits as
of the last fetch in the confirmation and the operations view.

After confirmation, fetch every target store, then plan the update from the
fetched state. A failed fetch is the skip reason for every target in that
store; a cancelled fetch cancels the update. Never update from stale state.

A target is eligible only when all of these conditions hold:

- no staged, modified, deleted, renamed, or conflicting entries;
- no merge, rebase, cherry-pick, revert, or bisect in progress;
- HEAD is a local branch with commits and an existing upstream whose commit
  is known;
- the branch is behind and not ahead of upstream;
- no other mutating job or interactive session is active for its store.

Untracked and ignored files do not themselves block eligibility, but must
not be overwritten. Apply exactly the inspected upstream commit:

```text
git -C <root> merge --ff-only --no-autostash --no-overwrite-ignore <upstream commit>
```

Recheck every condition under the store lock immediately before the merge.
The branch, HEAD commit, upstream, and upstream commit must still match the
plan; otherwise skip with a reason and ask the user to retry. The lock
coordinates headquarters jobs, not external Git processes. An external
process can still race after the recheck, but cannot change the commit
passed to the merge.

Never automatically create a stash, merge commit, rebase, reset, force-push,
clone, remote rewrite, or Git configuration change.

### Worklist and other branches

The operations view is the update worklist: updated, failed, cancelled, and
skipped targets, except those already up to date. Open rows in lazygit;
after inspection they show `→ now eligible`, `→ now up to date`, or
`→ still blocked: <reason>`. `u` opens a new confirmation for everything
not yet updated.

Rows, state filters, sorting, and safe updates describe the checked-out
branch. Report other local branches only when they need attention: no
upstream, upstream gone, ahead, or behind. Apply `[branches].max_age` and
`ignore`; pinned branches bypass filters. A missing pinned branch is
reported only for per-repository lists. Exclude branches checked out in
another worktree of the store.

Only `refs/heads` are considered. A merged branch without upstream remains
reported until it exceeds `max_age`; detecting merged branches needs another
process or Git 2.41. Base-branch comparisons and remote-only branches are out
of scope for this report; deleting merged branches compares with the
default branch on demand ([ADR 0018](0018-deleting-merged-branches.md)). Fast-forwarding a non-checked-out branch would be a separate,
opt-in operation with its own preview, never part of `u`.

## Consequences

`merge --ff-only` rather than `pull --ff-only` keeps network access in the
separate fetch, ignores `pull.*` settings, and applies the planned commit
rather than a later `@{upstream}`. `--no-autostash` overrides
`merge.autoStash`; `--no-overwrite-ignore` makes ignored-file collisions fail
instead of replacing files. Git itself refuses to overwrite untracked files.

The fast-forward does not update submodules, like `git merge` itself. A
submodule whose recorded commit moved stays at its old commit, so the
worktree is modified and the next update is blocked. If these submodules are
the only change, the reason names `git submodule update`; running it stays
with the user or lazygit.

Reporting only branches needing attention avoids noise without missing
unpublished work, unlike a fixed base branch per repository. Asynchronous
completion and store locking are specified in
[ADR 0005](0005-terminal-handover-and-job-coordination.md).
