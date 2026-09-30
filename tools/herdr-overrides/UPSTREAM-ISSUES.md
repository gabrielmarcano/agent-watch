# Draft issues for herdr

Drafts from a sandbox investigation with herdr 0.9.1; whether they are filed: `docs/STATUS.md`. There is already an upstream issue about agy dialogs; draft 1 adds the root cause and a candidate rule to it.

Once upstream fixes either one, run `tools/herdr-overrides/herdr-overrides.sh check` and uninstall the matching override (see [`README.md`](README.md)).

---

## Draft 1 — Antigravity CLI 1.2.x permission dialogs are never detected as `blocked`

**Versions:** herdr 0.9.1, agent manifest `agy` 2026.06.24.1, Antigravity CLI 1.2.10 and 1.2.11 (macOS).

**What happens:** while agy shows a permission dialog, herdr reports the pane as `done`/`idle`, or `working` when a background task is running. `herdr agent explain` shows no matching rule (`default_known_agent_idle_fallback`) or `background_tasks_working`.

**Why:** the `permission_prompt` rule requires `requesting permission for:` and then either `do you want to proceed?`, or both `tab amend` and `edit command`. agy 1.2.x renders neither:

```
Requesting permission for:
   touch aw1.txt

Run this command?                  (file edits: "Accept this file edit?")
> 1. Yes, run command
  2. Yes, and always allow in this conversation for commands that start with 'touch'
  3. Yes, and always allow for commands that start with 'touch' (Persist to settings.json)
  4. No, cancel

  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
esc to cancel
```

`ctrl+g edit/expand command` does not contain `edit command`, and there is no "Do you want to proceed?".

**Integrations can't work around it:** agy's hooks (`PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`) have no permission event, and `pane.report_agent` with source `herdr:antigravity_cli` is identity-only, so its state is ignored.

**Candidate rule** (validated against captured dialogs and idle/working screens, including an idle screen whose last answer contains a numbered list):

```toml
[[rules]]
id = "permission_dialog_1_2"
state = "blocked"
priority = 310
region = "bottom_non_empty_lines(12)"
visible_blocker = true
regex = ['(?m)^\s*(?:>\s*)?[1-9]\.\s+\S']
all = [ { regex = ['(?im)^\s*↑/↓\s+navigate\s+·\s+tab\s+amend\b[^\n]*\n\s*esc to cancel[^\n]*\s*\z'] } ]
```

---

## Draft 2 — Claude permission dialog reported `idle` when an old input box is still on screen

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
