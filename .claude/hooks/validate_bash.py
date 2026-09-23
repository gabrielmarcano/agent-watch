#!/usr/bin/env python3
"""PreToolUse hook (matcher: Bash) for the Agent Watch repo.

Blocks (exit 2, reason on stderr) commands that are dangerous in this project:

1. Input to live herdr panes. The development Mac runs the owner's REAL agent
   sessions in herdr; sending "1" to one of them approves whatever it asked.
   Mutating herdr commands are allowed only when the target pane belongs to the
   workspace labelled "aw-sandbox" (verified live via `herdr pane get` +
   `herdr workspace list`, so it cannot be bypassed with an env marker).
2. Raw writes to the herdr socket with mutating methods (use the CLI so this
   hook can check the target).
3. Owner-only herdr operations: server stop, integration install/uninstall.
4. Unsafe git in a shared worktree: add -A/--all/., commit --amend, push
   (push is allowed with AW_OWNER_APPROVED_PUSH=1 when the owner asked for it).

Mentioning a command is not running it: heredoc bodies are ignored and the
command is tokenised with shlex, so commit messages and grep patterns that
contain these words do not trigger the guard.
"""
import json
import os
import re
import shlex
import subprocess
import sys

SANDBOX_LABEL = "aw-sandbox"

# herdr <group> <sub> that send input or change panes/agents → need a sandbox target.
TARGETED = {
    ("agent", "prompt"), ("agent", "send-keys"), ("agent", "start"), ("agent", "rename"),
    ("agent", "focus"), ("agent", "attach"),
    ("pane", "send-keys"), ("pane", "send-text"), ("pane", "run"), ("pane", "close"),
    ("pane", "split"), ("pane", "input"), ("pane", "move"), ("pane", "swap"),
    ("pane", "rename"), ("pane", "zoom"), ("pane", "release-agent"),
    ("pane", "report-agent"), ("pane", "report-agent-session"), ("pane", "report-metadata"),
}
OWNER_ONLY = {
    ("server", "stop"): "stops herdr and every agent session of the owner",
    ("integration", "install"): "rewrites hooks in the owner's agent configuration",
    ("integration", "uninstall"): "removes hooks from the owner's agent configuration",
    ("workspace", "close"): "closes a workspace, possibly with the owner's agents in it",
}
MUTATING_SOCKET_METHODS = re.compile(
    r"agent\.(prompt|send_keys|start|rename|focus)|pane\.(send_|close|split|run|report_|release_agent)"
    r"|server\.stop|workspace\.close|integration\.(install|uninstall)"
)


def block(msg: str) -> None:
    print(f"BLOCKED: {msg}", file=sys.stderr)
    sys.exit(2)


def cut_heredocs(command: str) -> str:
    """Keep only lines up to (and including) each line that opens a heredoc."""
    out, skipping, terminator = [], False, None
    for line in command.split("\n"):
        if skipping:
            if line.strip() == terminator:
                skipping = False
            continue
        out.append(line)
        m = re.search(r"<<-?\s*['\"]?([A-Za-z_][A-Za-z0-9_]*)['\"]?", line)
        if m:
            skipping, terminator = True, m.group(1)
    return "\n".join(out)


def segments(command: str):
    """Split into simple commands (token lists) on ; && || | and newlines."""
    lex = shlex.shlex(cut_heredocs(command), posix=True, punctuation_chars=";&|\n")
    lex.whitespace = " \t\r"
    lex.whitespace_split = True
    current = []
    try:
        for tok in lex:
            if tok and set(tok) <= set(";&|\n"):
                if current:
                    yield current
                current = []
            else:
                current.append(tok)
    except ValueError:
        # Unbalanced quotes: fall back to a plain split so we still inspect it.
        yield command.split()
        return
    if current:
        yield current


def strip_env(tokens):
    """Return (env assignments, remaining tokens)."""
    env = {}
    i = 0
    while i < len(tokens) and re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", tokens[i]):
        k, v = tokens[i].split("=", 1)
        env[k] = v
        i += 1
    if i < len(tokens) and tokens[i] in ("env", "command", "exec", "time", "nohup"):
        i += 1
    return env, tokens[i:]


def herdr_json(args):
    try:
        out = subprocess.run(["herdr", *args], capture_output=True, text=True, timeout=5).stdout
        return json.loads(out)
    except Exception:
        return None


def workspace_label(ws_id: str):
    wl = herdr_json(["workspace", "list"]) or {}
    for ws in (wl.get("result") or {}).get("workspaces", []):
        if ws.get("workspace_id") == ws_id:
            return ws.get("label")
    return None


