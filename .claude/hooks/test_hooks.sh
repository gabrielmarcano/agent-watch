#!/usr/bin/env bash
# Regression tests for the repo hooks. Run: bash .claude/hooks/test_hooks.sh
# Uses the live herdr only for read-only lookups (pane get / workspace list):
# the guard blocks before anything is executed.
set -u
DIR="$(cd "$(dirname "$0")" && pwd)"
export CLAUDE_PROJECT_DIR="$(cd "$DIR/../.." && pwd)"
pass=0; fail=0

run() { # expected_exit hook json description
  local want="$1" hook="$2" json="$3" desc="$4" got
  printf '%s' "$json" | python3 "$DIR/$hook" >/dev/null 2>&1; got=$?
  if [ "$got" = "$want" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "FAIL [$hook] $desc (want $want, got $got)"; fi
}
bash_json() { python3 -c 'import json,sys; print(json.dumps({"tool_input":{"command":sys.argv[1]}}))' "$1"; }
file_json() { python3 -c 'import json,sys; print(json.dumps({"tool_input":{"file_path":sys.argv[1]}}))' "$1"; }

# A real pane that is NOT in aw-sandbox (first agent pane of the owner), if herdr is running.
OWNER_PANE="$(herdr agent list 2>/dev/null | python3 -c 'import json,sys
try: print(json.load(sys.stdin)["result"]["agents"][0]["pane_id"])
except Exception: print("")')"

# --- validate_bash.py: herdr ---
run 2 validate_bash.py "$(bash_json 'herdr agent send-keys w0:doesnotexist 1')" "unknown pane is refused"
if [ -n "$OWNER_PANE" ]; then
  run 2 validate_bash.py "$(bash_json "herdr agent send-keys $OWNER_PANE 1")" "owner pane send-keys refused"
  run 2 validate_bash.py "$(bash_json "herdr agent prompt $OWNER_PANE 'hi'")" "owner pane prompt refused"
  run 2 validate_bash.py "$(bash_json "cd /tmp && herdr pane send-text $OWNER_PANE x")" "chained pane send-text refused"
  run 2 validate_bash.py "$(bash_json "FOO=1 /opt/homebrew/bin/herdr agent send-keys $OWNER_PANE esc")" "env + abs path refused"
fi
run 2 validate_bash.py "$(bash_json 'herdr agent send-keys')" "missing target refused"
run 2 validate_bash.py "$(bash_json 'herdr server stop')" "server stop is owner-only"
run 2 validate_bash.py "$(bash_json 'herdr integration install claude')" "integration install is owner-only"
run 0 validate_bash.py "$(bash_json 'herdr agent list')" "agent list allowed"
run 0 validate_bash.py "$(bash_json 'herdr agent read w5:pAW --source visible --format text')" "agent read allowed"
run 0 validate_bash.py "$(bash_json 'herdr workspace create --help')" "workspace create allowed"
run 0 validate_bash.py "$(bash_json 'herdr workspace close --help')" "help on owner-only command allowed"
run 0 validate_bash.py "$(bash_json 'herdr agent send-keys --help')" "help on targeted command allowed"
run 2 validate_bash.py "$(bash_json 'herdr workspace close w5')" "closing a non-sandbox workspace refused"
run 0 validate_bash.py "$(bash_json 'grep -rn "herdr agent send-keys" docs')" "quoted mention allowed"
run 0 validate_bash.py "$(bash_json $'git commit -q -F - <<\'EOF\'\nnever run herdr agent prompt on owner panes\nEOF')" "heredoc commit message allowed"
run 2 validate_bash.py "$(bash_json $'python3 - <<\'EOF\'\nimport socket\ns.connect("herdr.sock"); send({"method":"agent.send_keys"})\nEOF')" "raw socket send_keys refused"
run 0 validate_bash.py "$(bash_json $'python3 - <<\'EOF\'\ns.connect("herdr.sock"); send({"method":"ping"})\nEOF')" "raw socket ping allowed"

# --- validate_bash.py: git ---
run 2 validate_bash.py "$(bash_json 'git add -A')" "git add -A refused"
run 2 validate_bash.py "$(bash_json 'git add .')" "git add . refused"
run 0 validate_bash.py "$(bash_json 'git add docs/STATUS.md pkg/model')" "explicit git add allowed"
run 2 validate_bash.py "$(bash_json 'git commit --amend --no-edit')" "amend refused"
run 2 validate_bash.py "$(bash_json 'git push origin feat/herdr-focus')" "push refused without approval"
run 0 validate_bash.py "$(bash_json 'AW_OWNER_APPROVED_PUSH=1 git push origin feat/herdr-focus')" "approved push allowed"

# --- protect_paths.py ---
R="$CLAUDE_PROJECT_DIR"
run 2 protect_paths.py "$(file_json "$R/bridge/server.js")" "legacy bridge/ refused"
run 2 protect_paths.py "$(file_json "$R/claude-plugin/hooks/hooks.json")" "legacy claude-plugin refused"
run 0 protect_paths.py "$(file_json "$R/pkg/bridge/engine.go")" "pkg/bridge allowed"
run 2 protect_paths.py "$(file_json "$R/wearos-app/app/google-services.json")" "google-services.json refused"
run 0 protect_paths.py "$(file_json "$R/deploy/relay/env.example")" "env.example allowed"

# --- post_edit.py ---
run 2 post_edit.py "$(file_json "$R/pkg/model/agent.go")" "contract reminder on pkg/model"
run 0 post_edit.py "$(file_json "$R/pkg/model/model_test.go")" "no reminder on model tests"
run 2 post_edit.py "$(file_json "$R/pkg/agents/menu.go")" "adapter reminder"
run 0 post_edit.py "$(file_json "$R/pkg/relay/api.go")" "no reminder on relay"

echo "hooks: $pass passed, $fail failed"
[ "$fail" = 0 ]
