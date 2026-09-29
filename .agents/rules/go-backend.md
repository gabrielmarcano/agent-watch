---
trigger: glob
glob: "**/*.go"
paths:
  - "**/*.go"
description: "Go conventions for the bridge, relay and shared packages"
---

# Go backend conventions

> Applies to: every Go file.

Cross-cutting rules: `AGENTS.md`. Shapes and values: the relay protocol in `docs/reference/contracts.md`, the herdr side in `docs/reference/herdr-socket-api.md`, transcript reads in `docs/reference/agents.md`.

## Language and dependencies
- **The `go` line of `go.mod` is the floor.** Use nothing newer (e.g. `net/http` routing patterns and `r.PathValue` are within it) without raising that line.
- **Standard library first.** Only these modules are approved: `github.com/coder/websocket`, `github.com/BurntSushi/toml`, `modernc.org/sqlite`, `golang.org/x/oauth2`. Anything else needs the owner's approval.
- **Binaries must build with `CGO_ENABLED=0`.** Never add a cgo dependency (e.g. `mattn/go-sqlite3`).

## Style
- Explicit errors, wrapped with context: `fmt.Errorf("herdr read %s: %w", pane, err)`. **No panics in daemons**: recover nothing, just don't panic.
- `context.Context` is the first parameter of anything that does I/O. Respect cancellation and set timeouts at the call site.
- Logging with `log/slog`, within `AGENTS.md` §3 (no tokens, headers, prompt or transcript text).
- JSON types that cross a process boundary live **only** in `pkg/model` and match `docs/reference/contracts.md`. Exceptions: the bridge's local files and CLI output (contracts §6, `pkg/bridge`, `cmd/bridge`), the push payloads (contracts §4, `pkg/push`), the relay's admin socket (`pkg/relay/admin.go`), and herdr's own shapes (`pkg/herdr`, `herdr-socket-api.md`).
- Timestamps only via `model.Now()` (RFC 3339 UTC).
- Every long-lived connection reconnects with exponential backoff and jitter (the relay WebSocket: `contracts.md` §3; herdr: `herdr-socket-api.md` §7).
- Package boundaries:
  - `pkg/herdr` knows no agent names;
  - `pkg/agents` does not import `pkg/herdr`;
  - the relay does not import `pkg/herdr` or `pkg/agents`.

## Tests
- Table-driven, `go test -race ./...`, no network, no real herdr socket: use `pkg/herdrtest`, `httptest` and temp dirs.
- Wait for conditions with a polling helper and a deadline; never use a `time.Sleep` longer than 50 ms.
- The pre-commit hook rejects unformatted Go (OpenCode also runs gofmt after each edit). Run `gofmt -w` and `go vet ./...` before finishing.

## Before claiming done
```bash
gofmt -l cmd pkg deploy   # empty
go vet ./...
go test -race ./...
CGO_ENABLED=0 go build ./...   # not `make build`: it replaces bin/agent-watch-bridge, which the owner's service runs
```
