#!/usr/bin/env bash
# Tests for tools/config/awenv.sh and the Makefile's agent-watch.env targets.
# Run: bash tools/config/test_awenv.sh
# Works in a temp dir only. The make targets are exercised on their refusal
# paths and on `config` / `watchos-config`, which only write the temp files
# passed in; nothing configures the bridge or touches a server.
set -u
DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"
A="$DIR/awenv.sh"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
pass=0
fail=0

ok() { pass=$((pass + 1)); }
no() { fail=$((fail + 1)); echo "FAIL: $*"; }
expect() { # want got description
	if [ "$1" = "$2" ]; then ok; else no "$3 (want [$1], got [$2])"; fi
}
contains() { # file-or-string needle description
	case "$1" in *"$2"*) ok ;; *) no "$3 (no [$2] in [$1])" ;; esac
}
lacks() {
	case "$1" in *"$2"*) no "$3 (found [$2])" ;; *) ok ;; esac
}
mode() { stat -f %Lp "$1" 2>/dev/null || stat -c %a "$1"; }
TOK=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

# ── init ──
cat >"$T/example" <<'EOF'
# header comment
AW_RELAY_DOMAIN=relay.example.com
# The token:
AW_HOST_TOKEN=
AW_RELAY_SSH=
# AW_LISTEN=127.0.0.1:8080
EOF
out="$(bash "$A" init "$T/a.env" "$T/example" 2>&1)"
expect 0 $? "init: exit 0"
expect 600 "$(mode "$T/a.env")" "init: mode 0600"
tok="$(sed -n 's/^AW_HOST_TOKEN=//p' "$T/a.env")"
printf '%s' "$tok" | grep -Eq '^[0-9a-f]{64}$' && ok || no "init: generated token is not 64 hex ([${#tok}] chars)"
lacks "$out" "$tok" "init: token printed"
contains "$out" "generated AW_HOST_TOKEN" "init: says it generated the token"
expect "$(grep -v '^AW_HOST_TOKEN=' "$T/example")" "$(grep -v '^AW_HOST_TOKEN=' "$T/a.env")" "init: every other line kept"
expect 1 "$(grep -c '^AW_HOST_TOKEN=' "$T/a.env")" "init: one token line"
ls "$T" | grep -q '^a\.env\.' && no "init: temp file left behind" || ok

chmod 644 "$T/a.env"
out="$(bash "$A" init "$T/a.env" "$T/example" 2>&1)"
expect 0 $? "init again: exit 0"
expect "$tok" "$(sed -n 's/^AW_HOST_TOKEN=//p' "$T/a.env")" "init again: token kept"
expect 600 "$(mode "$T/a.env")" "init again: mode fixed to 0600"
contains "$out" "already set" "init again: says the token is kept"
lacks "$out" "$tok" "init again: token printed"

printf 'AW_RELAY_DOMAIN=relay.example.com\n' >"$T/b.env"
bash "$A" init "$T/b.env" "$T/example" >/dev/null 2>&1
expect 0 $? "init without a token line: exit 0"
grep -Eq '^AW_HOST_TOKEN=[0-9a-f]{64}$' "$T/b.env" && ok || no "init without a token line: token not appended"

printf 'AW_HOST_TOKEN=not-hex\n' >"$T/c.env"
out="$(bash "$A" init "$T/c.env" "$T/example" 2>&1)"
expect 1 $? "init with a malformed token: refused"
expect "AW_HOST_TOKEN=not-hex" "$(cat "$T/c.env")" "init with a malformed token: file unchanged"
lacks "$out" "not-hex" "init with a malformed token: value echoed"

bash "$A" init "$T/d.env" "$T/missing-example" >/dev/null 2>&1
expect 1 $? "init without the example: refused"

# ── get ──
cat >"$T/g.env" <<EOF
AW_RELAY_DOMAIN=first.example.com
  AW_RELAY_DOMAIN = relay.example.com   # inline comment
AW_RELAY_SSH_OPTS=-i ~/.ssh/key -o Port=2222
AW_HOST_TOKEN=$TOK
# AW_RELAY_SSH=commented@example.com
EOF
expect "relay.example.com" "$(bash "$A" get "$T/g.env" AW_RELAY_DOMAIN)" "get: last assignment, trimmed, comment cut"
expect "-i ~/.ssh/key -o Port=2222" "$(bash "$A" get "$T/g.env" AW_RELAY_SSH_OPTS)" "get: value with spaces"
expect "" "$(bash "$A" get "$T/g.env" AW_RELAY_SSH)" "get: commented key is unset"
out="$(bash "$A" get "$T/g.env" AW_HOST_TOKEN 2>&1)"
expect 1 $? "get AW_HOST_TOKEN: refused"
lacks "$out" "$TOK" "get AW_HOST_TOKEN: token printed"
bash "$A" get "$T/g.env" AW_NTFY_TOKEN >/dev/null 2>&1
expect 1 $? "get AW_NTFY_TOKEN: refused"

