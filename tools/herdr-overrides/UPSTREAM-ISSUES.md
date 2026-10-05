# Draft issue for herdr

Draft from a sandbox investigation with herdr 0.9.1; whether it is filed: `docs/STATUS.md`. (The agy draft was dropped on 2026-10-05: upstream manifest `agy` 2026.10.05.1 fixed it.)

Once upstream fixes it, run `tools/herdr-overrides/herdr-overrides.sh check` and uninstall the override (see [`README.md`](README.md)).

---

## Draft — Claude permission dialog reported `idle` when an old input box is still on screen

**Versions:** herdr 0.9.1, agent manifest `claude` 2026.09.11.1, Claude Code 2.1.x (macOS).

**Steps:**
1. In a pane, run `claude`, do anything, then `/exit`.
2. Run `claude` again in the same pane, so the previous session's input box is still in the visible rows.
3. Trigger any permission dialog (e.g. `touch foo.txt`, or a WebFetch).

**What happens:** herdr reports `idle`. `herdr agent explain` shows `live_prompt_box` (priority 950) matching the region between the last two horizontal rules, which now spans the **old** input box. Claude hides its own input box while a dialog is open, so the stale one wins.

**Related, same root cause (stale rows):**
- A freshly relaunched, untouched Claude is reported `working`: `live_turn_working` finds the old session's `esc to interrupt` among the last 12 non-empty lines.
- WebFetch dialogs have no "Do you want to proceed?"/"Esc to cancel" in some layouts, so only `legacy_no_prompt_blocker` (priority 300) catches them, and it fails whenever any bare `❯` line is on screen.

**Integrations can't work around it:** Claude's `PermissionRequest` hook fires before the dialog, but `herdr:claude` is a reserved native source whose reported state is discarded; a manual "No" fires no hook, so nothing could release the state anyway.

**Candidate rule** (validated against captured Bash/Edit/Write/WebFetch/plan-approval dialogs, with and without a stale box, and against idle/working screens including a typed `❯` prompt):

```toml
[[rules]]
id = "live_permission_dialog"
state = "blocked"
priority = 990
region = "after_last_horizontal_rule"
visible_blocker = true
line_regex = ['^\s*❯\s*[1-9]\.\s+\S']
all = [ { line_regex = ['(?i)^\s*(?:❯\s*)?[1-9]\.\s*(?:yes|no)\b'] } ]
```
