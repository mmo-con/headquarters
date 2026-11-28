# Configuration

headquarters reads a TOML file. The file is located in this order:

1. the `--config <path>` command-line option;
2. the `HEADQUARTERS_CONFIG` environment variable;
3. `$XDG_CONFIG_HOME/headquarters/config.toml`, falling back to
   `~/.config/headquarters/config.toml`.

If no file exists, headquarters starts with an empty inventory and shows the
expected path.

After upgrading from the previous project name, move the existing configuration
file to the new location or select it with `--config` or `HEADQUARTERS_CONFIG`.
The previous configuration location and environment variable are not read
automatically.

`headquarters <path>` replaces `[[repositories]]` and `[[discovery]]` with a
single discovery below `<path>` with the defaults described below
(`max_depth = 3`, no exclusions, family `Ungrouped`, symbolic links not
followed). All other settings of the file apply; `R` scans the same path
again. `headquarters --check` validates the file and prints the
resolved inventory without starting the user interface.

Syntax errors, type errors, and unknown fields prevent startup. Problems with
individual entries, such as a missing path, are shown as entry states.

A complete example is in [../examples/config.toml](../examples/config.toml).

## Generating a configuration

```sh
headquarters init [--max-depth N] [-o FILE] [--force] DIR...
```

`init` scans the directories once with the discovery rules below and prints
a commented configuration that contains `version = 1` and only
`[[discovery]]` and `[[repositories]]` entries. Review it before use; all
other sections keep their defaults. `init` does not read a configuration
file, uses `git` from `PATH`, and never changes repositories.

| Option | Default | Description |
|---|---|---|
| `--max-depth N` | `3` | directory levels below each directory that are scanned, as for `headquarters <path>` |
| `-o FILE` | standard output | write the configuration to `FILE`; missing parent directories are created and the file is replaced atomically |
| `--force` | off | replace an existing `FILE`; a symbolic link is kept and its target is replaced |

`init` must be the first argument, and options precede the directories. A
directory named `init` is scanned with `headquarters ./init`.

For each directory, the generated configuration covers the scanned area, so
repositories cloned later are found as well:

- Every first-level directory that contains repositories becomes a
  `[[discovery]]` entry with the directory name as `family` and
  `max_depth = N - 1`.
- The directory itself becomes a `[[discovery]]` entry with family
  `Ungrouped` and `max_depth = N` that excludes the grouped directories.
- A directory that is itself a repository becomes a `[[repositories]]`
  entry.
- Nested repositories are written as commented-out `[[repositories]]`
  entries; submodules are skipped.

Paths below the home directory are written as `~/…`. A directory that
overlaps an earlier one is skipped. Paths that contain `$` or are not valid
UTF-8 cannot be written; affected directories stay `Ungrouped` or are
skipped. Every warning is printed to standard error and repeated as a
`# warning:` comment at the top of the generated file.

The exit status is `0` on success, also with warnings; `1` if Git is
unusable, no repository is found (nothing is written), or the file cannot
be written, including an existing file without `--force`; and `2` for usage
errors such as a missing directory.

## Paths

All path values (`path`, `root`, `[git].executable`, `[lazygit].command`, and
the first element of a launcher command) support:

- `~` and `~/` at the beginning;
- environment variables in the form `${NAME}`; an undefined variable is an
  error. `$NAME` without braces is rejected.

Relative paths are resolved against the directory of the configuration file.
A command without a slash, such as `lazygit`, is looked up in `PATH`.

## Top level

| Key | Type | Default | Description |
|---|---|---|---|
| `version` | integer | `1` | configuration format version; only `1` is supported |

## `[[repositories]]`

Explicit repositories. They are always included, even below directories that
a discovery rule excludes.

| Key | Type | Default | Description |
|---|---|---|---|
| `path` | string | required | worktree root of the repository; a subdirectory is reported as `config error` |
| `name` | string | directory name | name shown in the dashboard |
| `family` | string | discovery family or `Ungrouped` | family shown in the dashboard |
| `workspace` | string | discovery workspace or the repository | directory for the launcher placeholder `{workspace}`; a relative path is resolved against the repository, for example `".."` |
| `enabled` | boolean | `true` | `false` removes the repository, even if discovery finds it |
| `remote` | string | none | expected remote URL; a mismatch is shown as `≠remote` |
| `remote_name` | string | `origin` | remote that `remote` refers to; also selects the primary remote |
| `untracked` | `"normal"` or `"no"` | `[git].untracked` | `no` skips untracked-file detection for large repositories |
| `pinned_branches` | array of strings | `[branches].pinned` | replaces the global pinned branches; listed branches that do not exist are reported as `missing` |