# ── relay-env ──
cat >"$T/r.env" <<EOF
AW_RELAY_DOMAIN=relay.example.com
AW_RELAY_SSH=root@relay.example.com
AW_HOST_TOKEN=$TOK
AW_LISTEN=127.0.0.1:8080
AW_NTFY_TOKEN=
# AW_NTFY_TOPIC=commented-out
AW_TRUSTED_PROXIES=127.0.0.1/32, 10.0.0.0/8
AW_WATCHOS_BUNDLE_ID=com.example.agentwatch
EOF
out="$(bash "$A" relay-env "$T/r.env" "$T/r.out" 2>&1)"
expect 0 $? "relay-env: exit 0"
expect "AW_HOST_TOKEN=$TOK
AW_LISTEN=127.0.0.1:8080
AW_TRUSTED_PROXIES=127.0.0.1/32, 10.0.0.0/8
AW_NTFY_TOKEN=" "$(cat "$T/r.out")" "relay-env: only the relay keys the file sets, in contract order"
expect 600 "$(mode "$T/r.out")" "relay-env: output mode 0600"
lacks "$out" "$TOK" "relay-env: token printed"
contains "$out" "AW_HOST_TOKEN AW_LISTEN AW_TRUSTED_PROXIES AW_NTFY_TOKEN" "relay-env: names the keys"

printf 'AW_HOST_TOKEN=\nAW_LISTEN=127.0.0.1:8080\n' >"$T/r2.env"
bash "$A" relay-env "$T/r2.env" "$T/r2.out" >/dev/null 2>&1
expect 1 $? "relay-env: an empty host token is never synced"
[ -e "$T/r2.out" ] && no "relay-env: partial output left after a refusal" || ok

printf 'AW_HOST_TOKEN=%s\nAW_NTFY_TOPIC="quoted"\n' "$TOK" >"$T/r3.env"
out="$(bash "$A" relay-env "$T/r3.env" "$T/r3.out" 2>&1)"
expect 1 $? "relay-env: quoted value refused"
contains "$out" "AW_NTFY_TOPIC" "relay-env: names the bad key"

printf 'AW_LISTEN=$HOME\n' >"$T/r4.env"
bash "$A" relay-env "$T/r4.env" "$T/r4.out" >/dev/null 2>&1
expect 1 $? "relay-env: \$ refused (systemd would read it differently)"

printf 'AW_RELAY_DOMAIN=relay.example.com\n' >"$T/r5.env"
bash "$A" relay-env "$T/r5.env" "$T/r5.out" >/dev/null 2>&1
expect 0 $? "relay-env: no relay keys is fine"
expect "" "$(cat "$T/r5.out")" "relay-env: no relay keys → empty output"

# ── merge ──
cat >"$T/server.env" <<'EOF'
# /etc/agent-watch-relay/env (root:agentwatch, 0640)
AW_LISTEN=172.17.0.1:8080
AW_HOST_TOKEN=__old__
AW_DATA_DIR=/var/lib/agent-watch-relay
# AW_NTFY_TOPIC=old-commented
  AW_TRUSTED_PROXIES = "172.16.0.0/12"
; systemd comment AW_LISTEN=nope
AW_FCM_CREDENTIALS=/etc/agent-watch-relay/fcm.json
AW_LISTEN=duplicate
EOF
printf 'AW_HOST_TOKEN=%s\nAW_LISTEN=127.0.0.1:8080\nAW_TRUSTED_PROXIES=172.16.0.0/12\nAW_NTFY_TOPIC=new-topic\n' "$TOK" >"$T/updates"
out="$(bash "$A" merge "$T/server.env" "$T/updates" "$T/merged" 2>&1)"
expect 0 $? "merge: exit 0"
expect "# /etc/agent-watch-relay/env (root:agentwatch, 0640)
AW_LISTEN=127.0.0.1:8080
AW_HOST_TOKEN=$TOK
AW_DATA_DIR=/var/lib/agent-watch-relay
# AW_NTFY_TOPIC=old-commented
AW_TRUSTED_PROXIES=172.16.0.0/12
; systemd comment AW_LISTEN=nope
AW_FCM_CREDENTIALS=/etc/agent-watch-relay/fcm.json
AW_LISTEN=127.0.0.1:8080

# Added from agent-watch.env by deploy.sh --sync-env
AW_NTFY_TOPIC=new-topic" "$(cat "$T/merged")" "merge: replaced in place, comments and other keys kept, new keys appended"
contains "$out" "changed: AW_HOST_TOKEN AW_LISTEN;" "merge: reports changed keys"
contains "$out" "added: AW_NTFY_TOPIC;" "merge: reports added keys"
contains "$out" "unchanged: AW_TRUSTED_PROXIES" "merge: a quoted equal value is unchanged"
lacks "$out" "$TOK" "merge: token printed"
lacks "$out" "new-topic" "merge: value printed"

: >"$T/empty-updates"
bash "$A" merge "$T/server.env" "$T/empty-updates" "$T/merged2" 2>/dev/null
expect "$(cat "$T/server.env")" "$(cat "$T/merged2")" "merge: no updates → identical"

