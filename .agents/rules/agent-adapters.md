---
trigger: glob
glob: "pkg/agents/**"
description: "Rules for agent-specific adapters: menus, keys, transcripts"
---

# Agent adapter rules

> Applies to: pkg/agents/**.

Spec: `docs/reference/agents.md`. Guide: `docs/phases/2b-agent-adapters.md`. How-to: `.agents/skills/add-agent-adapter/SKILL.md`.

- **All agent-specific knowledge lives here and nowhere else**: menus, keys, cancel, transcript formats, and whether a prompt can be queued while working.
- **Roles come from option labels, never positions.** In Claude Code, `2` is "Yes, and don't ask again". Treating it as "No" grants permanent permission.
- **No menu parsed → `ok=false`.** The bridge publishes `kind=unknown` and the watch offers only Cancel. Never guess an Allow.
- **Every behaviour change needs a test that fails without it**: a real fixture in `testdata/<agent>/` (captured with the `capture-fixture` skill) or a synthetic case in `testdata/generic/`.
- **Priority agents:** `claude`, `agy`, `opencode`. Everything else must keep working through `generic`, so do not special-case other agents inside generic code.
- **Transcript readers:**
  - bounded tail reads (256 KB) and read-only access (the OpenCode DB opens with `mode=ro`);
  - `ErrNoTranscript` on any failure, never a panic;
  - responses truncated with `model.TruncateUTF8(…, 16384)`.
- **No imports of `pkg/herdr`**, and no network I/O.
- **Fixtures are scrubbed:** no home paths, usernames, emails or tokens. The pre-commit hook scans them.
