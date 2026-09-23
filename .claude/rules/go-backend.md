---
paths:
  - "**/*.go"
  - "go.mod"
  - "go.sum"
  - "Makefile"
---

# Go backend conventions

Applies to every Go file (bridge, relay, shared packages). Design: `HERDR_REFACTOR_PLAN.md`. Phase guides: `docs/phases/`.

## Language and dependencies
- **Go 1.22 is the floor** (`go.mod` says `go 1.22`). Use `net/http` routing patterns and `r.PathValue`, and nothing newer without raising the `go` line.
- **Standard library first.** Only these modules are approved: `github.com/coder/websocket`, `github.com/BurntSushi/toml`, `modernc.org/sqlite`, `golang.org/x/oauth2`. Anything else needs the owner's approval.
- **Binaries must build with `CGO_ENABLED=0`.** Never add a cgo dependency (e.g. `mattn/go-sqlite3`).

## Style
- Explicit errors, wrapped with context: `fmt.Errorf("herdr read %s: %w", pane, err)`. **No panics in daemons**: recover nothing, just don't panic.
- `context.Context` is the first parameter of anything that does I/O. Respect cancellation and set timeouts at the call site.
- Logging with `log/slog`. **Never log** tokens, `Authorization` headers, prompt text or transcript content. Log lengths and ids instead.
- JSON types that cross a process boundary live **only** in `pkg/model` and match `docs/reference/contracts.md`.
- Timestamps only via `model.Now()` (RFC 3339 UTC).
- Package boundaries:
  - `pkg/herdr` knows no agent names;
  - `pkg/agents` does not import `pkg/herdr`;
  - the relay does not import `pkg/herdr` or `pkg/agents`.

## Tests
- Table-driven, `go test -race ./...`, no network, no real herdr socket: use `pkg/herdrtest`, `httptest` and temp dirs.
- Wait for conditions with a polling helper and a deadline; never use a `time.Sleep` longer than 50 ms.
- A gofmt hook formats edited files automatically. Still run `go vet ./...` before finishing.

## Before claiming done
```bash
gofmt -l cmd pkg   # empty
go vet ./...
go test -race ./...
make build
```
