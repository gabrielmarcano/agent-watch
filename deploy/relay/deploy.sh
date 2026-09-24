#!/usr/bin/env bash
set -euo pipefail

VPS="${1:?usage: deploy.sh <ssh-target>}"

make relay-linux

scp bin/agent-watch-relay-linux-amd64 "$VPS":/tmp/agent-watch-relay
ssh "$VPS" 'install -m 0755 /tmp/agent-watch-relay /usr/local/bin/agent-watch-relay && systemctl restart agent-watch-relay && systemctl --no-pager status agent-watch-relay | head -5'
