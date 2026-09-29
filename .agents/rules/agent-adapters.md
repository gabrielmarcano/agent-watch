---
trigger: glob
glob: "pkg/agents/**"
description: "Rules for agent-specific adapters: menus, keys, transcripts"
---

# Agent adapter rules

> Applies to: pkg/agents/**.

Facts per agent: `docs/reference/agents.md`. How-to: the `add-agent-adapter` and `capture-fixture` skills. The cross-cutting rules (agent knowledge only in `pkg/agents`, roles from labels) are in `AGENTS.md` §1.2.

- **`ParsePrompt` must be right both ways.** A missed dialog cannot be answered from the watch; a false one (a numbered list in an answer) makes the bridge refuse dictation with `agent_blocked`. Hence menus count only while their dialog is open, and the `no-menu-*-numbered-list` fixtures.
- **No menu parsed → `ok=false`.** The bridge publishes `kind=unknown` and the watch offers only Cancel. Never guess an Allow.
- **Every behaviour change needs a test that fails without it**: a real fixture in `testdata/<agent>/` (captured with the `capture-fixture` skill; `--format ansi` for focus fixtures) or a synthetic case in `testdata/generic/`.
- **Priority agents:** `claude`, `agy`, `opencode`. Everything else must keep working through `generic`, so do not special-case other agents inside generic code.
- **Transcript readers:**
  - bounded tail reads (256 KiB; Claude grows to 1 MiB and 4 MiB when a turn starts further back) and read-only access (the OpenCode DB opens with `mode=ro`);
  - `ErrNoTranscript` on any failure, never a panic (the bridge then falls back to a screen capture);
  - responses truncated with `model.TruncateUTF8(…, 16384)`.
- **No imports of `pkg/herdr`**, and no network I/O.
- **Fixtures are scrubbed:** no home paths, usernames, emails or tokens. The pre-commit hook scans only for keys and tokens; check the rest by hand (the `capture-fixture` skill's scrub check).
