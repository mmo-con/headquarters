# ADR 0022: Keeping branches that exist on the server

Status: Accepted

## Context

[ADR 0018](0018-deleting-merged-branches.md) deleted every local branch
that is merged into the default branch and into its existing upstream.
This includes branches that are still in use on the server: `develop` after
a release merge, release branches, and a branch that was just pushed without
commits of its own. Only a pinned branch was kept. The reason
`no commits of its own` applied only to branches without an upstream. No
commits are lost, because the upstream still has them, but the local branch
and its upstream configuration disappear, which a user cleaning up merged
work does not expect.

## Decision

A branch whose upstream still exists is never deleted. It is listed with
the reason `upstream <upstream> still exists`, or with
`has commits not pushed to <upstream>` if it is ahead. This amends the
candidates of ADR 0018: deleted are branches whose upstream is gone
([ADR 0019](0019-deleting-squash-and-rebase-merged-branches.md)) and
branches without an upstream that are merged into the default branch and
into HEAD.

A gone branch at the commit of the default branch is still deleted: after a
fast-forward merge on the server, the default branch points to the branch's
commit, and the branch has no other changes.

## Consequences

- Long-lived branches no longer need a pin to survive the cleanup.
- A merged branch whose upstream still exists stays until it is deleted on
  the server and a fetch with `--prune` marks it as gone; it is then
  deleted like any gone branch. Hosting services that keep merged branches
  leave such branches to lazygit.
- `git branch -d` is no longer used for branches with an existing upstream,
  so its rule of merging into the upstream no longer decides a deletion.