## `[[discovery]]`

Bounded discovery below a root directory.

| Key | Type | Default | Description |
|---|---|---|---|
| `root` | string | required | directory to scan; missing roots produce a warning |
| `max_depth` | integer | `3` | number of directory levels below `root` that are scanned |
| `family` | string | `Ungrouped` | family of every discovered repository |
| `workspace` | string | the repository | `{workspace}` directory of every discovered repository; a relative path is resolved against each repository |
| `include` | array of strings | `["**"]` | a repository must match at least one pattern |
| `exclude` | array of strings | `[]` | matching directories are skipped and not traversed |
| `follow_symlinks` | boolean | `false` | follow symbolic links; loops are detected |

Rules:

- Patterns use doublestar glob syntax (`*`, `**`, `?`, `[...]`, `{a,b}`) and
  are matched case-sensitively against the slash-separated path relative to
  `root`, for example `company/api`.
- A pattern ending in `/**` also matches the directory itself.
- Discovery never descends into a repository. Nested repositories, including
  submodules, must be configured explicitly.
- Bare repositories are never discovered.
- If several discovery entries find the same repository, the first entry in
  the file supplies the family, and a warning is shown.

### Names

A repository is named after its directory unless `[[repositories]].name` is
set. If several repositories share a directory name, or a directory name
equals a configured name, those repositories are named by as many trailing
path components as needed to tell them apart, for example `hsm1/src` and
`hsm2/src`. Configured names are never changed.

### Precedence

| Explicit entry | Found by discovery | Result |
|---|---|---|
| none | yes | included with the discovery settings |
| `enabled = true` | no, excluded, or outside every root | included with the explicit settings |
| `enabled = true` | yes | included once; explicit fields override discovery fields |
| `enabled = false` | any | excluded |

## `[git]`

| Key | Type | Default | Description |
|---|---|---|---|
| `executable` | string | `git` | Git executable; version 2.34 or newer is required |
| `inspection_parallelism` | integer | `8` | concurrent inspections |
| `inspection_timeout` | duration | `"10s"` | timeout per inspection |
| `fetch_parallelism` | integer | `4` | concurrent fetches and updates |
| `fetch_timeout` | duration | `"120s"` | timeout per fetch or update |
| `fetch_prune` | boolean | `true` | add `--prune` to `git fetch --all` |
| `untracked` | `"normal"` or `"no"` | `"normal"` | default untracked-file detection |

Durations use Go syntax, for example `"500ms"`, `"10s"`, or `"2m"`.

## `[branches]`

Besides the checked-out branch, headquarters reports other local branches
(`refs/heads`) that need attention. The table shows only their number, for
example `⎇2`; the detail panel lists them. Row state, header counters `1`–`5`,
sorting, and the safe update keep referring to the checked-out branch only.

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | boolean | `true` | `false` reads only the checked-out branch |
| `pinned` | array of strings | `["main", "master"]` | branches that are always evaluated, regardless of `ignore` and `max_age`; missing ones are not reported |
| `ignore` | array of strings | `[]` | doublestar patterns of branch names that are not reported, for example `"archive/**"` |
| `max_age` | age | `"30d"` | branches whose latest commit is older are not reported; `"0"` disables the age filter |

A branch needs attention if it has no upstream, its upstream is gone, or it is
ahead of or behind its upstream. Branches checked out in any worktree are not
listed because the worktree has its own row. The marker is shown in the
warning color if at least one branch holds unpublished work (no upstream,
upstream gone, ahead, or missing), otherwise in the behind color.

Ages are a number of days such as `"30d"`, a Go duration such as `"36h"`, or
`"0"`.

## `[lazygit]`

| Key | Type | Default | Description |
|---|---|---|---|
| `command` | string | `lazygit` | executable name or path |
| `args` | array of strings | `[]` | additional arguments; no placeholders |
| `enabled` | boolean | `true` | `false` disables `Enter` |

lazygit starts in the worktree root of the selected repository.

Any terminal Git client that works on the repository in its current
directory can take lazygit's place, for example gitui or tig:

```toml
[lazygit]
command = "gitui"
```

## `[ui]`

