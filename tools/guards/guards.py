#!/usr/bin/env python3
"""Tool-agnostic safety guards for the Agent Watch repo.

One implementation, several entry points:

  guards.py command "<shell command>"   → exit 2 + reason on stderr if blocked
  guards.py write <path>                 → exit 2 if an agent must not write there
  guards.py agy-pretool                  → Antigravity CLI PreToolUse hook (stdin/stdout JSON)
  guards.py agy-stop                     → Antigravity CLI Stop hook (contract drift)
  guards.py opencode-before              → OpenCode plugin bridge (stdin JSON {tool,args})
  guards.py precommit                    → git pre-commit: secrets, gofmt, contract sync

Wired from:
  .agents/hooks.json                     (agy)
  .opencode/plugins/agent-watch-guards.js (opencode)
  .githooks/pre-commit                   (git, every tool and humans)

What is blocked, and why:
1. Input to live herdr panes. The development Mac runs the owner's REAL agent
   sessions; sending "1" to one of them approves whatever it asked. Mutating
   herdr commands are allowed only when the target pane belongs to the
   workspace labelled "aw-sandbox" (checked live with `herdr pane get` +
   `herdr workspace list`, so an env marker cannot bypass it).
2. Raw writes to the herdr socket with mutating methods.
3. Owner-only herdr operations: server stop, integration install/uninstall,
   closing a non-sandbox workspace.
4. Unsafe git in a shared worktree: add -A/--all/., commit --amend, and push
   unless AW_OWNER_APPROVED_PUSH=1 (use it only when the owner asked).
5. Writes to legacy paths and secret files.
6. Commits with secrets, unformatted Go, or pkg/model changes without the
   matching client/contract changes.
"""
import json
import os
import re
import shlex
import shutil
import subprocess
import sys

ROOT = os.environ.get("AW_REPO_ROOT") or os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SANDBOX_LABEL = "aw-sandbox"

# ─────────────────────────────── herdr ───────────────────────────────

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
CODE_RUNNERS = {"python", "python3", "node", "bun", "deno", "nc", "socat", "go", "ruby", "perl"}


class Blocked(Exception):
    pass


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
        yield command.split()
        return
    if current:
        yield current


def strip_env(tokens):
    env, i = {}, 0
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


def workspace_label(ws_id):
    wl = herdr_json(["workspace", "list"]) or {}
    for ws in (wl.get("result") or {}).get("workspaces", []):
        if ws.get("workspace_id") == ws_id:
            return ws.get("label")
    return None


def sandbox_problem(target):
    """None if target is in the aw-sandbox workspace, else the reason."""
    pane = herdr_json(["pane", "get", target]) or {}
    info = (pane.get("result") or {}).get("pane")
    if not info:
        agent = herdr_json(["agent", "get", target]) or {}
        info = (agent.get("result") or {}).get("agent")
    if not info:
        return f"cannot resolve target {target!r} in herdr"
    label = workspace_label(info.get("workspace_id"))
    if label == SANDBOX_LABEL:
        return None
    return f"target {target} is in workspace {label!r}, not {SANDBOX_LABEL!r}"


def first_positional(args):
    skip_next = False
    for a in args:
        if skip_next:
            skip_next = False
            continue
        if a.startswith("--"):
            skip_next = "=" not in a
            continue
        return a
    return None


def check_herdr(tokens):
    rest = tokens[1:]
    while rest and rest[0].startswith("--"):
        rest = rest[2:] if "=" not in rest[0] else rest[1:]
    if len(rest) < 2 or any(a in ("--help", "-h", "help") for a in rest):
        return
    key = (rest[0], rest[1])
    if key == ("workspace", "close"):
        target = first_positional(rest[2:])
        if target and workspace_label(target) == SANDBOX_LABEL:
            return
    if key in OWNER_ONLY:
        raise Blocked(f"`herdr {' '.join(key)}` {OWNER_ONLY[key]}. Ask the owner to run it himself.")
    if key in TARGETED:
        target = first_positional(rest[2:])
        if not target:
            raise Blocked(f"`herdr {' '.join(key)}` without an explicit target would hit the focused pane, "
                          f"which is the owner's. Pass a pane id from the {SANDBOX_LABEL!r} workspace.")
        reason = sandbox_problem(target)
        if reason:
            raise Blocked(f"`herdr {' '.join(key)}` refused: {reason}. The owner's panes are live agent "
                          f"sessions. Create panes in a workspace labelled {SANDBOX_LABEL!r} "
                          f"(see .agents/skills/capture-fixture/SKILL.md) and target those.")


def check_git(tokens, env):
    args = tokens[1:]
    while args and args[0] in ("-C", "-c"):
        args = args[2:]
    if not args:
        return
    sub = args[0]
    if sub == "add" and any(a in ("-A", "--all", ".", ":/") for a in args[1:]):
        raise Blocked("`git add -A` / `git add .` stages other sessions' work in this shared worktree. "
                      "Add the exact paths you changed.")
    if sub == "commit" and "--amend" in args:
        raise Blocked("`git commit --amend` can rewrite another agent's commit in a shared branch. "
                      "Make a new commit instead.")
    if sub == "commit" and ("--no-verify" in args or "-n" in args):
        raise Blocked("`git commit --no-verify` skips the secret/format/contract checks. Fix what the "
                      "pre-commit hook reports instead.")
    if sub == "push" and env.get("AW_OWNER_APPROVED_PUSH") != "1":
        raise Blocked("`git push` only happens when the owner explicitly asked for it. If he did, re-run as "
                      "`AW_OWNER_APPROVED_PUSH=1 git push ...` after verifying the build and tests pass.")


