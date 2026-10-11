#!/usr/bin/env bash
# Regression tests for tools/guards/guards.py and its agy / opencode / Claude Code (+ Copilot CLI) / git adapters.
# Run: bash tools/guards/test_guards.sh
# Uses the live herdr only for read-only lookups (pane get / workspace list).
set -u
DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"
G="$DIR/guards.py"
pass=0; fail=0

expect() { # want got description
  if [ "$1" = "$2" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "FAIL: $3 (want $1, got $2)"; fi
}
cmd()   { python3 "$G" command "$1" >/dev/null 2>&1; expect "$2" $? "command: $3"; }
write() { python3 "$G" write "$1" >/dev/null 2>&1; expect "$2" $? "write: $3"; }
agy() { # json expected_decision description
  local out dec
  out="$(printf '%s' "$1" | python3 "$G" agy-pretool 2>/dev/null)"
  dec="$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("decision","none"))')"
  expect "$2" "$dec" "agy: $3"
}
oc() { printf '%s' "$1" | python3 "$G" opencode-before >/dev/null 2>&1; expect "$2" $? "opencode: $3"; }
cc() { printf '%s' "$1" | python3 "$G" claude-pretool >/dev/null 2>&1; expect "$2" $? "claude: $3"; }
j() { python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"; }  # JSON-quote a string

# an agent pane outside aw-sandbox (another session may be capturing fixtures in the sandbox)
OWNER_PANE="$(python3 -c 'import sys; sys.path.insert(0, sys.argv[1]); import guards as g
agents = ((g.herdr_json(["agent", "list"]) or {}).get("result") or {}).get("agents") or []
print(next((a["pane_id"] for a in agents if g.workspace_label(a.get("workspace_id")) != g.SANDBOX_LABEL), ""))' "$DIR" 2>/dev/null)"

# ── herdr ──
cmd 'herdr agent send-keys w0:doesnotexist 1' 2 "unknown pane refused"
if [ -n "$OWNER_PANE" ]; then
  cmd "herdr agent send-keys $OWNER_PANE 1" 2 "owner pane send-keys refused"
  cmd "herdr agent prompt $OWNER_PANE 'hi'" 2 "owner pane prompt refused"
  cmd "cd /tmp && herdr pane send-text $OWNER_PANE x" 2 "chained send-text refused"
  cmd "FOO=1 /opt/homebrew/bin/herdr agent send-keys $OWNER_PANE esc" 2 "env + abs path refused"
else
  echo "note: herdr not running, owner-pane cases skipped"
fi
cmd 'herdr agent send-keys' 2 "missing target refused"
cmd 'herdr server stop' 2 "server stop owner-only"
cmd 'herdr integration install claude' 2 "integration install owner-only"
cmd 'herdr workspace close w5' 2 "closing non-sandbox workspace refused"
cmd 'herdr tab close w5:t1' 2 "closing a non-sandbox tab refused"
cmd 'herdr tab close' 2 "tab close without a target refused"
cmd 'herdr tab close --help' 0 "tab close help allowed"
cmd $'python3 - <<\'EOF\'\ns.connect("herdr.sock"); send({"method":"tab.close"})\nEOF' 2 "raw socket tab.close refused"
cmd 'herdr workspace close --help' 0 "help allowed"
cmd 'herdr agent list' 0 "agent list allowed"
cmd 'herdr agent read w5:pAW --source visible --format text' 0 "agent read allowed"
cmd 'grep -rn "herdr agent send-keys" docs' 0 "quoted mention allowed"
cmd $'git commit -q -F - <<\'EOF\'\nnever run herdr agent prompt on owner panes\nEOF' 0 "heredoc commit message allowed"
cmd $'python3 - <<\'EOF\'\ns.connect("herdr.sock"); send({"method":"agent.send_keys"})\nEOF' 2 "raw socket send_keys refused"
cmd $'python3 - <<\'EOF\'\ns.connect("herdr.sock"); send({"method":"ping"})\nEOF' 0 "raw socket ping allowed"
# the socket check looks only at the pipeline that runs code, heredoc bodies included
cmd "go test ./pkg/herdr/... && rg -n 'agent.prompt' pkg/herdr; echo \$HERDR_SOCKET_PATH" 0 "go test + grep for a method + socket echo allowed"
cmd "python3 -m pytest && rg agent.prompt; echo herdr.sock" 0 "python in another pipeline allowed"
cmd "echo '{\"method\":\"agent.prompt\"}' | socat - UNIX-CONNECT:\$HERDR_SOCKET_PATH" 2 "echo piped into socat refused"
cmd "printf '{\"method\":\"pane.send_text\"}' 2>&1 | nc -U ~/.config/herdr/herdr.sock" 2 "printf piped into nc refused"
cmd 'go run ./tools/x -m agent.prompt -s herdr.sock' 2 "go run with a mutating method refused"
cmd $'cd /tmp && python3 - <<\'EOF\' && echo done\nimport os; os.environ["HERDR_SOCKET_PATH"]; send("agent.prompt")\nEOF' 2 "heredoc script mid-chain refused"

# ── renames that would fake the sandbox or a session tab ──
cmd 'herdr workspace rename w9 aw-sandbox' 2 "workspace renamed to the sandbox label"
cmd "herdr workspace rename w9 'aw-sandbox'" 2 "quoted sandbox label"
cmd 'herdr workspace rename w9 my-project' 0 "ordinary workspace rename"
cmd 'herdr tab rename w9:t1 aw-session-ui' 2 "tab renamed into the session prefix"
cmd 'herdr tab rename w9:t1 notes' 0 "ordinary tab rename"
cmd 'herdr tab create --workspace w9 --label aw-session-ui --no-focus' 0 "creating a session tab"
cmd "python3 -c 'import socket; s=socket.socket(socket.AF_UNIX); s.connect(\"/x/herdr.sock\"); s.sendall(b\"workspace.rename\")'" 2 "raw socket workspace.rename"
cmd "python3 -c 'import socket; s=socket.socket(socket.AF_UNIX); s.connect(\"/x/herdr.sock\"); s.sendall(b\"tab.rename\")'" 2 "raw socket tab.rename"

# ── session-launch exception (pane run in an aw-session-* tab), with a fake herdr ──
python3 "$DIR/test_session_launch.py" >/dev/null 2>&1; expect 0 $? "session launch exception unit tests"

# ── git ──
cmd 'git add -A' 2 "add -A refused"
cmd 'git add .' 2 "add . refused"
cmd 'git add docs/STATUS.md pkg/model' 0 "explicit add allowed"
cmd 'git commit --amend --no-edit' 2 "amend refused"
cmd 'git commit --no-verify -m x' 2 "no-verify refused"
cmd 'git push origin main' 2 "push refused without approval"
cmd 'AW_OWNER_APPROVED_PUSH=1 git push origin main' 0 "approved push allowed"
cmd 'git push --dry-run origin main' 0 "push --dry-run allowed"
cmd 'git push -n -u origin HEAD' 0 "push -n allowed"
cmd 'git commit -a -m x' 2 "commit -a refused"
cmd 'git commit -am x' 2 "commit -am refused"
cmd 'git commit --all -m x' 2 "commit --all refused"
cmd 'git commit -m x .' 2 "commit . refused"
cmd "git commit -m '-a is fine in a message' -- pkg/a.go" 0 "commit of named paths allowed"
cmd 'git commit -nm x' 2 "commit -n in a flag cluster refused"
cmd 'git commit -S1a2b -m x -- pkg/a.go' 0 "an a in a glued key id is no -a"
cmd 'git add -u' 2 "add -u refused"
cmd 'git add --update' 2 "add --update refused"
cmd 'git add -- .' 2 "add -- . refused"
cmd 'git --no-pager add -A' 2 "add -A after --no-pager refused"
cmd 'git -c core.quotepath=off add --all' 2 "add --all after -c refused"
cmd 'git -C /tmp add docs/' 0 "add of a named dir allowed"

# ── paths ──
write "$ROOT/bridge/server.js" 2 "legacy bridge/ refused"
write "$ROOT/claude-plugin/hooks/hooks.json" 2 "legacy claude-plugin refused"
write "$ROOT/pkg/bridge/engine.go" 0 "pkg/bridge allowed"
write "wearos-app/app/google-services.json" 2 "google-services.json refused (relative)"
write "$ROOT/deploy/relay/env.example" 0 "env.example allowed"
write "$ROOT/agent-watch.env" 2 "agent-watch.env refused"
write "agent-watch.env" 2 "agent-watch.env refused (relative)"
write "$ROOT/agent-watch.env.example" 0 "agent-watch.env.example allowed"
write "$ROOT/.env" 2 ".env refused"
write "$ROOT/pkg/agents/testdata/x/.env" 0 ".env fixture under testdata allowed"

# ── agy adapter (PreToolUse contract: toolCall.name/args → decision) ──
agy '{"toolCall":{"name":"run_command","args":{"CommandLine":"git add -A"}}}' deny "run_command git add -A denied"
agy '{"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./..."}}}' allow "run_command go test allowed"
agy "{\"toolCall\":{\"name\":\"write_to_file\",\"args\":{\"TargetFile\":$(j "$ROOT/bridge/x.js")}}}" deny "write legacy denied"
agy "{\"toolCall\":{\"name\":\"view_file\",\"args\":{\"AbsolutePath\":$(j "$ROOT/wearos-app/app/google-services.json")}}}" allow "view_file never blocked"
agy 'not json' allow "garbage input is a no-op"

# ── opencode adapter ({tool,args} → exit code) ──
oc '{"tool":"bash","args":{"command":"git commit --amend"}}' 2 "bash amend blocked"
oc '{"tool":"shell","args":{"command":"git commit --amend"}}' 2 "shell (V2 name) amend blocked"
oc '{"tool":"bash","args":{"command":"make check"}}' 0 "bash make allowed"
oc "{\"tool\":\"write\",\"args\":{\"filePath\":$(j "$ROOT/.env")}}" 2 "write .env blocked"
oc "{\"tool\":\"edit\",\"args\":{\"filePath\":$(j "$ROOT/agent-watch.env")}}" 2 "edit agent-watch.env blocked"
oc "{\"tool\":\"edit\",\"args\":{\"filePath\":$(j "$ROOT/pkg/model/agent.go")}}" 0 "edit pkg/model allowed"
oc '{"tool":"apply_patch","args":{"patchText":"*** Begin Patch\n*** Add File: claude-plugin/x.sh\n+x\n*** End Patch"}}' 2 "patch into legacy blocked"
oc '{"tool":"read","args":{"filePath":".env"}}' 0 "read is not a write"

# ── Claude Code adapter (PreToolUse: {tool_name, tool_input} → exit 2 blocks) ──
cc '{"tool_name":"Bash","tool_input":{"command":"git add -A"}}' 2 "git add -A"
cc '{"tool_name":"Bash","tool_input":{"command":"git status"}}' 0 "git status"
cc '{"tool_name":"Bash","tool_input":{"command":"herdr workspace rename w9 aw-sandbox"}}' 2 "sandbox rename"
cc "{\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"$ROOT/agent-watch.env\",\"content\":\"x\"}}" 2 "write agent-watch.env"
cc "{\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"$ROOT/README.md\",\"old_string\":\"a\",\"new_string\":\"b\"}}" 0 "edit README"
cc "{\"tool_name\":\"Read\",\"tool_input\":{\"file_path\":\"$ROOT/README.md\"}}" 0 "read is never blocked"
cc 'not json' 0 "unparseable input fails open to the normal permission flow"
cc '[1]' 0 "non-object JSON fails open"
# Copilot CLI loads these hooks; its apply_patch arrives as Edit with the patch text as tool_input
cc '{"tool_name":"Edit","tool_input":"*** Begin Patch"}' 0 "copilot: patch text without paths"
cc '{"tool_name":"Edit","tool_input":"*** Begin Patch\n*** Update File: docs/STATUS.md\n@@\n-a\n+b\n*** End Patch\n"}' 0 "copilot: patch to a doc"
cc "{\"tool_name\":\"Edit\",\"tool_input\":\"*** Begin Patch\\n*** Update File: pkg/a.go\\n*** Add File: $ROOT/agent-watch.env\\n+x\\n*** End Patch\\n\"}" 2 "copilot: patch adding agent-watch.env"
cc '{"tool_name":"Edit","tool_input":"*** Begin Patch\n*** Add File: claude-plugin/x.sh\n+x\n*** End Patch\n"}' 2 "copilot: patch into legacy"
cc '{"tool_name":"Write","tool_input":{"path":".env","file_text":"x"}}' 2 "copilot: create .env (path key)"
# a deny carries the reason on stdout too: Copilot CLI shows permissionDecisionReason, not stderr
PUSH='{"tool_name":"Bash","tool_input":{"command":"git push origin main"}}'
DENY="$(printf '%s' "$PUSH" | python3 "$G" claude-pretool 2>/dev/null)"
expect "deny yes" "$(printf '%s' "$DENY" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("permissionDecision"), "yes" if "AW_OWNER_APPROVED_PUSH" in d.get("permissionDecisionReason","") else "no")' 2>/dev/null)" "claude: deny JSON with the reason on stdout"
DENYERR="$(printf '%s' "$PUSH" | python3 "$G" claude-pretool 2>&1 >/dev/null)"
expect "BLOCKED:" "${DENYERR%% *}" "claude: reason on stderr"

# ── an internal error never blocks (or crashes) a tool hook; pre-commit fails closed ──
crash() { # entry expected_stdout description
  local out rc
  out="$(python3 -c 'import sys
sys.path.insert(0, sys.argv[1])
import guards
def boom(_argv): raise RuntimeError("synthetic")
guards.COMMANDS[sys.argv[2]] = boom
sys.exit(guards.main(["guards.py", sys.argv[2]]))' "$DIR" "$1" 2>/dev/null)"; rc=$?
  expect "${4:-0} $2" "$rc $out" "crash: $3"
}
crash claude-pretool "" "claude-pretool exits 0"
crash agy-pretool '{"decision": "allow"}' "agy-pretool allows"
crash precommit "" "precommit blocks" 1

# ── pre-commit in a throw-away repo ──
TMP="$(mktemp -d)"; (
  cd "$TMP" && git init -q && git config user.email t@t && git config user.name t
  mkdir -p pkg/model
  printf 'package model\n\ntype X struct{ A int }\n' > pkg/model/agent.go
  git add pkg/model/agent.go
  AW_REPO_ROOT="$TMP" python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r1"
  AW_REPO_ROOT="$TMP" AW_CONTRACT_NO_JSON_CHANGE=1 python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r2"
  printf 'package model\nfunc  Bad( ){}\n' > pkg/model/bad_test.go; git add pkg/model/bad_test.go
  AW_REPO_ROOT="$TMP" AW_CONTRACT_NO_JSON_CHANGE=1 python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r3"
  git rm -q --cached pkg/model/bad_test.go
  printf 'AW_HOST_TOKEN=%s\n' "$(printf 'a%.0s' $(seq 64))" > leak.txt; git add leak.txt
  AW_REPO_ROOT="$TMP" AW_CONTRACT_NO_JSON_CHANGE=1 python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r4"
  git rm -q --cached leak.txt
  printf 'AW_RELAY_DOMAIN=relay.example.com\n' > agent-watch.env; git add -f agent-watch.env
  AW_REPO_ROOT="$TMP" AW_CONTRACT_NO_JSON_CHANGE=1 python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r5"
  git rm -q --cached agent-watch.env
  printf 'AW_HOST_TOKEN=\n' > agent-watch.env.example; git add -f agent-watch.env.example
  AW_REPO_ROOT="$TMP" AW_CONTRACT_NO_JSON_CHANGE=1 python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r6"
  mkdir -p pkg/relay/testdata; printf '{}\n' > pkg/relay/testdata/store.json; git add pkg/relay/testdata/store.json
  AW_REPO_ROOT="$TMP" AW_CONTRACT_NO_JSON_CHANGE=1 python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r7"
  printf '{}\n' > store.json; git add store.json
  AW_REPO_ROOT="$TMP" AW_CONTRACT_NO_JSON_CHANGE=1 python3 "$G" precommit >/dev/null 2>&1; echo $? > "$TMP/r8"
)
expect 1 "$(cat "$TMP/r1")" "precommit: pkg/model without peers blocked"
expect 0 "$(cat "$TMP/r2")" "precommit: explicit no-JSON-change allowed"
expect 1 "$(cat "$TMP/r3")" "precommit: unformatted Go blocked"
expect 1 "$(cat "$TMP/r4")" "precommit: host token blocked"
expect 1 "$(cat "$TMP/r5")" "precommit: agent-watch.env blocked"
expect 0 "$(cat "$TMP/r6")" "precommit: agent-watch.env.example allowed"
expect 0 "$(cat "$TMP/r7")" "precommit: store.json fixture under testdata allowed"
expect 1 "$(cat "$TMP/r8")" "precommit: store.json blocked"
rm -rf "$TMP"

echo "guards: $pass passed, $fail failed"
[ "$fail" = 0 ]
