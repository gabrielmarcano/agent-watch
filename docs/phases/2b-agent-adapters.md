# Phase 2b — `pkg/agents`: Prompt Parsing, Key Mapping, Transcript Readers

> **Goal:** all agent-specific knowledge, as pure, fixture-tested functions. Given a screen, return the menu (options with roles) and the keys for each option. Given a session reference, return the last turn (query + response).

| | |
|---|---|
| **Depends on** | Phase 1 (types). Phase 0 fixtures for the claude/agy/opencode adapters. The generic parser can start before Phase 0 ends |
| **Parallel with** | 2a, 3a, 3b, 4 |
| **Touches** | `pkg/agents/**` (except `testdata/`, which Phase 0 owns — you may **add** synthetic test inputs under `testdata/generic/`), `go.mod`/`go.sum` (sqlite dependency), `docs/STATUS.md` |
| **Must not** | import `pkg/herdr` or do any I/O except reading transcript files / the OpenCode DB |

---

## Read first

- [`docs/reference/agents.md`](../reference/agents.md): the complete spec of what each adapter does.
- [`docs/reference/contracts.md`](../reference/contracts.md) §1.3–§1.4 (`PendingPrompt`, `HistoryItem`, fingerprint and id rules).
- The fixtures in `pkg/agents/testdata/<agent>/` and their `.golden.json` files.

---

## Files

| File | Responsibility |
|---|---|
| `adapter.go` | `Adapter` interface, `Prompt`, `SessionRef`, `ErrNoTranscript`, `Registry` |
| `menu.go` | Generic menu parser + role classifier + default key resolution |
| `generic.go` | `genericAdapter` (the fallback) |
| `claude.go` | `claudeAdapter` (prompt quirks + JSONL transcript) |
| `agy.go` | `agyAdapter` (prompt quirks + `transcript_full.jsonl`) |
| `opencode.go` | `opencodeAdapter` (prompt quirks + SQLite reader) |
| `screen.go` | Screen-capture history fallback formatter (`agents.md` §6) |
| `tail.go` | Bounded tail reader (last N bytes, drop the first partial line) |
| `*_test.go` | Fixture-driven tests |

---

## Steps

### 1. `adapter.go`

```go
package agents

var ErrNoTranscript = errors.New("no transcript available")

// SessionRef is what herdr knows about the agent's session (already filtered by TrustedSession).
type SessionRef struct {
    Agent string // herdr agent id
    Kind  string // "id" | "path"
    Value string
    CWD   string // pane cwd; Claude uses it to build the project slug
}

// Prompt = public model + private key map. Keys never leave the Mac.
type Prompt struct {
    Public model.PendingPrompt
    Keys   map[string][]string // option id → keys
}

type Adapter interface {
    Name() string
    ParsePrompt(screen string) (Prompt, bool)
    CancelKeys() []string
    PromptWhileWorking() bool
    LastTurn(ctx context.Context, ref SessionRef) (*model.HistoryItem, error)
}

type Config struct {
    ClaudeConfigDirs []string // expanded (no "~")
    OpenCodeDBPath   string   // default ~/.local/share/opencode/opencode.db
    AgyBrainDir      string   // default ~/.gemini/antigravity-cli/brain
}

type Registry struct{ /* map[string]Adapter + generic */ }

func NewRegistry(cfg Config) *Registry
func (r *Registry) For(agent string) Adapter // exact match, else generic

// UnknownPrompt builds the Kind=unknown prompt with RawTail (last 12 non-empty lines, right-trimmed).
func UnknownPrompt(screen string) Prompt
```

`LastTurn` returns a `HistoryItem` with `Query`, `Response`, `Source="transcript"` and a session value. The **caller** (bridge) fills `ID`, `PaneID`, `Agent`, `Label` and `CompletedAt`. Return the session value through an extra field or a small wrapper struct; your choice, but document it.

### 2. `menu.go` — generic parser

Implement the rules in `agents.md` §2 exactly. Suggested decomposition:

```go
type menuOption struct {
    Number int
    Label  string
    Cursor bool
}

type parsedMenu struct {
    Title   string
    Detail  string
    Options []menuOption
}

func findMenu(screen string) (parsedMenu, bool)          // last numbered block, continuation lines, cursor
func classify(label string) model.OptionRole             // role by label keywords
func kindFor(options []model.PromptOption) model.PromptKind
func buildPrompt(m parsedMenu, keysFor func(o menuOption, idx int, m parsedMenu) []string) Prompt
func digitKeys(o menuOption, _ int, _ parsedMenu) []string                 // ["<n>"]
func arrowKeys(o menuOption, idx int, m parsedMenu) []string               // Up/Down from cursor + "Enter"
```

`buildPrompt`:
- sets `id = "opt-<Number>"`;
- sets `Kind` via `kindFor`;
- computes `Fingerprint` with `model.Fingerprint`;
- initialises `Options` to a non-nil slice.

**Screen normalisation** before parsing:
- Split on `\n`.
- Right-trim each line.
- Strip any leftover ANSI escapes with the regex `\x1b\[[0-9;?]*[A-Za-z]`. `format:text` should not contain them; this is defence in depth.

### 3. Adapters

Each adapter embeds `genericAdapter` and overrides only what `agents.md` (after Phase 0) says differs:

| Adapter | Override |
|---|---|
| `claude` | `PromptWhileWorking() = true`; `LastTurn` = JSONL algorithm (`agents.md` §3.2); menu keys per fixtures |
| `agy` | `LastTurn` = `transcript_full.jsonl` algorithm (§4.2); keys and cancel per fixtures |
| `opencode` | `LastTurn` = SQLite algorithm (§5.2); keys and cancel per fixtures (may be `arrowKeys`) |

