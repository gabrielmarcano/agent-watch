#!/usr/bin/env bash
# Regression tests for tools/guards/guards.py and its agy / opencode / git adapters.
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

OWNER_PANE="$(herdr agent list 2>/dev/null | python3 -c 'import json,sys
try: print(json.load(sys.stdin)["result"]["agents"][0]["pane_id"])
except Exception: print("")')"

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
cmd 'herdr workspace close --help' 0 "help allowed"
cmd 'herdr agent list' 0 "agent list allowed"
cmd 'herdr agent read w5:pAW --source visible --format text' 0 "agent read allowed"
cmd 'grep -rn "herdr agent send-keys" docs' 0 "quoted mention allowed"
cmd $'git commit -q -F - <<\'EOF\'\nnever run herdr agent prompt on owner panes\nEOF' 0 "heredoc commit message allowed"
cmd $'python3 - <<\'EOF\'\ns.connect("herdr.sock"); send({"method":"agent.send_keys"})\nEOF' 2 "raw socket send_keys refused"
cmd $'python3 - <<\'EOF\'\ns.connect("herdr.sock"); send({"method":"ping"})\nEOF' 0 "raw socket ping allowed"

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
cmd 'git push origin feat/herdr-focus' 2 "push refused without approval"
cmd 'AW_OWNER_APPROVED_PUSH=1 git push origin feat/herdr-focus' 0 "approved push allowed"

# ── paths ──
write "$ROOT/bridge/server.js" 2 "legacy bridge/ refused"
write "$ROOT/claude-plugin/hooks/hooks.json" 2 "legacy claude-plugin refused"
write "$ROOT/pkg/bridge/engine.go" 0 "pkg/bridge allowed"
write "wearos-app/app/google-services.json" 2 "google-services.json refused (relative)"
write "$ROOT/deploy/relay/env.example" 0 "env.example allowed"
write "$ROOT/agent-watch.env" 2 "agent-watch.env refused"
write "agent-watch.env" 2 "agent-watch.env refused (relative)"
write "$ROOT/agent-watch.env.example" 0 "agent-watch.env.example allowed"

# ── agy adapter (PreToolUse contract: toolCall.name/args → decision) ──
agy '{"toolCall":{"name":"run_command","args":{"CommandLine":"git add -A"}}}' deny "run_command git add -A denied"
agy '{"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./..."}}}' allow "run_command go test allowed"
agy "{\"toolCall\":{\"name\":\"write_to_file\",\"args\":{\"TargetFile\":$(j "$ROOT/bridge/x.js")}}}" deny "write legacy denied"
agy "{\"toolCall\":{\"name\":\"view_file\",\"args\":{\"AbsolutePath\":$(j "$ROOT/wearos-app/app/google-services.json")}}}" allow "view_file never blocked"
agy 'not json' allow "garbage input is a no-op"

# ── opencode adapter ({tool,args} → exit code) ──
oc '{"tool":"bash","args":{"command":"git commit --amend"}}' 2 "bash amend blocked"
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
)
expect 1 "$(cat "$TMP/r1")" "precommit: pkg/model without peers blocked"
expect 0 "$(cat "$TMP/r2")" "precommit: explicit no-JSON-change allowed"
expect 1 "$(cat "$TMP/r3")" "precommit: unformatted Go blocked"
expect 1 "$(cat "$TMP/r4")" "precommit: host token blocked"
expect 1 "$(cat "$TMP/r5")" "precommit: agent-watch.env blocked"
expect 0 "$(cat "$TMP/r6")" "precommit: agent-watch.env.example allowed"
rm -rf "$TMP"

echo "guards: $pass passed, $fail failed"
[ "$fail" = 0 ]
