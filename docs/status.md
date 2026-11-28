# Status command

`headquarters status` inspects the repositories and prints their status
without starting the user interface. It never changes a repository and
accesses the network only with `--fetch`
([ADR 0016](adr/0016-non-interactive-status-command.md)).

```sh
headquarters status [--config FILE] [--json | --format json|ndjson]
                    [--filter EXPR] [--fetch] [--fail-on STATES] [PATH]
```

```sh
headquarters status --json                                    # one JSON document
headquarters status --filter "is:dirty family:work"           # dashboard filter syntax
headquarters status --format ndjson                           # one repository per line
headquarters status --fail-on dirty,ahead,unpublished,unknown # exit 3 if any repository matches
headquarters status --fetch --fail-on behind                  # fetch the matching repositories first
```

## Options

- `--filter` accepts the text filter of the dashboard
  ([filter syntax](keybindings.md#filter-syntax)) except `is:marked`.
- `--fetch` works like `F` in the dashboard: it fetches the repositories that
  match the filter, with the configured fetch parallelism and timeout, then
  inspects them again and applies the filter to the new state.
- `--fail-on` takes the states of the filter language without `is:`, such as
  `dirty`, `behind`, `ahead`, `diverged`, `unpublished`, `stashed`, `stale`,
  or `error`, plus `unknown`, which matches every repository with
  `known: false`, such as an unavailable entry or a failed inspection;
  `error` also matches a failed fetch. Add `unknown` to checks such as
  `--fail-on dirty`, so that a repository whose state cannot be read does not
  pass unnoticed. `headquarters status -h` lists all states.
- A path argument replaces the configured repositories as in the dashboard.

The report follows the configured sort order.

## Exit status

| Exit status | Meaning |
|---|---|
| 0 | report written, no `--fail-on` state found |
| 1 | runtime error, such as an unusable Git executable or an interrupt |
| 2 | usage error, invalid filter, or invalid configuration |
| 3 | report written, and a repository matches a `--fail-on` state |

## Report

Only the report is written to standard output; warnings go to standard
error. The document has a `schema` version, which increases only when a
field is renamed, removed, or changes its meaning:

```json
{
  "schema": 1,
  "generated_at": "2026-11-23T17:00:00Z",
  "summary": { "total": 42,
               "states": { "dirty": 5, "behind": 3, "unknown": 1, "…": 0 },
               "health": { "error": 0, "critical": 1, "dirty": 5, "…": 0 } },
  "repositories": [
    {
      "name": "headquarters",
      "path": "/home/me/src/headquarters",
      "family": "mmo-con",
      "availability": "available",
      "known": true,
      "health": "dirty",
      "states": ["dirty", "behind", "branches", "stashed"],
      "head": {
        "state": "attached",
        "branch": "main",
        "commit": { "hash": "abc1234…", "short_hash": "abc1234",
                    "author": "Jane Doe",
                    "subject": "tui: group the operations view by status",
                    "time": "2026-11-22T20:41:00Z" },
        "upstream": { "name": "origin/main", "gone": false, "ahead": 0, "behind": 3 }
      },
      "worktree": { "operation": "none", "staged": 0, "modified": 2, "deleted": 0,
                    "renamed": 0, "conflicts": 0, "untracked": 1 },
      "stashes": 1,
      "branches": [
        { "name": "release", "state": "behind", "pinned": true,
          "upstream": { "name": "origin/release", "gone": false, "ahead": 0, "behind": 2 },
          "commit_time": "2026-11-20T09:30:00Z" }
      ],
      "remote": { "name": "origin", "url": "git@github.com:mmo-con/headquarters.git" },
      "last_fetch": "2026-11-23T08:12:00Z",
      "fetch_result": null,
      "error": null
    }
  ]
}
```

With `--format ndjson`, each line is one entry of `repositories` with its
own `schema` field; there is no `generated_at` and no `summary`.

- `summary` counts the reported repositories in `total`, per `--fail-on`
  state in `states`, and per `health` level in `health`. `states` and
  `health` list every state and level, including those with a count of 0.
- `states` lists the `--fail-on` states that the repository is in, in the
  order of `headquarters status -h`, for example to see why its `health` is
  `attention`. It contains `unknown` exactly when `known` is `false`.
- `health` is the level of the dashboard's health column: `error`,
  `critical`, `dirty`, `behind`, `ahead`, `attention`, `ok`, or `unknown`.
- `family` is `null` for repositories without a family, which the dashboard
  shows as Ungrouped and `--filter family:ungrouped` selects.
- `availability` is `available`, `missing`, `not_a_repository`,
  `not_worktree_root`, `bare`, `untrusted_owner`, or `validation_failed`.
- `head` describes the checked-out branch: `state` is `attached`,
  `detached`, or `unborn`; `branch` is `null` when HEAD is detached, and
  `commit` is `null` when it is unborn.
- `upstream`, in `head` and in each entry of `branches`, has the same
  fields: `name` is `null` when no upstream is configured, `gone` reports an
  upstream that no longer exists, and `ahead` and `behind` are `null` unless
  the upstream exists.
- `worktree` counts the changed entries of the worktree; its `operation` is
  `none` or the operation in progress: `merging`, `rebasing`, `cherry_picking`,
  `reverting`, or `bisecting`. `stashes` is the number of stash entries,
  which all worktrees of a repository share.
- Values are stable identifiers, not display text. Values taken from the
  filter language, such as the `--fail-on` states, are spelled as there;
  all others use snake_case.
- Unknown state is `null`, and `known` is `false` if any part of the state
  could not be determined; an unknown repository is never reported as clean
  or up to date. `worktree.untracked` is `null` when untracked files are not
  checked, `worktree` and `stashes` when they cannot be read, `last_fetch` when the repository was never fetched or the time is
  unknown, `head` when HEAD cannot be read, and `head.upstream` when the
  upstream state is unknown. A count is `0` only when it is known to be
  zero.
- `branches` lists the other local branches that need attention, as in the
  detail panel: pinned branches first, then the others by latest commit.
  `state` is `missing`, `noupstream`, `gone`, `diverged`, `ahead`, or
  `behind`; `upstream` and `commit_time` are `null` for a missing pinned
  branch. `branches` is `null` when the branch state is
  unknown or `[branches].enabled = false`, and `[]` when no branch needs
  attention. Count them so that `null` stays distinguishable from zero, for
  example with
  `jq '.repositories[] | .branches | if . == null then "?" else length end'`;
  `--fail-on branches` checks for them directly.
- `last_fetch` is the time of the latest fetch of the repository, by any
  tool. `fetch_result` is `null` without `--fetch`, otherwise the `status`
  (`succeeded`, `failed`, `skipped`, or `cancelled`) and `error` of the fetch
  of this run.
- `error` collects configuration and inspection errors. A failed fetch is
  reported only in `fetch_result`, but it also sets `health` to `error` and
  adds `error` to `states`.
- Text from repositories is stripped of control characters, and user
  information in URLs is redacted.
