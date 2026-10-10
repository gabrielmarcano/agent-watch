#!/usr/bin/env bash
# Reads and renders agent-watch.env, the shared configuration file (see
# agent-watch.env.example). Used by the Makefile and deploy/relay/deploy.sh;
# deploy.sh also copies it to the relay host to run `merge` there.
#
#   awenv.sh init <env> <example>          create <env> from <example> (0600) and
#                                          generate AW_HOST_TOKEN when it is empty
#   awenv.sh get <env> <key>               print a non-secret value ("" when unset)
#   awenv.sh relay-env <env> <out>         write the relay server keys <env> sets to <out> (0600)
#   awenv.sh merge <current> <updates> <out>
#                                          write <current> with each key of <updates>
#                                          replaced in place or appended
#   awenv.sh xcconfig <env> <out>          write the watchOS xcconfig
#
# Secrets never reach argv or output: values go from file to file, and the
# messages name keys only. Syntax of the file (make's, since the Makefile
# includes it): KEY=value; '#' starts a comment anywhere; values are trimmed
# and literal; the last assignment of a key wins.
#
# Portable to bash 3.2 (macOS) and POSIX awk (BSD awk, mawk, gawk).
set -euo pipefail

RELAY_KEYS="AW_HOST_TOKEN AW_HOST_ID AW_HOST_NAME AW_LISTEN AW_TRUSTED_PROXIES AW_CLIENT_IP_HEADER AW_PUSH_RESOLVED AW_PUSH_PRESENCE_IDLE AW_FCM_CREDENTIALS AW_NTFY_URL AW_NTFY_TOPIC AW_NTFY_TOKEN"
DOMAIN_RE='^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$'
BUNDLE_RE='^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$'
HEX64_RE='^[0-9a-fA-F]{64}$'

die() {
	echo "awenv: $*" >&2
	exit 1
}

# parse FILE: prints "KEY=value" for every assignment, in file order, with
# comments stripped and the key and value trimmed. Malformed lines are skipped
# (make and the Go reader reject them; the watch build ignores them).
parse() {
	awk '
		{ line = $0; c = index(line, "#"); if (c > 0) line = substr(line, 1, c - 1) }
		{ sub(/^[ \t\r]+/, "", line); sub(/[ \t\r]+$/, "", line) }
		line == "" { next }
		{
			eq = index(line, "="); if (eq == 0) next
			key = substr(line, 1, eq - 1); sub(/[ \t]+$/, "", key)
			if (key !~ /^[A-Za-z_][A-Za-z0-9_]*$/) next
			val = substr(line, eq + 1); sub(/^[ \t]+/, "", val)
			print key "=" val
		}
	' "$1"
}

# has FILE KEY: whether FILE assigns KEY (even an empty value).
has() {
	parse "$1" | grep -q "^$2="
}

# value FILE KEY: the last value of KEY ("" when unset). Callers keep it in
# a variable; only non-secret keys may be printed.
value() {
	parse "$1" | awk -v k="$2" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2) } END { printf "%s", v }'
}

is_secret() {
	case "$1" in *TOKEN* | *SECRET* | *PASSWORD*) return 0 ;; esac
	return 1
}

