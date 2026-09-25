#!/usr/bin/env bash
# Build the relay, install it on the VPS and restart the service. If the new
# binary does not answer /v1/healthz within ~15 s, the previous binary (and,
# with --sync-env, the previous env file) is put back, restarted, and the
# script exits non-zero.
#
# Usage: deploy/relay/deploy.sh [--sync-env] [<ssh-target>]
#        make deploy-relay [ARGS=--sync-env]
#
#   <ssh-target>  user@host or an ~/.ssh/config alias. Without it, the target
#                 is AW_RELAY_SSH from agent-watch.env, and the ssh/scp options
#                 AW_RELAY_SSH_OPTS (unless SSH_OPTS is set).
#   SSH_OPTS      extra options passed to both ssh and scp, word-split. Use -i and
#                 -o forms (scp spells the port -P, ssh -p; -o Port= works for both):
#                 SSH_OPTS="-i ~/.ssh/relay_ed25519 -o Port=2222" deploy/relay/deploy.sh root@vps
#   --sync-env    also update /etc/agent-watch-relay/env from agent-watch.env, key
#                 by key: each relay key the file sets (AW_HOST_TOKEN, AW_LISTEN,
#                 AW_TRUSTED_PROXIES, AW_CLIENT_IP_HEADER, AW_PUSH_RESOLVED,
#                 AW_FCM_CREDENTIALS, AW_NTFY_*) replaces the server's line; other
#                 lines and comments stay. The old file is kept as env.bak-<UTC time>.
#                 Values travel in a 0600 file over scp and are never printed.
#   AW_ENV_FILE   the config file (default: agent-watch.env at the repo root).
#
# The SSH user must be able to write /usr/local/bin and /etc/agent-watch-relay
# and run systemctl (root). The box needs curl. The binary is stamped
# VERSION=<Makefile version>-<short sha>, and the one it replaces is kept as
# /usr/local/bin/agent-watch-relay.prev.
set -euo pipefail

usage() {
	echo "usage: deploy.sh [--sync-env] [<ssh-target>]   (optional env: SSH_OPTS=\"-i key -o Port=2222\", AW_ENV_FILE)" >&2
	exit "${1:-1}"
}

sync_env=0
VPS=""
while [ $# -gt 0 ]; do
	case "$1" in
	--sync-env) sync_env=1 ;;
	-h | --help) usage 0 ;;
	-*) echo "deploy.sh: unknown option $1" >&2 && usage ;;
	*)
		[ -z "$VPS" ] || usage
		VPS="$1"
		;;
	esac
	shift
done

repo="$(git rev-parse --show-toplevel)"
awenv="$repo/tools/config/awenv.sh"
env_file="${AW_ENV_FILE:-$repo/agent-watch.env}"
case "$env_file" in /*) ;; *) env_file="$PWD/$env_file" ;; esac

# file_value KEY: a non-secret value of the env file, refused when quoted.
file_value() {
	local v
	v="$("$awenv" get "$env_file" "$1")"
	case "$v" in *\"* | *\'*)
		echo "deploy.sh: $1 in $env_file is quoted; write the value bare" >&2
		exit 1
		;;
	esac
	printf '%s' "$v"
}

if [ -z "$VPS" ]; then
	if [ ! -f "$env_file" ]; then
		echo "deploy.sh: no <ssh-target> given and no $env_file to read AW_RELAY_SSH from" >&2
		usage
	fi
	VPS="$(file_value AW_RELAY_SSH)"
	[ -n "$VPS" ] || {
		echo "deploy.sh: no <ssh-target> given and AW_RELAY_SSH is empty in $env_file" >&2
		exit 1
	}
	if [ -z "${SSH_OPTS+set}" ]; then
		SSH_OPTS="$(file_value AW_RELAY_SSH_OPTS)"
	fi
fi
# shellcheck disable=SC2206 # word splitting of SSH_OPTS is intended
SSH_ARGS=(${SSH_OPTS:-})

local_tmp="$(mktemp -d)"
trap 'rm -rf "$local_tmp"' EXIT

if [ "$sync_env" = 1 ]; then
	[ -f "$env_file" ] || {
		echo "deploy.sh: --sync-env needs $env_file (make config)" >&2
		exit 1
	}
	# Validated before anything is built; the 0600 file holds the secrets.
	"$awenv" relay-env "$env_file" "$local_tmp/relay.env"
	if [ ! -s "$local_tmp/relay.env" ]; then
		echo "deploy.sh: --sync-env: $env_file sets no relay key" >&2
		exit 1
	fi
fi

cd "$repo"

if [ -n "$(git status --porcelain)" ]; then
	echo "deploy.sh: refusing to deploy a dirty tree (the binary would not match any commit):" >&2
	git status --short >&2
	exit 1
fi

base_version="$(sed -n 's/^VERSION ?= *//p' Makefile)"
version="${base_version:-dev}-$(git rev-parse --short HEAD)"
make relay-linux VERSION="$version"