printf 'AW_LISTEN=1.2.3.4:8080' >"$T/no-newline.env" # no trailing newline
printf 'AW_LISTEN=127.0.0.1:8080\n' >"$T/u2"
bash "$A" merge "$T/no-newline.env" "$T/u2" "$T/merged3" 2>/dev/null
expect "AW_LISTEN=127.0.0.1:8080" "$(cat "$T/merged3")" "merge: file without a trailing newline"

bash "$A" merge "$T/server.env" "$T/updates" "$T/merged4" 2>/dev/null
bash "$A" merge "$T/merged4" "$T/updates" "$T/merged5" 2>"$T/err5"
expect "$(cat "$T/merged4")" "$(cat "$T/merged5")" "merge: idempotent"
contains "$(cat "$T/err5")" "changed: none; added: none" "merge: second run changes nothing"

# ── xcconfig ──
printf 'AW_RELAY_DOMAIN=relay.example.com\nAW_WATCHOS_BUNDLE_ID=com.example.agentwatch\n' >"$T/x.env"
bash "$A" xcconfig "$T/x.env" "$T/x.xcconfig"
expect 0 $? "xcconfig: exit 0"
x="$(cat "$T/x.xcconfig")"
contains "$x" "AW_RELAY_DOMAIN = relay.example.com" "xcconfig: domain"
contains "$x" 'AW_RELAY_URL = https:/$()/$(AW_RELAY_DOMAIN)' "xcconfig: URL survives the // comment rule"
contains "$x" "PRODUCT_BUNDLE_IDENTIFIER = com.example.agentwatch" "xcconfig: bundle id"

printf 'AW_RELAY_DOMAIN=relay.example.com\nAW_WATCHOS_BUNDLE_ID=\n' >"$T/x2.env"
bash "$A" xcconfig "$T/x2.env" "$T/x2.xcconfig"
lacks "$(grep -v '^//' "$T/x2.xcconfig")" "PRODUCT_BUNDLE_IDENTIFIER" "xcconfig: no bundle id when unset"

printf 'AW_RELAY_DOMAIN=https://relay.example.com\n' >"$T/x3.env"
bash "$A" xcconfig "$T/x3.env" "$T/x3.xcconfig" 2>/dev/null
expect 1 $? "xcconfig: domain with a scheme refused"
printf 'AW_WATCHOS_BUNDLE_ID=com.example.x\n' >"$T/x4.env"
bash "$A" xcconfig "$T/x4.env" "$T/x4.xcconfig" 2>/dev/null
expect 1 $? "xcconfig: missing domain refused"

# ── Makefile ──
m() { make -s -C "$ROOT" "$@" 2>&1; }
out="$(m config AW_ENV_FILE="$T/mk.env")"
expect 0 $? "make config: exit 0"
expect 600 "$(mode "$T/mk.env")" "make config: mode 0600"
mtok="$(sed -n 's/^AW_HOST_TOKEN=//p' "$T/mk.env")"
printf '%s' "$mtok" | grep -Eq '^[0-9a-f]{64}$' && ok || no "make config: no token generated"
lacks "$out" "$mtok" "make config: token printed"
expect "$(grep -v '^AW_HOST_TOKEN=' "$ROOT/agent-watch.env.example")" "$(grep -v '^AW_HOST_TOKEN=' "$T/mk.env")" "make config: copy of the example"

out="$(m configure-bridge AW_ENV_FILE="$T/missing.env")"
expect 2 $? "make configure-bridge without the file: refused"
contains "$out" "make config" "make configure-bridge without the file: says to run make config"

# The example's placeholder domain is refused before anything is built or written.
out="$(m configure-bridge AW_ENV_FILE="$T/mk.env" ARGS="--config $T/never.toml")"
expect 2 $? "make configure-bridge with relay.example.com: refused"
contains "$out" "example value" "make configure-bridge: says the domain is the placeholder"
[ -e "$T/never.toml" ] && no "make configure-bridge wrote a config despite the refusal" || ok
lacks "$out" "$mtok" "make configure-bridge: token printed"

printf 'AW_RELAY_DOMAIN=relay.test.invalid\nAW_HOST_TOKEN=\n' >"$T/notok.env"
out="$(m configure-bridge AW_ENV_FILE="$T/notok.env" ARGS="--config $T/never.toml")"
expect 2 $? "make configure-bridge without a token: refused"
contains "$out" "AW_HOST_TOKEN is not set" "make configure-bridge: names the missing token"

out="$(m deploy-relay AW_ENV_FILE="$T/mk.env")"
expect 2 $? "make deploy-relay without AW_RELAY_SSH: refused"
contains "$out" "AW_RELAY_SSH is not set" "make deploy-relay: names the missing key"

printf 'AW_RELAY_DOMAIN=relay.test.invalid\nAW_WATCHOS_BUNDLE_ID=com.example.agentwatch\n' >"$T/w.env"
out="$(m watchos-config AW_ENV_FILE="$T/w.env" WATCHOS_XCCONFIG="$T/w.xcconfig")"
expect 0 $? "make watchos-config: exit 0"
contains "$(cat "$T/w.xcconfig")" "AW_RELAY_DOMAIN = relay.test.invalid" "make watchos-config: writes the xcconfig"

echo "awenv: $pass passed, $fail failed"
[ "$fail" = 0 ]
