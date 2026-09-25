#!/usr/bin/env bash
# Build the relay, install it on the VPS and restart the service. If the new
# binary does not answer /v1/healthz within ~15 s, the previous binary is put
# back, restarted, and the script exits non-zero.
#
# Usage: deploy/relay/deploy.sh <ssh-target>
#
#   SSH_OPTS  extra options passed to both ssh and scp, word-split. Use -i and
#             -o forms (scp spells the port -P, ssh -p; -o Port= works for both):
#             SSH_OPTS="-i ~/.ssh/relay_ed25519 -o Port=2222" deploy/relay/deploy.sh root@vps
#
# The SSH user must be able to write /usr/local/bin and run systemctl (root).
# The box needs curl. The binary is stamped VERSION=<Makefile version>-<short sha>,
# and the one it replaces is kept as /usr/local/bin/agent-watch-relay.prev.
set -euo pipefail

VPS="${1:?usage: deploy.sh <ssh-target>   (optional env: SSH_OPTS=\"-i key -o Port=2222\")}"
# shellcheck disable=SC2206 # word splitting of SSH_OPTS is intended
SSH_ARGS=(${SSH_OPTS:-})

cd "$(git rev-parse --show-toplevel)"

if [ -n "$(git status --porcelain)" ]; then
	echo "deploy.sh: refusing to deploy a dirty tree (the binary would not match any commit):" >&2
	git status --short >&2
	exit 1
fi

base_version="$(sed -n 's/^VERSION ?= *//p' Makefile)"
version="${base_version:-dev}-$(git rev-parse --short HEAD)"
make relay-linux VERSION="$version"

remote_dir="$(ssh ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "$VPS" 'mktemp -d /tmp/agent-watch-relay.XXXXXX')"
scp ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} bin/agent-watch-relay-linux-amd64 "$VPS:$remote_dir/agent-watch-relay"

# shellcheck disable=SC2087 # the remote script is quoted on purpose; its args follow bash -s
ssh ${SSH_ARGS[@]+"${SSH_ARGS[@]}"} "$VPS" bash -s -- "$remote_dir" "$version" <<'REMOTE'
set -euo pipefail
remote_dir="$1"
version="$2"
bin=/usr/local/bin/agent-watch-relay
prev="$bin.prev"
env_file=/etc/agent-watch-relay/env
trap 'rm -rf "$remote_dir"' EXIT

# Health-check the address the relay really listens on (AW_LISTEN).
listen="$(sed -n 's/^[[:space:]]*AW_LISTEN=//p' "$env_file" 2>/dev/null | tail -n 1 | tr -d "\"' ")"
listen="${listen:-:8080}"
case "$listen" in
	:*) hostport="127.0.0.1$listen" ;;
	0.0.0.0:*) hostport="127.0.0.1:${listen#0.0.0.0:}" ;;
	"[::]:"*) hostport="[::1]:${listen#"[::]:"}" ;;
	*) hostport="$listen" ;;
esac
url="http://$hostport/v1/healthz"

healthy() {
	for _ in $(seq 1 15); do
		if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	return 1
}

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
if [ ! -f "$prev" ]; then
	echo "no previous binary to roll back to; the service is DOWN" >&2
	exit 1
fi
install -m 0755 "$prev" "$bin"
systemctl restart agent-watch-relay
if healthy; then
	echo "rolled back to $("$bin" version); $url is healthy" >&2
else
	echo "the rolled-back binary is not healthy either; the service is DOWN" >&2
fi
exit 1
REMOTE