echo "deploying $version to $VPS"
cp bin/agent-watch-relay-linux-amd64 "$local_tmp/agent-watch-relay"
files=("$local_tmp/agent-watch-relay")
if [ "$sync_env" = 1 ]; then
	files+=("$awenv" "$local_tmp/relay.env")
fi

remote_dir="$(ssh ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "$VPS" 'mktemp -d /tmp/agent-watch-relay.XXXXXX')"
scp -q ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "${files[@]}" "$VPS:$remote_dir/"

# shellcheck disable=SC2087 # the remote script is quoted on purpose; its args follow bash -s
ssh ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "$VPS" bash -s -- "$remote_dir" "$version" "$sync_env" <<'REMOTE'
set -euo pipefail
remote_dir="$1"
version="$2"
sync_env="$3"
bin=/usr/local/bin/agent-watch-relay
prev="$bin.prev"
env_file=/etc/agent-watch-relay/env
env_backup=""
env_tmp=""
trap 'rm -rf "$remote_dir"; if [ -n "$env_tmp" ]; then rm -f "$env_tmp"; fi' EXIT

# Health-check the address the relay really listens on (AW_LISTEN).
health_url() {
	local listen hostport
	listen="$(sed -n 's/^[[:space:]]*AW_LISTEN[[:space:]]*=//p' "$env_file" 2>/dev/null | tail -n 1 | tr -d "\"' ")"
	listen="${listen:-:8080}"
	case "$listen" in
		:*) hostport="127.0.0.1$listen" ;;
		0.0.0.0:*) hostport="127.0.0.1:${listen#0.0.0.0:}" ;;
		"[::]:"*) hostport="[::1]:${listen#"[::]:"}" ;;
		*) hostport="$listen" ;;
	esac
	echo "http://$hostport/v1/healthz"
}

healthy() {
	for _ in $(seq 1 15); do
		if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	return 1
}

# --sync-env: merge the synced keys into a copy that keeps the live file's
# owner, group and mode, then swap it in. Only key names are printed.
if [ "$sync_env" = 1 ]; then
	if [ ! -f "$env_file" ]; then
		echo "$env_file does not exist: create it first (docs/phases/3c-relay-deploy.md), then re-run with --sync-env" >&2
		exit 1
	fi
	env_tmp="$(mktemp "$env_file.sync.XXXXXX")"
	cp -p "$env_file" "$env_tmp"
	bash "$remote_dir/awenv.sh" merge "$env_file" "$remote_dir/relay.env" "$env_tmp"
	if cmp -s "$env_file" "$env_tmp"; then
		echo "env: $env_file is already up to date"
	else
		env_backup="$env_file.bak-$(date -u +%Y%m%dT%H%M%SZ)"
		cp -p "$env_file" "$env_backup"
		mv "$env_tmp" "$env_file"
		echo "env: updated $env_file (previous version: $env_backup)"
	fi
	rm -f "$env_tmp"
	env_tmp=""
fi

url="$(health_url)"
if [ -f "$bin" ]; then
	cp -p "$bin" "$prev"
fi
install -m 0755 "$remote_dir/agent-watch-relay" "$bin"
systemctl restart agent-watch-relay

if healthy; then
	echo "deployed $("$bin" version); $url is healthy"
	systemctl --no-pager status agent-watch-relay | head -5
	exit 0
fi

echo "agent-watch-relay $version is not healthy at $url; rolling back" >&2
journalctl -u agent-watch-relay -n 30 --no-pager >&2 || true
rolled=""
if [ -n "$env_backup" ]; then
	cp -p "$env_backup" "$env_file"
	rolled="the env file"
fi
if [ -f "$prev" ]; then
	install -m 0755 "$prev" "$bin"
	rolled="${rolled:+$rolled and }the binary"
fi
if [ -z "$rolled" ]; then
	echo "no previous binary to roll back to; the service is DOWN" >&2
	exit 1
fi
url="$(health_url)"
systemctl restart agent-watch-relay
if healthy; then
	echo "rolled back $rolled: $("$bin" version); $url is healthy" >&2
else
	echo "rolled back $rolled, but the relay is not healthy either; the service is DOWN" >&2
fi
exit 1
REMOTE
