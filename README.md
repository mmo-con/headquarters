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

## Features

- Explicit repositories plus bounded discovery below root directories, with
  include and exclude patterns and clear precedence rules.
- Status per repository: branch, detached or unborn HEAD, operations in
  progress, staged/modified/deleted/renamed/conflicting/untracked entries,
  upstream state, stash entries, remotes, latest commit, and last fetch time;
  the detail panel lists the most recent commits (three by default) and the
  incoming upstream commits as of the last fetch. Repositories not fetched
  within seven days are flagged. Stash entries are only reported; applying
  or dropping them stays with lazygit.
- Other local branches that need attention, such as a stale `main` or a
  branch with unpushed work, shown as a count with details in the detail
  panel;
  old and ignored branches are filtered out, pinned branches always count.
- Families as a filter or as a collapsible tree; text, family, and state
  filters (`family:work`, `is:dirty`, `is:behind`, …); sorting, also by
  health with the most urgent repositories on top.
- `Enter` opens the selected repository in lazygit, or in the terminal Git
  client configured in `[lazygit]`; the dashboard refreshes the repository
  afterward.
- Concurrent fetch of the marked or visible repositories with progress,
  cancellation, retry, and an interactive retry in the terminal for
  repositories that need a passphrase.
- Safe update after one confirmation that lists the incoming commits of each
  repository: the dashboard fetches first, then fast-forwards only branches
  that can be updated safely, without merge commits, rebases, or stashes, and
  lists every skipped repository with the reason. The operations view then
  serves as a worklist: open blocked repositories in lazygit, see which
  blockers are resolved, and continue.
- Delete merged local branches across repositories after a preview: only
  branches whose changes are all in the default branch, also after squash
  and rebase merges, and that no longer exist on the server; every kept
  branch is listed with the reason.
- [`headquarters status`](#status-for-scripts) prints the same status as
  JSON for scripts and status bars.
- Configurable launchers for a shell, an editor, or a file manager, and
  opening the remote in the browser.
- `y` copies the worktree path, branch name, primary remote URL, or its web
  URL (HTTPS, also for SSH remotes) of the selected repository to the
  clipboard.
- lazygit-style panels: one column per count (staged, modified, untracked,
  stashed, ahead, behind) plus a State column, header counters that double
  as quick filters, and dialogs as overlays.
- Configurable colors, theme presets (such as high contrast, Catppuccin,
  Dracula, Gruvbox, Monokai, and One; Catppuccin, Gruvbox, and One follow
  the terminal's light or dark background), and symbol sets (Unicode, Nerd
  Font, ASCII). `NO_COLOR` is honored.

headquarters uses the official Git CLI for everything. Background Git
processes run without a controlling terminal, so they can never prompt in or
write into the user interface.

## Requirements

- Linux or macOS
- Git 2.34 or newer; detecting squash and rebase merges in the branch
  cleanup needs Git 2.38
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
headquarters status               # print the status as JSON and exit, see below
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
without starting the user interface, for example to check for uncommitted or
unpushed work before shutting down. It never changes a repository and
accesses the network only with `--fetch`.

```sh
headquarters status                                           # one JSON document
headquarters status --filter "is:dirty family:work"           # dashboard filter syntax
headquarters status --format ndjson                           # one repository per line
headquarters status --fail-on dirty,ahead,unpublished,unknown # exit 3 if any repository matches
headquarters status --fetch --fail-on behind                  # fetch the matching repositories first
```

The options, exit statuses, and report fields are described in
[docs/status.md](docs/status.md).

## Configuration

The configuration file is looked up in this order: `--config`, the
`HEADQUARTERS_CONFIG` environment variable, and
`$XDG_CONFIG_HOME/headquarters/config.toml` (falling back to
`~/.config/headquarters/config.toml`). After upgrading from the previous
project name, move your existing configuration to the new location and
update any environment variable or scripts that refer to the old name.

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

The package structure, the architecture, and the decisions behind it are
documented in [docs/architecture.md](docs/architecture.md), with individual
[architecture decision records](docs/adr/README.md).

## License

Licensed under the [MIT License](LICENSE).