cmd_get() {
	[ $# -eq 2 ] || die "usage: awenv.sh get <env> <key>"
	[ -f "$1" ] || die "$1 not found; create it with \`make config\`"
	is_secret "$2" && die "refusing to print $2: it is a secret"
	value "$1" "$2"
	echo
}

cmd_init() {
	[ $# -eq 2 ] || die "usage: awenv.sh init <env> <example>"
	local env="$1" example="$2" tok tmp
	if [ ! -f "$env" ]; then
		[ -f "$example" ] || die "$example not found"
		(umask 077 && cp "$example" "$env")
		echo "created $env from $example"
	fi
	chmod 600 "$env"

	tok="$(value "$env" AW_HOST_TOKEN)"
	if [ -n "$tok" ]; then
		if printf '%s' "$tok" | grep -Eq "$HEX64_RE"; then
			echo "$env: AW_HOST_TOKEN is already set (kept)"
			return 0
		fi
		die "AW_HOST_TOKEN in $env is not 64 hex characters: fix it, or empty it to generate a new one"
	fi

	command -v openssl >/dev/null 2>&1 || die "openssl is needed to generate AW_HOST_TOKEN"
	tmp="$(umask 077 && mktemp "$env.XXXXXX")"
	# The token goes openssl → pipe → awk → file: never argv, env or output.
	# Every existing assignment gets it (the last one would win anyway); with
	# none, it is appended.
	if ! openssl rand -hex 32 | awk '
		FILENAME == "/dev/stdin" { if (tok == "") tok = $0; next }
		{
			t = $0; sub(/^[ \t]+/, "", t)
			if (t ~ /^AW_HOST_TOKEN[ \t]*=/) { print "AW_HOST_TOKEN=" tok; done = 1; next }
			print
		}
		END {
			if (tok !~ /^[0-9a-f]+$/ || length(tok) != 64) exit 3
			if (!done) print "AW_HOST_TOKEN=" tok
		}
	' /dev/stdin "$env" >"$tmp"; then
		rm -f "$tmp"
		die "could not generate AW_HOST_TOKEN; $env is unchanged"
	fi
	mv "$tmp" "$env"
	echo "$env: generated AW_HOST_TOKEN (not shown; it stays in the file)"
}

cmd_relay_env() {
	[ $# -eq 2 ] || die "usage: awenv.sh relay-env <env> <out>"
	local env="$1" out="$2" key val keys=""
	[ -f "$env" ] || die "$env not found; create it with \`make config\`"
	(umask 077 && : >"$out")
	chmod 600 "$out"
	for key in $RELAY_KEYS; do
		has "$env" "$key" || continue
		val="$(value "$env" "$key")"
		case "$val" in
		*\"* | *\'* | *\\* | *\$*)
			rm -f "$out"
			die "$key in $env contains a quote, a backslash or \$: write the value bare"
			;;
		esac
		if [ "$key" = AW_HOST_TOKEN ] && ! printf '%s' "$val" | grep -Eq "$HEX64_RE"; then
			rm -f "$out"
			die "AW_HOST_TOKEN in $env is not 64 hex characters (empty? run \`make config\`)"
		fi
		printf '%s=%s\n' "$key" "$val" >>"$out"
		keys="$keys $key"
	done
	echo "relay keys in $env:${keys:- none}" >&2
}

# merge: the key-by-key update of the relay's env file. Lines that assign a
# key of <updates> (after optional blanks) are replaced; every other line,
# comments included, is kept as it was. Keys <current> does not assign are
# appended. Reports key names (never values) on stderr.
cmd_merge() {
	[ $# -eq 3 ] || die "usage: awenv.sh merge <current> <updates> <out>"
	[ -f "$1" ] || die "$1 not found"
	[ -f "$2" ] || die "$2 not found"
	awk -v updates="$2" -v q="'" '
		BEGIN {
			while ((getline line < updates) > 0) {
				eq = index(line, "="); if (eq == 0) continue
				k = substr(line, 1, eq - 1)
				if (!(k in upd)) order[++n] = k
				upd[k] = substr(line, eq + 1)
			}
			close(updates)
		}
		{
			t = $0; sub(/^[ \t]+/, "", t)
			eq = index(t, "=")
			if (t !~ /^[#;]/ && eq > 0) {
				k = substr(t, 1, eq - 1); sub(/[ \t]+$/, "", k)
				if (k in upd) {
					v = substr(t, eq + 1); sub(/^[ \t]+/, "", v); sub(/[ \t\r]+$/, "", v)
					f = substr(v, 1, 1)
					if (length(v) >= 2 && (f == "\"" || f == q) && substr(v, length(v), 1) == f)
						v = substr(v, 2, length(v) - 2) # systemd unquotes; compare unquoted
					if (v != upd[k]) changed[k] = 1
					print k "=" upd[k]; seen[k] = 1
					next
				}
			}
			print
		}
		END {
			for (i = 1; i <= n; i++) {
				k = order[i]
				if (k in seen) continue
				if (!header) { print ""; print "# Added from agent-watch.env by deploy.sh --sync-env"; header = 1 }
				print k "=" upd[k]; added = added " " k
			}
			for (i = 1; i <= n; i++) if (order[i] in changed) ch = ch " " order[i]
			for (i = 1; i <= n; i++) if ((order[i] in seen) && !(order[i] in changed)) same = same " " order[i]
			printf "env: changed:%s; added:%s; unchanged:%s\n", (ch == "" ? " none" : ch), (added == "" ? " none" : added), (same == "" ? " none" : same) > "/dev/stderr"
		}
	' "$1" >"$3"
}

cmd_xcconfig() {
	[ $# -eq 2 ] || die "usage: awenv.sh xcconfig <env> <out>"
	local env="$1" out="$2" domain bundle
	[ -f "$env" ] || die "$env not found; create it with \`make config\`"
	domain="$(value "$env" AW_RELAY_DOMAIN)"
	bundle="$(value "$env" AW_WATCHOS_BUNDLE_ID)"
	[ -n "$domain" ] || die "AW_RELAY_DOMAIN is not set in $env"
	printf '%s' "$domain" | grep -Eq "$DOMAIN_RE" ||
		die "AW_RELAY_DOMAIN in $env is not a bare host name such as relay.example.com"
	if [ -n "$bundle" ] && ! printf '%s' "$bundle" | grep -Eq "$BUNDLE_RE"; then
		die "AW_WATCHOS_BUNDLE_ID in $env is not a bundle id such as com.example.agentwatch"
	fi
	{
		echo "// Generated from agent-watch.env by \`make watchos-config\`. Do not edit or commit."
		echo "// Not used by the legacy Xcode project: the Phase 6 rewrite sets it as its base configuration."
		echo "AW_RELAY_DOMAIN = $domain"
		echo "// \"//\" starts a comment in an xcconfig; the empty \$() keeps it in the value."
		echo "AW_RELAY_URL = https:/\$()/\$(AW_RELAY_DOMAIN)"
		if [ -n "$bundle" ]; then
			echo "PRODUCT_BUNDLE_IDENTIFIER = $bundle"
		else
			echo "// AW_WATCHOS_BUNDLE_ID is empty: the project's own PRODUCT_BUNDLE_IDENTIFIER applies."
		fi
	} >"$out"
}

main() {
	[ $# -ge 1 ] || die "usage: awenv.sh init|get|relay-env|merge|xcconfig ..."
	local sub="$1"
	shift
	case "$sub" in
	init) cmd_init "$@" ;;
	get) cmd_get "$@" ;;
	relay-env) cmd_relay_env "$@" ;;
	merge) cmd_merge "$@" ;;
	xcconfig) cmd_xcconfig "$@" ;;
	*) die "unknown command: $sub" ;;
	esac
}

main "$@"
