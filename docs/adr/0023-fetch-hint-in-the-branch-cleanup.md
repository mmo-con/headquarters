# ADR 0023: Fetch hint in the branch cleanup

Status: Accepted

## Context

The preview of [ADR 0018](0018-deleting-merged-branches.md) reads local
refs only. A branch deleted on the server counts as gone only after a fetch
with `--prune`; squash- and rebase-merged branches
([ADR 0019](0019-deleting-squash-and-rebase-merged-branches.md)) and, since
[ADR 0022](0022-keeping-branches-that-exist-on-the-server.md), every branch
with an upstream are deleted only then. Pressing `d` right after the start
therefore often showed "No merged branches to delete." without saying why.
Fetching automatically when the preview opens would contradict
[ADR 0012](0012-no-persistent-state-and-no-automatic-activity.md) and make
opening the preview a network operation.

## Decision

The preview does not fetch. It states how many target repositories with
remotes were not fetched since headquarters started, and asks to press `f`
first. The time of the last fetch is the newest `FETCH_HEAD` of the store,
which the inspection already reads; no state is kept besides the start time
of the session. Repositories without remotes and repositories whose fetch
time is unknown are not counted.

## Consequences

- The reason for an empty or short preview is visible, and the user decides
  whether to fetch.
- A fetch by another tool after the start also counts, because it updates
  `FETCH_HEAD`.
- The hint is shown even if a fetch would find nothing new.