| Key | Type | Default | Description |
|---|---|---|---|
| `icons` | `"unicode"`, `"nerd"`, or `"ascii"` | `"unicode"` | symbol set; `nerd` requires a Nerd Font, `ascii` suits limited terminals |
| `view` | `"list"` or `"tree"` | `"list"` | repository view at startup; `v` switches at runtime. After `R`, a changed value takes effect, otherwise the current view is kept |
| `sort` | `"name"`, `"family"`, `"commit"`, `"worktree"`, `"upstream"`, or `"health"` | `"name"` | sort order at startup; `health` puts the most urgent repositories on top. `s` chooses at runtime. After `R`, a changed value takes effect, otherwise the current sort order is kept |
| `detail_commits` | integer, 1–20 | `3` | recent commits of the selected repository in the detail panel; with `1`, only the latest commit is shown and no extra Git process runs |
| `incoming_commits` | integer, 0–50 | `10` | incoming upstream commits listed as of the last fetch in the detail panel, the update confirmation, and the operations view; more are summarized as `… and n more`. With `0`, only the counts are shown and no extra Git process runs |

## `[ui.theme]`

`preset` selects a built-in color scheme; the color keys below override
single colors of the preset.

| Preset | Description |
|---|---|
| `default` | the defaults listed below |
| `high-contrast` | bright ANSI colors and the terminal's default foreground for secondary text |
| `low-color` | only the eight basic ANSI colors, for limited terminals |
| `monochrome` | no hues; the selected row is inverted |
| `catppuccin` | `catppuccin-mocha` or `catppuccin-latte`, by the terminal background |
| `catppuccin-mocha` / `catppuccin-latte` | Catppuccin, dark / light background |
| `dracula` | Dracula, dark background |
| `gruvbox` | `gruvbox-dark` or `gruvbox-light`, by the terminal background |
| `gruvbox-dark` / `gruvbox-light` | Gruvbox, dark / light background |
| `monokai` | Monokai, dark background |
| `onedark` | One Dark, dark background |

The ANSI-based presets follow the terminal's palette and suit light and dark
backgrounds. The other presets use fixed `#rrggbb` colors and require a
terminal with true color support. `catppuccin` and `gruvbox` choose the
dark or light variant themselves: headquarters asks the terminal for its
background color once at startup and keeps the answer for reloads with
`R`. A terminal that does not answer, such as tmux
without passthrough, counts as dark; choose the variant explicitly there.
Color keys override both variants.

```toml
[ui.theme]
preset = "gruvbox"
selected_accent = "#fe8019"
```

Colors are ANSI names (`black`, `red`, `green`, `yellow`, `blue`, `magenta`,
`cyan`, `white`, `gray`, `bright-red`, …, `default`), numbers `0`–`255`, or
`#rrggbb`. Unset keys keep their defaults. If the `NO_COLOR` environment
variable is set, no colors are used; every state remains readable through
symbols and text.

| Key | Default | Used for |
|---|---|---|
| `active_border` | `green` | border and title of the focused panel or dialog |
| `inactive_border` | `gray` | other panel borders and the rules of detail sections |
| `title` | `green` | application title and detail section titles |
| `selected_background` | `237` | selected row |
| `selected_foreground` | `bright-white` | names, secondary text, and text in the terminal's default color in the selected row; other semantic colors are kept |
| `selected_accent` | `cyan` | cursor of the selected row |
| `header_background` | `24` | background of the table header |
| `header_foreground` | `bright-white` | titles in the table header |
| `key` | `cyan` | key hints, marks, running operations |
| `branch` | `cyan` | branch names |
| `clean` | `gray` | clean and up-to-date states |
| `dirty` | `yellow` | modified and deleted entries |
| `ahead` | `green` | commits ahead, staged entries |
| `behind` | `magenta` | commits behind |
| `success` | `green` | finished operations, progress bars, confirmations |
| `error` | `red` | conflicts, divergence, failures |
| `warning` | `yellow` | warnings, stale fetches, remote mismatch |
| `dim` | `gray` | secondary text |

## `[[launchers]]`

Launchers appear in the menu opened with `o`.

| Key | Type | Description |
|---|---|---|
| `name` | string | label in the menu |
| `command` | array of strings | executable and arguments |

No shell is involved. Placeholders must be complete arguments:

- `{path}` is replaced by the worktree root.
- `{workspace}` is replaced by the repository's `workspace`, or the worktree
  root if none is configured. Use it to open a directory that contains the
  repository, for example `hsm1` for the repository `hsm1/src`.

Launchers start in the worktree root, take over the terminal like lazygit,
and the repository is refreshed afterward.

```toml
[[discovery]]
root = "~/src/hsm1"
max_depth = 1
include = ["src"]
workspace = ".."

[[launchers]]
name = "VS Code"
command = ["code", "--new-window", "{workspace}"]
```

The built-in menu entry *Open remote in browser* converts the primary remote
to an HTTPS URL and opens it with `xdg-open` or `open`. For SSH remotes it
uses the remote host and repository path, dropping the SSH user and port.
This assumes the web interface is available on that host at the same path;
SSH host aliases are not resolved.