def check_command(command: str):
    """Raise Blocked if the shell command must not run."""
    if not command:
        return
    segs = list(segments(command))
    runs_code = any(strip_env(s)[1] and os.path.basename(strip_env(s)[1][0]) in CODE_RUNNERS for s in segs)
    if runs_code and ("herdr.sock" in command or "HERDR_SOCKET_PATH" in command) \
            and MUTATING_SOCKET_METHODS.search(command):
        raise Blocked("raw socket call with a mutating herdr method. Use the `herdr` CLI instead so the "
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

# ─────────────────────────────── paths ───────────────────────────────

LEGACY = re.compile(r"^(bridge|claude-plugin|\.claude-plugin)/")
SECRET_PATH = re.compile(r"(^|/)(google-services\.json|firebase-service-account[^/]*\.json|\.env)$")


def rel_to_root(path: str) -> str:
    return os.path.relpath(os.path.abspath(os.path.join(ROOT, path)), ROOT)


def check_write(path: str):
    """Raise Blocked if an agent must not create/modify this path."""
    if not path:
        return
    rel = rel_to_root(path)
    if rel.startswith("docs/") or rel.endswith(".md"):
        return
    if LEGACY.search(rel):
        raise Blocked(f"{rel} is a legacy path removed by the herdr refactor (Node bridge, Claude plugin). "
                      "See AGENTS.md §1.1: the replacement lives in cmd/, pkg/ and herdr-plugin.toml.")
    if SECRET_PATH.search(rel):
        raise Blocked(f"{rel} holds secrets and is placed by the owner by hand, never written by an agent. "
                      "Use the *.example files for templates.")

# ───────────────────────────── pre-commit ────────────────────────────

SECRET_FILES = re.compile(
    r"(^|/)(google-services\.json|firebase-service-account[^/]*\.json|\.env(\.[^/]*)?|store\.json)$"
)
ALLOWED_FILES = re.compile(r"(\.example|\.template|\.sample)$")
SKIP_SCAN = ("tools/guards/",)  # the patterns themselves live here
SECRET_PATTERNS = [
    (re.compile(r"-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----"), "private key"),
    (re.compile(r'"private_key"\s*:\s*"-----BEGIN'), "service-account private key"),
    (re.compile(r'"type"\s*:\s*"service_account"'), "service-account JSON"),
    (re.compile(r"AW_HOST_TOKEN\s*=\s*['\"]?[0-9a-f]{64}"), "relay host token"),
    (re.compile(r"host_token\s*=\s*['\"][0-9a-f]{64}['\"]"), "bridge host token"),
    (re.compile(r"Bearer\s+[0-9a-f]{64}\b"), "bearer token"),
    (re.compile(r"\btk_[a-z0-9]{29}\b"), "ntfy access token"),
    (re.compile(r"AIza[0-9A-Za-z_\-]{35}"), "Google API key"),
    (re.compile(r"\bghp_[A-Za-z0-9]{36}\b|\bgithub_pat_[A-Za-z0-9_]{50,}"), "GitHub token"),
]
CONTRACT_GO = re.compile(r"^pkg/model/(?!.*_test\.go$)[^/]+\.go$")
CONTRACT_PEERS = {
    "docs/reference/contracts.md": re.compile(r"^docs/reference/contracts\.md$"),
    "wearos-app model": re.compile(r"^wearos-app/app/src/main/java/com/gabriel/agentwatch/model/"),
    "watchos-app Models": re.compile(r"^watchos-app/AgentWatch/Models/"),
}


def git(*args):
    return subprocess.run(["git", "-C", ROOT, *args], capture_output=True, text=True).stdout


def precommit_problems():
    staged = git("diff", "--cached", "--name-only", "--diff-filter=ACM").split()
    problems = []
    for path in staged:
        if SECRET_FILES.search(path) and not ALLOWED_FILES.search(path):
            problems.append(f"{path}: secret file must never be committed")
            continue
        if path.startswith(SKIP_SCAN) or path.endswith((".png", ".jpg", ".jar", ".ico")):
            continue
        diff = git("diff", "--cached", "-U0", "--", path)
        added = "\n".join(l[1:] for l in diff.splitlines() if l.startswith("+") and not l.startswith("+++"))
        for rx, what in SECRET_PATTERNS:
            if rx.search(added):
                problems.append(f"{path}: looks like a {what}")

    go_files = [p for p in staged if p.endswith(".go")]
    if go_files and shutil.which("gofmt"):
        unformatted = subprocess.run(["gofmt", "-l", *[os.path.join(ROOT, p) for p in go_files]],
                                     capture_output=True, text=True).stdout.split()
        for f in unformatted:
            problems.append(f"{os.path.relpath(f, ROOT)}: not gofmt-formatted (run `gofmt -w` on it and re-stage)")

    if any(CONTRACT_GO.search(p) for p in staged):
        missing = [name for name, rx in CONTRACT_PEERS.items() if not any(rx.search(p) for p in staged)]
        if missing and os.environ.get("AW_CONTRACT_NO_JSON_CHANGE") != "1":
            problems.append(
                "pkg/model changed without " + ", ".join(missing) + ". Keep the four contract copies in sync "
                "(.agents/skills/schema-sync/SKILL.md). If the change touches no JSON field (e.g. a helper "
                "function), commit with AW_CONTRACT_NO_JSON_CHANGE=1 and say so in the message.")
    return problems


def contract_drift_uncommitted():
    """For agy's Stop hook: pkg/model modified in the worktree without its peers."""
    status = git("status", "--porcelain").splitlines()
    paths = [l[3:].strip() for l in status]
    if not any(CONTRACT_GO.search(p) for p in paths):
        return None
    missing = [name for name, rx in CONTRACT_PEERS.items() if not any(rx.search(p) for p in paths)]
    return missing or None

# ───────────────────────────── entry points ──────────────────────────


def cmd_command(argv):
    try:
        check_command(" ".join(argv))
    except Blocked as e:
        print(f"BLOCKED: {e}", file=sys.stderr)
        return 2
    return 0


def cmd_write(argv):
    try:
        for p in argv:
            check_write(p)
    except Blocked as e:
        print(f"BLOCKED: {e}", file=sys.stderr)
        return 2
    return 0


PATH_KEYS = ("TargetFile", "AbsolutePath", "FilePath", "filePath", "file_path", "path", "Path")


def paths_in_args(args: dict):
    found = []
    for k in PATH_KEYS:
        v = args.get(k)
        if isinstance(v, str) and v:
            found.append(v)
    # patch-style tools: "*** Update File: path" / "*** Add File: path"
    for k in ("patchText", "patch", "Patch"):
        v = args.get(k)
        if isinstance(v, str):
            found += re.findall(r"^\*\*\* (?:Add|Update|Delete) File: (.+)$", v, flags=re.M)
    return found


def cmd_agy_pretool(_argv):
    """Antigravity CLI PreToolUse: stdin {"toolCall":{"name","args"}}, stdout {"decision",...}."""
    try:
        data = json.load(sys.stdin)
    except Exception:
        print("{}")
        return 0
    call = data.get("toolCall") or {}
    args = call.get("args") or {}
    try:
        cmd = args.get("CommandLine") or args.get("command") or ""
        if isinstance(cmd, str) and cmd:
            check_command(cmd)
        if str(call.get("name", "")) != "view_file":
            for p in paths_in_args(args):
                check_write(p)
    except Blocked as e:
        print(json.dumps({"decision": "deny", "reason": f"BLOCKED: {e}"}))
        return 0
    print("{}")  # no opinion: agy applies its normal permission flow
    return 0


def cmd_agy_stop(_argv):
    """Antigravity CLI Stop: keep going if the contracts are out of sync (once)."""
    try:
        data = json.load(sys.stdin)
    except Exception:
        data = {}
    missing = contract_drift_uncommitted()
    if missing and data.get("executionNum", 1) <= 1:
        print(json.dumps({"decision": "continue", "reason":
              "pkg/model has uncommitted changes but these were not updated: " + ", ".join(missing)
              + ". Update them (.agents/skills/schema-sync/SKILL.md) or tell the owner why the change "
                "does not affect them."}))
        return 0
    print("{}")
    return 0


WRITE_TOOLS = {"edit", "write", "patch", "multiedit", "apply_patch"}


def cmd_opencode_before(_argv):
    """OpenCode bridge: stdin {"tool": str, "args": {...}} → exit 2 + stderr when blocked."""
    try:
        data = json.load(sys.stdin)
    except Exception:
        return 0
    tool = str(data.get("tool") or "")
    args = data.get("args") or {}
    try:
        if tool == "bash":
            check_command(str(args.get("command") or ""))
        elif tool in WRITE_TOOLS:
            for p in paths_in_args(args):
                check_write(p)
    except Blocked as e:
        print(f"BLOCKED: {e}", file=sys.stderr)
        return 2
    return 0


def cmd_precommit(_argv):
    problems = precommit_problems()
    if problems:
        print("pre-commit BLOCKED:\n  " + "\n  ".join(problems), file=sys.stderr)
        return 1
    return 0


COMMANDS = {
    "command": cmd_command,
    "write": cmd_write,
    "agy-pretool": cmd_agy_pretool,
    "agy-stop": cmd_agy_stop,
    "opencode-before": cmd_opencode_before,
    "precommit": cmd_precommit,
}

if __name__ == "__main__":
    if len(sys.argv) < 2 or sys.argv[1] not in COMMANDS:
        print(__doc__, file=sys.stderr)
        sys.exit(64)
    sys.exit(COMMANDS[sys.argv[1]](sys.argv[2:]))
