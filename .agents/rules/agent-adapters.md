---
trigger: glob
glob: "pkg/agents/**"
paths:
  - "pkg/agents/**"
description: "Rules for agent-specific adapters: menus, keys, transcripts"
---

# Agent adapter rules

> Applies to: pkg/agents/**.

Facts per agent: `docs/reference/agents.md`. How-to: the `add-agent-adapter` and `capture-fixture` skills. The cross-cutting rules (agent knowledge only in `pkg/agents`, roles from labels) are in `AGENTS.md` §1.2.

- **`ParsePrompt` must be right both ways.** A missed dialog cannot be answered from the watch; a false one (a numbered list in an answer) makes the bridge refuse dictation with `agent_blocked`. Hence menus count only while their dialog is open, and the `no-menu-*-numbered-list` fixtures.
- **No menu parsed → `ok=false`.** The bridge publishes `kind=unknown` and the watch offers only Cancel. Never guess an Allow.
- **Every behaviour change needs a test that fails without it**: a real fixture in `testdata/<agent>/` (captured with the `capture-fixture` skill; `--format ansi` for focus fixtures) or a synthetic case in `testdata/generic/`.
- **Every agent but the priority ones (`AGENTS.md` §1.2) must keep working through `generic`,** so do not special-case other agents inside generic code.
- **Transcript readers:**
  - bounded tail reads (sizes per agent: `docs/reference/agents.md`) and read-only access (the OpenCode DB opens with `mode=ro`);
  - `ErrNoTranscript` on any read or parse failure (a cancelled context returns `ctx.Err()`), never a panic; the bridge falls back to a screen capture on any error;
  - responses truncated with `model.TruncateUTF8` to the limit in `contracts.md` §1.4.
- **No imports of `pkg/herdr`**, and no network I/O.
- **Fixtures are scrubbed:** no home paths, usernames, emails or tokens. The pre-commit hook scans only for keys and tokens; check the rest by hand (the `capture-fixture` skill's scrub check).