**Dialog rule (added 2026-09-25):** the `claude`, `agy` and `opencode` adapters parse a menu only while their dialog is open (the numbered block at the bottom, in place of the input box, or OpenCode's `┃` frame; `agents.md` §3.1, §4.1, §5.1). A numbered list in an answer is not a menu: the bridge refuses dictation while `ParsePrompt` finds one, so a false positive would block the agent. The `no-menu-*-numbered-list` fixtures cover it.

**Transcript rules** (all readers):
- Use `tail.go` to read at most 256 KB from the end of the file.
- Treat a missing file, an unparseable line, an empty result or any SQL error as `ErrNoTranscript`. **Never panic.**
- Respect `ctx`: check `ctx.Err()` between lines/rows.
- Apply `model.TruncateUTF8(response, 16384)`.

**Claude file resolution** (`kind="id"`):
1. For each dir in `ClaudeConfigDirs`, try `<dir>/projects/<slug(cwd)>/<id>.jsonl`, where `slug` replaces `/` and `.` with `-`.
2. If not found, `filepath.Glob("<dir>/projects/*/<id>.jsonl")`.
3. If `kind="path"`, use `Value` directly.

**OpenCode:**

```bash
go get modernc.org/sqlite
```

Open the DB with `sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")`. Open it per call and close it afterwards: it is rare and cheap. **Never** write to it.

### 4. `screen.go`

```go
// ScreenTurn formats agent.read(recent_unwrapped) output per agents.md §6.
func ScreenTurn(text string) *model.HistoryItem // Source="screen", Query=""
```

### 5. Tests

**Golden fixture test** (table-driven, `adapters_test.go`):

```go
// For every pkg/agents/testdata/<agent>/*.txt that has a sibling .golden.json:
//   p, ok := registry.For(agent).ParsePrompt(screen)
//   if golden.prompt == null → require !ok
//   else → require ok; compare Kind/Title/Detail/Options (id, label, role) and Keys and CancelKeys()
//   and require p.Public.Fingerprint == model.Fingerprint(...) of the golden values.
```

**Generic parser unit tests** (`menu_test.go`), using synthetic screens you write under `testdata/generic/`:
- a menu with a `❯` cursor on option 2 → `arrowKeys` for option 3 is `["Down","Enter"]`;
- continuation lines are appended to the label;
- a menu inside a box (`│ 1. Yes │`) → labels without box characters;
- two numbered blocks on screen → the **last** one wins;
- a numbered list in normal output followed by a real menu → the menu wins;
- `classify` for every keyword in `agents.md` §2, plus that a `Yes` label containing `don't ask again` is `allow_always`, not `allow_once`;
- no menu → `ok=false`, and `UnknownPrompt` has ≤ 12 lines and an empty non-nil `Options`.

**Transcript tests:**
- `claude`: `testdata/claude/transcript.jsonl` → equals `transcript.expected.json`. Add a synthetic case where the last `user` line is a `tool_result` (it must be skipped) and a case with `isSidechain` lines.
- `agy`: `testdata/agy/transcript_full.jsonl` → expected. Add a synthetic case where the last `PLANNER_RESPONSE` has `tool_calls` (skipped).
- `opencode`: build a temp SQLite DB in the test from `testdata/opencode/session.json`, using the same `CREATE TABLE` statements as `agents.md` §5.2, then assert against the expected result. Add a case with a `reasoning` part (skipped).
- Missing file / bad JSON / missing DB → `ErrNoTranscript`, no panic.
- Truncation: a 20 KB response → ≤ 16 384 bytes + marker.

**Rule enforced by review:** any change to `menu.go` or an adapter must come with a fixture or synthetic test that fails without the change.

---

## Definition of done

- [ ] `go test -race ./pkg/agents/...` passes, including every fixture from Phase 0.
- [ ] `go vet ./...` passes; `gofmt -l pkg` is empty.
- [ ] `pkg/agents` does not import `pkg/herdr` (`go list -deps ./pkg/agents | grep pkg/herdr` prints nothing).
- [ ] The binary still builds with `CGO_ENABLED=0` (proves the SQLite driver is pure Go).
- [ ] Commit only `pkg/agents`, `go.mod`, `go.sum`, `docs/STATUS.md`.

---

## Pitfalls

- **Position-based roles.** Tests must include a menu where "No" is option 2, so position-based code fails.
- **`\r` characters** in captures: normalise `\r\n` to `\n` first.
- **Unicode markers** (`❯`, `›`) are multi-byte. Use `strings`/`regexp` on strings, never byte indexes.
- **Reading the whole transcript:** real Claude transcripts reach hundreds of MB. Always tail.

---

## Prompt for the executing agent

```
You are executing Phase 2b (pkg/agents) of the Agent Watch refactor in /Users/me/Code/personal/agent-watch-herdr.
Read AGENTS.md, docs/reference/agents.md, docs/reference/contracts.md §1.3–1.4 and docs/phases/2b-agent-adapters.md,
then implement the package exactly as specified. Every behaviour must be driven by the fixtures in
pkg/agents/testdata (captured in Phase 0) or by synthetic cases you add under testdata/generic. Never infer
option roles from their position. Do not import pkg/herdr and do not talk to the herdr socket. Keep the
build CGO-free (modernc.org/sqlite). Run `go vet ./... && go test -race ./pkg/agents/...`, paste the output,
tick Phase 2b in docs/STATUS.md, and commit only pkg/agents, go.mod, go.sum and docs/STATUS.md.
```
