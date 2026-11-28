<p align="center">
  <img src="docs/images/headquarters.png" alt="headquarters logo" width="160" />
</p>

<h1 align="center">headquarters</h1>

<p align="center">
  A CLI dashboard that groups local Git repositories in a table view, runs batch fetches and safe fast-forward updates, and launches <code>lazygit</code> per repo.
</p>

<p align="center">
  <a href="https://github.com/mmo-con/headquarters/actions/workflows/ci.yml"><img src="https://github.com/mmo-con/headquarters/actions/workflows/ci.yml/badge.svg?branch=main" alt="Build status" /></a>
  <a href="https://github.com/mmo-con/headquarters/releases/latest"><img src="https://img.shields.io/github/v/release/mmo-con/headquarters?style=flat-square" alt="Release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=flat-square" alt="License: MIT" /></a>
</p>

---

headquarters is a terminal application for managing a curated collection of
local Git repositories. It shows a cross-repository overview, groups
repositories into families, runs safe batch operations such as fetch and
fast-forward updates, and hands all detailed work within one repository over
to [lazygit](https://github.com/jesseduffield/lazygit) or another terminal
Git client, such as [gitui](https://github.com/gitui-org/gitui) or
[tig](https://github.com/jonas/tig).

> headquarters manages the repository collection. lazygit manages the
> individual repository.

![headquarters in the tree view: repositories grouped by family with health
symbols, family summaries, and the detail panel of the selected
repository](docs/images/headquarters-screenshot.png)

## Quick start

[Install headquarters](#installation), then point it at a directory containing
your repositories. No configuration file is needed:

```sh
headquarters ~/src
```

To keep the result, generate a starting configuration that groups the
repositories by their first-level directory, then review it:

```sh
headquarters init -o ~/.config/headquarters/config.toml ~/src
```

Use `j` / `k` to navigate, `Enter` to open a repository in lazygit, `f` to
fetch, and `u` for a safe update after confirmation. Press `?` for help or `q`
to quit. See [key bindings](docs/keybindings.md) for all shortcuts.

The architecture and the decisions behind it are documented in
[docs/architecture.md](docs/architecture.md), with individual
[architecture decision records](docs/adr/README.md).

## Features

- Explicit repositories plus bounded discovery below root directories, with
  include and exclude patterns and clear precedence rules.
- Status per repository: branch, detached or unborn HEAD, operations in
  progress, staged/modified/deleted/renamed/conflicting/untracked entries,
  upstream state, stash entries, remotes, latest commit, and last fetch time;
  the detail panel lists the most recent commits (three by default) and the
  incoming upstream commits as of the last fetch. A
  repository that was never fetched or not within seven days is flagged in
  the table and has a quick filter.
  Stash entries are only reported, as a count and with a quick filter;
  applying or dropping them stays with lazygit.
- Other local branches that need attention, such as a stale `main` or a
  branch with unpushed work, shown as a count with details in the side panel;
  old and ignored branches are filtered out, pinned branches always count.
- Families as a filter or as a collapsible tree, text, family, and state
  filters (`family:work`, `is:dirty`, `is:behind`, …), and sorting, also by
  health with the most urgent repositories on top.
- `Enter` opens the selected repository in lazygit, or in the terminal Git
  client configured in `[lazygit]`; the dashboard refreshes the repository
  afterward.
- Concurrent fetch of the selected or visible repositories with progress,
  cancellation, retry, and an interactive retry in the terminal for
  repositories that need a passphrase.
- Safe update after one confirmation that lists the incoming commits of each
  repository: the dashboard fetches first, then fast-forwards only branches
  that can be updated safely, without merge commits, rebases, or stashes, and
  lists every skipped repository with the reason. The operations view then serves as a worklist: open blocked
  repositories in lazygit, see which blockers are resolved, and continue.
- Delete merged local branches across repositories after a preview: only
  branches whose changes are all in the default branch, also after squash
  and rebase merges, and that no longer exist on the server; every kept
  branch is listed with the reason.
- `headquarters status` prints the same status, filter results, and health as
  JSON for scripts and status bars, with an exit status for checks such as
  "nothing uncommitted before shutdown". It only reads, unless `--fetch` is
  given.
- Configurable launchers for a shell, an editor, or a file manager.
- `y` copies the worktree path, branch name, primary remote URL, or its web
  URL (HTTPS, also for SSH remotes) of the selected repository to the
  clipboard.
- lazygit-style panels with titled borders, one column per count (staged,
  modified, untracked, stashed, ahead, behind) plus a State column for everything
  else, per-cell colors, spinners and progress bars, dialogs as overlays, and
  header counters that double as quick filters. Colors, built-in theme presets
  (such as high contrast, Catppuccin, Dracula, and Gruvbox), and the symbol
  set (Unicode, Nerd Font, ASCII) are configurable; `NO_COLOR` is honored.

headquarters uses the official Git CLI for everything. Background Git
processes run without a controlling terminal, so they can never prompt in or
write into the user interface.

## Requirements

- Linux or macOS
- Git 2.34 or newer
- lazygit or another terminal Git client (optional, for `Enter`)
- `pbcopy` on macOS, or `wl-copy`, `xclip`, or `xsel` on Linux (optional,
  for copying to the clipboard with `y`)
- Go 1.26 or newer to build from source

## Installation

### Quick install

On Linux or macOS (amd64 or arm64), download the installer and run it:

```sh
curl -fsSL https://github.com/mmo-con/headquarters/releases/latest/download/install.sh -o install.sh &&
  sh install.sh
```

You can inspect `install.sh` before running it. It installs to `~/.local/bin`
without `sudo` and tells you how to add that directory to `PATH` if needed.
It verifies the archive against the SHA-256 checksum from the same release;
the checksum is not an independent signature. If the selected release does
not include `install.sh`, use [manual installation](#manual-installation).

### Manual installation

Download the archive for your platform and `checksums.txt` from the same
[release](https://github.com/mmo-con/headquarters/releases), verify the
archive's SHA-256 checksum, then extract it and copy the `headquarters`
binary to a directory on your `PATH`.

The macOS binaries are not notarized. `curl` downloads run as they are; if
you download an archive with a browser, macOS blocks the binary until you
remove the quarantine attribute with
`xattr -d com.apple.quarantine headquarters`.

### Build from source

```sh
make build            # builds ./bin/headquarters
make install          # installs into ~/.local/bin, like the installer
```

`make install PREFIX=/usr/local` installs into `/usr/local/bin`; `BINDIR`
sets the directory directly, and `DESTDIR` stages the installation for
packaging.

## Usage

```sh
headquarters                      # uses ~/.config/headquarters/config.toml
headquarters ~/src                # repositories below ~/src, no inventory configuration needed
headquarters --config path.toml   # explicit configuration file
headquarters --check              # validate the configuration and print the inventory
headquarters --version
headquarters status --json        # print the status as JSON and exit, see below
headquarters init ~/src ~/work    # print a starting configuration for these directories
headquarters init -o config.toml ~/src   # write it to a file; --force replaces an existing file
```

With a path, headquarters shows the repositories found below that directory
(or the directory itself if it is a repository) instead of the configured
ones. All other settings of the configuration file, such as the theme,
lazygit, and launchers, still apply. Options must precede the path.

`headquarters init` scans directories once and prints a commented
configuration with `[[discovery]]` entries, using each first-level
directory that contains repositories as a family. It never changes
repositories or overwrites a file without `--force`. See
[Generating a configuration](docs/configuration.md#generating-a-configuration).

### Status for scripts

`headquarters status` inspects the repositories and prints their status
without starting the user interface. It never changes a repository and
accesses the network only with `--fetch`.

```sh
headquarters status --json                                    # one JSON document
headquarters status --filter "is:dirty family:work"           # dashboard filter syntax
headquarters status --format ndjson                           # one repository per line
headquarters status --fail-on dirty,ahead,unpublished,unknown # exit 3 if any repository matches
headquarters status --fetch --fail-on behind                  # fetch the matching repositories first
```

`--filter` accepts the text filter of the dashboard
([filter syntax](docs/keybindings.md#filter-syntax)) except `is:marked`.
`--fetch` works like `F` in the dashboard: it fetches the repositories that
match the filter, with the configured fetch parallelism and timeout, then
inspects them again and applies the filter to the new state. `--fail-on`
takes the states of the filter language without `is:`, such as `dirty`,
`behind`, `ahead`, `diverged`, `unpublished`, `stashed`, `stale`, or `error`,
plus `unknown`, which matches every repository with `known: false`, such as an
unavailable entry or a failed inspection; `error` also matches a failed fetch.
Add `unknown` to checks such as `--fail-on dirty`, so that a repository whose
state cannot be read does not pass unnoticed. A path argument replaces
the configured repositories as in the dashboard. The report follows the
configured sort order.

| Exit status | Meaning |
|---|---|
| 0 | report written, no `--fail-on` state found |
| 1 | runtime error, such as an unusable Git executable or an interrupt |
| 2 | usage error, invalid filter, or invalid configuration |
| 3 | report written, and a repository matches a `--fail-on` state |

Only the report is written to standard output; warnings go to standard
error. The document has a `schema` version, which increases only when a
field is renamed, removed, or changes its meaning:

```json
{
  "schema": 1,
  "generated_at": "2026-11-23T17:00:00Z",
  "summary": { "total": 42, "dirty": 5, "behind": 3, "unknown": 1, "…": 0,
               "health": { "error": 0, "critical": 1, "dirty": 5, "…": 0 } },
  "repositories": [
    {
      "name": "headquarters",
      "path": "/home/me/src/headquarters",
      "family": "mmo-con",
      "availability": "available",
      "known": true,
      "health": "dirty",
      "branch": "main",
      "head": "attached",
      "operation": "none",
      "counts": { "staged": 0, "modified": 2, "deleted": 0, "renamed": 0,
                  "conflicts": 0, "untracked": 1, "stashed": 1 },
      "upstream": { "name": "origin/main", "configured": true, "gone": false,
                    "ahead": 0, "behind": 3 },
      "branches": [
        { "name": "release", "state": "behind", "pinned": true,
          "upstream": "origin/release", "ahead": 0, "behind": 2,
          "commit_time": "2026-11-20T09:30:00Z" }
      ],
      "remote": { "name": "origin", "url": "git@github.com:mmo-con/headquarters.git" },
      "last_fetch": "2026-11-23T08:12:00Z",
      "last_commit": { "hash": "abc1234…", "short_hash": "abc1234",
                       "author": "Jane Doe",
                       "subject": "tui: group the operations view by status",
                       "time": "2026-11-22T20:41:00Z" },
      "fetch": null,
      "error": null
    }
  ]
}
```

- `summary` counts the reported repositories in total, per `--fail-on`
  state, and per `health` level.
- `health` is the level of the dashboard's health column: `error`,
  `critical`, `dirty`, `behind`, `ahead`, `attention`, `ok`, or `unknown`.
- `head` is `attached`, `detached`, or `unborn`; `operation` is `none` or the
  operation in progress, such as `merging` or `rebasing`.
- Unknown state is `null`, and `known` is `false` if any part of the state
  could not be determined; an unknown repository is never reported as clean
  or up to date. `counts.untracked` is `null` when untracked files are not
  checked, `last_fetch` when the repository was never fetched or the time is
  unknown, `upstream.name` when no upstream is configured, and
  `upstream.ahead` and `upstream.behind` when no upstream is configured or
  it is gone. A count is `0` only when it is known to be zero.
- `branches` lists the other local branches that need attention, as in the
  detail panel: pinned branches first, then the others by latest commit. `state` is `missing`, `noupstream`,
  `gone`, `diverged`, `ahead`, or `behind`; `upstream` is `null` without an
  upstream, `ahead` and `behind` are `null` unless the upstream exists, and
  `commit_time` is `null` for a missing pinned branch. `branches` is `null`
  when the branch state is unknown or `[branches].enabled = false`, and `[]`
  when no branch needs attention. Count them so that `null` stays
  distinguishable from zero, for example with
  `jq '.repositories[] | .branches | if . == null then "?" else length end'`;
  `--fail-on branches` checks for them directly.
- `fetch` is `null` without `--fetch`, otherwise the `status` (`succeeded`,
  `failed`, `skipped`, or `cancelled`) and `error` of the fetch.
- `error` collects configuration, inspection, and fetch errors.
- Text from repositories is stripped of control characters, and user
  information in URLs is redacted.

The configuration file is looked up in this order: `--config`, the
`HEADQUARTERS_CONFIG` environment variable, and
`$XDG_CONFIG_HOME/headquarters/config.toml` (falling back to
`~/.config/headquarters/config.toml`).
After upgrading from the previous project name, move your existing
configuration to the new location and update any environment variable or
scripts that refer to the old name.

Start with [examples/config.toml](examples/config.toml). All options are
described in [docs/configuration.md](docs/configuration.md), and all keys in
[docs/keybindings.md](docs/keybindings.md).

## Development

```sh
make test             # unit and integration tests (requires git)
make test-installer   # offline tests for the release installer
make test-race        # tests with the race detector
make coverage         # race-detector tests with a Markdown coverage report
make perf             # local benchmark: 200 repositories within 2 seconds
make lint             # go vet and gofmt check
make vuln             # govulncheck for modules and the Go standard library
make release          # cross-compiled archives, checksums, and installer in ./dist
```

Integration tests create temporary Git repositories and ignore the user's
global Git configuration.

### Project layout

| Path | Responsibility |
|---|---|
| `cmd/headquarters` | command-line entry point, `status` and `init` commands |
| `internal/config` | configuration lookup, parsing, path expansion, validation |
| `internal/domain` | domain models without dependencies on UI or Git output |
| `internal/inventory` | discovery, identity, and precedence rules |
| `internal/initconfig` | starting configuration generated by `headquarters init` |
| `internal/git` | typed wrapper around the Git CLI with process isolation |
| `internal/jobs` | bounded scheduler with per-store locks and event queue |
| `internal/launcher` | lazygit, launchers, and opening remotes in a browser |
| `internal/clipboard` | copying text through native clipboard tools |
| `internal/app` | application services: refresh, fetch, update, branch cleanup, health, filter, sort, status report |
| `internal/tui` | Bubble Tea user interface |
| `internal/testutil` | helpers for tests with real Git repositories |
| `scripts` | development scripts, such as the coverage report used in CI |

## License

Licensed under the [MIT License](LICENSE).