def sandbox_check(target: str) -> str | None:
    """Return None if target is in the aw-sandbox workspace, else a reason."""
    pane = herdr_json(["pane", "get", target]) or {}
    info = (pane.get("result") or {}).get("pane")
    if not info:
        agent = herdr_json(["agent", "get", target]) or {}
        info = (agent.get("result") or {}).get("agent")
    if not info:
        return f"cannot resolve target {target!r} in herdr"
    ws_id = info.get("workspace_id")
    wl = herdr_json(["workspace", "list"]) or {}
    for ws in (wl.get("result") or {}).get("workspaces", []):
        if ws.get("workspace_id") == ws_id:
            if ws.get("label") == SANDBOX_LABEL:
                return None
            return f"target {target} is in workspace {ws.get('label')!r}, not {SANDBOX_LABEL!r}"
    return f"workspace of {target} not found"


def first_positional(args):
    skip_next = False
    for a in args:
        if skip_next:
            skip_next = False
            continue
        if a.startswith("--"):
            if "=" not in a:
                skip_next = True  # herdr flags take a value
            continue
        return a
    return None


def check_herdr(tokens):
    rest = tokens[1:]
    # drop global flags like --session X / --machine X
    while rest and rest[0].startswith("--"):
        rest = rest[2:] if "=" not in rest[0] else rest[1:]
    if len(rest) < 2:
        return
    if any(a in ("--help", "-h", "help") for a in rest):
        return  # reading help never mutates anything
    key = (rest[0], rest[1])
    if key == ("workspace", "close"):
        target = first_positional(rest[2:])
        if target and workspace_label(target) == SANDBOX_LABEL:
            return  # tearing down the sandbox is part of the fixture procedure
    if key in OWNER_ONLY:
        block(f"`herdr {' '.join(key)}` {OWNER_ONLY[key]}. Ask the owner to run it himself "
              f"(he can prefix it with `!` in the prompt).")
    if key in TARGETED:
        target = first_positional(rest[2:])
        if not target:
            block(f"`herdr {' '.join(key)}` without an explicit target would hit the focused pane, "
                  f"which is the owner's. Pass a pane id from the {SANDBOX_LABEL!r} workspace.")
        reason = sandbox_check(target)
        if reason:
            block(f"`herdr {' '.join(key)}` refused: {reason}. The owner's panes are live agent "
                  f"sessions. Create panes in a workspace labelled {SANDBOX_LABEL!r} "
                  f"(see .claude/skills/capture-fixture/SKILL.md) and target those.")


def check_git(tokens, env):
    args = tokens[1:]
    while args and args[0] in ("-C", "-c"):
        args = args[2:]
    if not args:
        return
    sub = args[0]
    if sub == "add" and any(a in ("-A", "--all", ".", ":/") for a in args[1:]):
        block("`git add -A` / `git add .` stages other sessions' work in this shared worktree. "
              "Add the exact paths you changed.")
    if sub == "commit" and "--amend" in args:
        block("`git commit --amend` can rewrite another agent's commit in a shared branch. "
              "Make a new commit instead.")
    if sub == "push" and env.get("AW_OWNER_APPROVED_PUSH") != "1":
        block("`git push` only happens when the owner explicitly asked for it in this conversation. "
              "If he did, re-run as `AW_OWNER_APPROVED_PUSH=1 git push ...` after verifying the build/tests pass.")


def main():
    try:
        data = json.load(sys.stdin)
    except Exception:
        sys.exit(0)
    command = (data.get("tool_input") or {}).get("command") or ""
    if not command:
        sys.exit(0)

    segs = list(segments(command))
    # Raw socket scripts usually live in a heredoc body, so look at the full command text,
    # but only when an interpreter or socket tool actually runs (not for commit messages).
    runners = {"python", "python3", "node", "bun", "deno", "nc", "socat", "go", "ruby", "perl"}
    runs_code = any(os.path.basename(strip_env(s)[1][0]) in runners for s in segs if strip_env(s)[1])
    if runs_code and ("herdr.sock" in command or "HERDR_SOCKET_PATH" in command) \
            and MUTATING_SOCKET_METHODS.search(command):
        block("raw socket call with a mutating herdr method. Use the `herdr` CLI instead so the "
              "sandbox check can verify the target pane (read-only methods like ping/agent.list are fine).")

    for seg in segs:
        env, tokens = strip_env(seg)
        if not tokens:
            continue
        prog = os.path.basename(tokens[0])
        if prog == "herdr":
            check_herdr(tokens)
        elif prog == "git":
            check_git(tokens, env)
    sys.exit(0)


if __name__ == "__main__":
    main()
