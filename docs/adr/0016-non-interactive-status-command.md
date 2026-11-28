# ADR 0016: Non-interactive status command

Status: Accepted

## Context

The repository overview was only available in the terminal user interface.
Status bars, scripts, cron jobs, and other tools could not reuse it, for
example to check for uncommitted or unpushed work before shutting down a
machine ([#10](https://github.com/mmo-con/headquarters/issues/10)). Further
consumers are foreseeable: a read-only MCP server
([#46](https://github.com/mmo-con/headquarters/issues/46)) and possibly a
local backend for a browser interface. The inspection itself was driven by
the user interface, so a second consumer would have duplicated it.

## Decision

`headquarters status` collects the status of the configured repositories
without starting the user interface, prints it, and exits. It is a single
run, not a server: it accepts no commands while running and keeps no state
afterward ([ADR 0012](0012-no-persistent-state-and-no-automatic-activity.md)).

**Read-only.** The command inspects every repository of the inventory with
the inspection jobs of [ADR 0003](0003-git-cli-and-process-isolation.md). It
never updates, never changes a repository, and never fetches unless `--fetch`
is given. `--fetch` runs the fetch batch of
[ADR 0004](0004-batch-fetch-and-safe-update.md) with its parallelism,
timeout, and store deduplication, like `F` in the dashboard: the filter
evaluated before the fetch selects the targets, the fetched stores are
inspected again, and the output applies the filter to the state after the
fetch. A failed fetch is reported per repository, in the fetch result
and not again in the error of the entry, and classifies the repository as an
error, as in the health column.

**One implementation.** The inventory, the inspection, the fetch batch, the
filter language, the health classification, and the report model live in
`internal/app`. The user interface and the command are frontends of the same
services; `cmd/headquarters` only parses arguments, encodes the report, and
chooses the exit status. Services that keep batch state, such as the
operations and the inspection tracking, are owned by one goroutine, which
delivers the scheduler events of its batches to them. The user interface
uses its update loop; the command runs a synchronous loop that waits until
its batches are finished. A future long-running frontend follows the same
rule.

**Versioned output.** The report is JSON with a `schema` number, a summary,
and one entry per repository. Each entry lists the `--fail-on` states it is
in, so that consumers do not have to repeat rules such as the age of a stale
fetch; `--format ndjson` prints one repository per
line. Adding fields keeps the schema number; renaming, removing, or changing
the meaning of a field increments it. Unknown state is `null` and marks the
entry `known: false`, following
[ADR 0006](0006-domain-state-and-errors.md); it is never reported as clean or
up to date. Text from repositories passes through `domain.Printable`, remote
URLs through `domain.RedactURL`. Only the report goes to standard output;
warnings and errors go to standard error.

**Exit status.**

| Status | Meaning |
|---|---|
| 0 | report written, no `--fail-on` state found |
| 1 | runtime error, such as an unusable Git executable |
| 2 | usage error, invalid filter, or invalid configuration |
| 3 | report written, and a repository matches a `--fail-on` state |

`--fail-on` takes state names of the filter language (`dirty` for `is:dirty`)
and `unknown`, which matches every entry with `known: false`, including
unavailable entries and failed inspections; `error` also matches a failed
fetch. A state that is not determined, such as the worktree of a repository
whose index cannot be read, therefore fails an `unknown` check instead of
passing a `dirty` check unnoticed.

**No coordination across processes.** Store locks and the batch limits apply
within one process. A dashboard and a `status --fetch` running at the same
time can fetch the same store; Git's own ref locks protect the repository, and
the losing fetch is reported as failed.

## Consequences

- Scripts and status bars get the same state, filter results, and health as
  the dashboard, and an exit status suitable for `--fail-on` checks.
- Running the command inspects every repository once, which costs the same
  local Git processes as starting the dashboard.
- Other frontends, such as an MCP server or a local web backend, can reuse
  the collection and the report model. Accepting commands, streaming
  progress, or serving several clients requires a new ADR that defines the
  protocol, its security, and the ownership of the shared services.
