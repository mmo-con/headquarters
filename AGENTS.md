# Agent instructions

headquarters is a Go terminal application for managing a collection of local
Git repositories. Before changing code, read the relevant decisions linked
from [docs/architecture.md](docs/architecture.md). The accepted ADRs are the
source of truth for design decisions and their constraints.

## Commands

```sh
make build              # ./bin/headquarters
make lint               # go vet and gofmt check
make vuln               # govulncheck; CI runs it with the newest stable Go
go test -race ./...     # all tests; CI also runs make coverage
make perf               # local performance benchmark
```

## Tests

- Add focused tests for new or fixed behavior to the existing test files.
- For temporary Git repositories, use the helpers in `internal/testutil`
  (`IsolateGit`, `InitRepo`, `Commit`, `Git`).
- TUI tests use `newModel`, `keys`, and `pump` (see
  `internal/tui/model_test.go` and `workflow_test.go`) and check rendered
  views with `plain` or `m.View()`.
- Follow the testing decisions in
  [ADR 0008](docs/adr/0008-compatibility-performance-and-testing.md).

## Documentation

Keep documentation consistent with the change:

| Change | Update |
|---|---|
| configuration option | `docs/configuration.md`, `examples/config.toml` |
| key, symbol, or filter | `docs/keybindings.md`, help view in `internal/tui/keymap.go`, legend in `internal/tui/icons.go` |
| `status` option, exit status, or report field | `docs/status.md` |
| design decision or constraint | the relevant ADR; follow [ADR maintenance](docs/adr/README.md) |
| package structure | `docs/architecture.md` |
| user-visible feature | `README.md` |

Documentation and code comments are written in English.
