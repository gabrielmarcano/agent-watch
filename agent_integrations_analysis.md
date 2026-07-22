# Agent Integrations Analysis: Claude Code & Google Antigravity (AGY) -> Wear OS

This document details how we achieved seamless, unified support for both major AI coding agents (Claude Code and Antigravity) by leveraging their respective architectural strengths, allowing them to communicate with our Wear OS watch app.

## 1. Claude Code Integration (Native Plugin Hooks)

Claude Code exposes a native plugin system that triggers shell scripts on specific events. We built a custom local plugin (`claude-plugin/`) to intercept these events.

### The 6 Available Hooks in Claude Code

| Hook | Event | JSON Payload (stdin) | Our Use Case |
|---|---|---|---|
| `SessionStart` | Claude starts/resumes | `session_id`, `cwd` | Notify watch: "Agent Started" |
| `Stop` | Claude finished responding | `session_id`, `cwd`, `transcript_path` | Notify watch: "Waiting for Input" |
| `Notification` | Claude is idle waiting for input | `session_id`, `notification_type` | Notify watch: "Input Needed" |
| `PermissionRequest` | Claude needs tool permission | `session_id`, `tool_name`, `tool_input` | Notify watch: "Asking Permission" |
| `UserPromptSubmit` | User sent a prompt | `session_id`, `prompt` | Notify watch: "Thinking..." |
| `PostToolUse` | A tool finished running | `session_id`, `tool_name` | Notify watch: "Thinking..." |

**How it works:**
Whenever one of these hooks fires, our `claude-plugin/scripts/send-to-bridge.sh` executes a simple `curl POST` request to our local Node.js bridge (`http://localhost:8420/webhook`).

---

## 2. Google Antigravity (AGY) Integration (Sidecar)

Unlike Claude Code, Antigravity does not rely on a simple shell-hook plugin system for output. However, it maintains an extensive, real-time "brain" file called `transcript.jsonl` containing every state and thought process.

To integrate AGY without modifying its core, we built a **Sidecar** process (`bridge/agy-sidecar.js`).

### How the AGY Sidecar Works

1. **Auto-Discovery**: The sidecar scans `~/.gemini/antigravity-cli/brain/` for the most recently modified session directory.
2. **Real-Time Tailing**: It establishes a lightweight, non-blocking file stream (`fs.createReadStream`) that polls the end of `transcript.jsonl` every 500ms for new bytes.
3. **Event Mapping**: 
   - `USER_INPUT` -> Mapped to `SessionStart`.
   - `PLANNER_RESPONSE` (with `tool_calls`) -> Mapped to `PostToolUse` (Agent is thinking/working).
   - `PLANNER_RESPONSE` (without `tool_calls`) -> Mapped to `Stop` (Agent is waiting for human).

Whenever the Sidecar detects these JSON states, it fires the exact same HTTP POST requests to `http://localhost:8420/webhook`. 

---

## 3. The Bridge Server (Single Source of Truth)

Because both the Claude Plugin and the AGY Sidecar normalize their events into identical HTTP Webhooks, the Node.js Bridge Server (`bridge/server.js`) remains completely agnostic. 

It simply receives the webhook, formats it into a Server-Sent Event (SSE), and pushes it to the Wear OS watch over the local network. 

When the user dictates a voice response on the watch, the Bridge receives it and uses AppleScript to dynamically type the input into the `target_terminal` specified in `config.json` (e.g., Warp or iTerm2), seamlessly completing the loop for either agent.
