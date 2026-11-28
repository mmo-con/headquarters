# headquarters — Architecture

headquarters manages a collection of local Git repositories and hands
single-repository work over to lazygit. This page is a map of the code and
its architecture decisions; the ADRs are the source of truth for the
decisions, their rationale, and their constraints.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/headquarters` | flags, configuration lookup, version check, `--check`, `init`, `status` |
| `internal/config` | TOML decoding, defaults, paths, validation, themes |
| `internal/inventory` | discovery, patterns, identity, precedence, validation states |
| `internal/initconfig` | one-time scan and generation of a starting configuration, atomic file writing |
| `internal/git` | isolated Git command runner, parsers, inspection, fetch, update, branch deletion |
| `internal/jobs` | scheduler, timeouts, store locks, event queue |
| `internal/app` | application services, inspections, operations, branch cleanup, handover, health classification, filters, sorting, status collection and report |
| `internal/launcher` | lazygit, custom launchers, browser |
| `internal/clipboard` | native clipboard tools, value checks |
| `internal/domain` | repository and operation models |
| `internal/tui` | Bubble Tea model, views, layout, theme, icons, key map |
| `internal/testutil` | real Git repositories for tests |

The TUI and the `status` command are frontends of the same services in
`internal/app`. The `status` command is read-only: it inspects the
repositories once and fetches only with `--fetch`; it never updates or
changes a repository ([ADR 0016](adr/0016-non-interactive-status-command.md)).

## Architecture decision records

The [ADR index](adr/README.md) explains how the records are maintained.

| Topic | Record |
|---|---|
| Product boundaries, technology, and configuration | [ADR 0001](adr/0001-product-boundaries-and-technology.md) |
| Inventory, identity, and repository stores | [ADR 0002](adr/0002-inventory-and-repository-identity.md) |
| Git commands, parsing, and process isolation | [ADR 0003](adr/0003-git-cli-and-process-isolation.md) |
| Fetch and safe updates | [ADR 0004](adr/0004-batch-fetch-and-safe-update.md) |
| Terminal handover and job coordination | [ADR 0005](adr/0005-terminal-handover-and-job-coordination.md) |
| Domain state and error handling | [ADR 0006](adr/0006-domain-state-and-errors.md) |
| User interface principles | [ADR 0007](adr/0007-user-interface-principles.md) |
| Compatibility, performance, and testing | [ADR 0008](adr/0008-compatibility-performance-and-testing.md) |
| Generating a starting configuration | [ADR 0009](adr/0009-starting-configuration-generation.md) |
| Copying values to the clipboard | [ADR 0010](adr/0010-copying-to-the-clipboard.md) |
| Families in the tree | [ADR 0011](adr/0011-families-in-the-tree.md) |
| No persistent state and no automatic activity | [ADR 0012](adr/0012-no-persistent-state-and-no-automatic-activity.md) |
| Calmer table and detail panel | [ADR 0013](adr/0013-calmer-table-and-detail-panel.md) |
| Dependency security and Go toolchain | [ADR 0014](adr/0014-dependency-security-and-go-toolchain.md) |
| Showing incoming commits | [ADR 0015](adr/0015-showing-incoming-commits.md) |
| Non-interactive status command | [ADR 0016](adr/0016-non-interactive-status-command.md) |
| Detail panel order and commit lines | [ADR 0017](adr/0017-detail-panel-order-and-commit-lines.md) |
| Deleting merged branches | [ADR 0018](adr/0018-deleting-merged-branches.md) |
| Deleting squash- and rebase-merged branches | [ADR 0019](adr/0019-deleting-squash-and-rebase-merged-branches.md) |
| Branches of rebases and bisects | [ADR 0020](adr/0020-branches-of-rebases-and-bisects.md) |
| Untrusted repository configuration | [ADR 0021](adr/0021-untrusted-repository-configuration.md) |
| Keeping branches that exist on the server | [ADR 0022](adr/0022-keeping-branches-that-exist-on-the-server.md) |
| Fetch hint in the branch cleanup | [ADR 0023](adr/0023-fetch-hint-in-the-branch-cleanup.md) |

User-facing configuration and keys are documented in
[configuration.md](configuration.md) and [keybindings.md](keybindings.md).
