# ADR 0018: Deleting merged branches

Status: Accepted, amended by [ADR 0019](0019-deleting-squash-and-rebase-merged-branches.md)
and [ADR 0022](0022-keeping-branches-that-exist-on-the-server.md)

## Context

Local branches accumulate after their work was merged. headquarters reports
other local branches that need attention, including those whose upstream is
gone, but removing them meant opening every repository in lazygit
([#45](https://github.com/mmo-con/headquarters/issues/45)).
[ADR 0001](0001-product-boundaries-and-technology.md) kept all changes
within a repository, apart from fetch and the safe update, with lazygit.
[ADR 0004](0004-batch-fetch-and-safe-update.md) kept base-branch
comparisons out of scope, because detecting merged branches needs further
Git processes.

## Decision

headquarters can delete local branches that are fully merged into the
default branch, as a batch with a preview and one confirmation. This amends
the product boundaries of ADR 0001; lazygit remains responsible for every
other change to branches.

**Targets and preview.** `d` targets marked repositories, or the repository
or family under the cursor if none are marked; `D` targets all visible
repositories, following the rules of ADR 0004 and
[ADR 0011](0011-families-in-the-tree.md). The preview opens at once and
reads, per store and without network access:

```text
git symbolic-ref -q refs/remotes/<remote>/HEAD
git for-each-ref --format=… refs/heads
git for-each-ref --merged=<default ref> --format=%(refname) refs/heads
git for-each-ref --merged=HEAD --format=%(refname) refs/heads
```

`<remote>` is the primary remote of
[ADR 0005](0005-terminal-handover-and-job-coordination.md). The default
branch is the target of `refs/remotes/<remote>/HEAD`; there is no fallback
to a local `main` or `master`. A repository without a remote or without
this ref is listed with the reason and the hint
`git remote set-head <remote> --auto`. Several entries of a store are read
once, from the first target entry. The confirmation is accepted only after
every read is finished, so no dialog becomes confirmable asynchronously.
The inspection itself does not change and keeps its cost.

**Candidates.** A branch is deleted only if it is merged into the default
branch and Git's own rule of `git branch -d` allows it: merged into its
upstream if that exists, otherwise into HEAD of the worktree that runs the
deletion. [ADR 0019](0019-deleting-squash-and-rebase-merged-branches.md)
also deletes branches with a gone upstream whose changes are all in the
default branch, after a squash or rebase merge, or by ancestry but not in
HEAD. [ADR 0022](0022-keeping-branches-that-exist-on-the-server.md)
keeps every branch whose upstream still exists. The preview lists, with a
reason, branches that are merged or whose upstream is gone but are not
deleted:

| Reason | Condition |
|---|---|
| checked out | in this or another worktree of the store |
| pinned | named in the pinned branches of any entry of the store |
| not merged | upstream gone but changes not in the default branch ([ADR 0019](0019-deleting-squash-and-rebase-merged-branches.md)) |
| unpushed commits | ahead of its existing upstream |
| upstream still exists | merged, upstream exists ([ADR 0022](0022-keeping-branches-that-exist-on-the-server.md)) |
| update first | without a usable upstream and not merged into HEAD |
| no commits of its own | without upstream and at the commit of the default branch, for example just created |

The local branch named like the default branch and branches that are
neither merged nor without upstream are not listed.

**Deletion.** One mutating job per branch runs under the store lock. It
reads the state again, applies the same rules, and skips the branch if it
no longer exists, if its commit changed since the preview, or if a rule no
longer holds. Only then it runs:

```text
git branch -d -- <branch>
```

Never `-D`: if anything changed after the recheck, Git refuses the
deletion, and the row fails with Git's message. ADR 0019 deletes branches
that `-d` would refuse with a compare-and-delete of the checked commit. Branch deletion uses the
operations view of ADR 0004 with one row per branch; a failure does not
stop the batch. Its results do not change the health of the repository,
which describes its state, not the latest deletion. The stores are
inspected again afterward.

## Consequences

- Merged branches can be removed across a collection without opening each
  repository. `git branch -d` also removes the branch's reflog; the commits
  stay reachable from the default branch and from the upstream or HEAD, as
  the candidate rules require.
- Squash- and rebase-merged branches cannot be detected from the ancestry;
  [ADR 0019](0019-deleting-squash-and-rebase-merged-branches.md) detects
  them by their content.
- Opening the preview starts up to four Git processes per store.
- A branch without an upstream that is merged into the default branch but
  not yet into local HEAD needs a safe update first, because
  `git branch -d` checks HEAD; ADR 0019 removes this step for branches
  with a gone upstream.
