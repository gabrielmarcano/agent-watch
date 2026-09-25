---
name: relay-deploy
description: "Build and ship a new agent-watch-relay binary to the VPS, restart it, and verify it through the reverse proxy (healthz, auth, SSE). Use when relay code changed and must go live, or to diagnose the deployed relay."
---

# Deploy and check the relay

The first-time VPS setup is in `docs/phases/3c-relay-deploy.md`; day-to-day operations (the Nginx Proxy Manager topology, client IP, devices) in `deploy/relay/README.md`. This skill covers **updates and checks**.

**The owner provides:**

| Placeholder | Meaning |
|---|---|
| `<vps>` | SSH target of the VPS |
| `relay.<domain>` | Public hostname of the relay |

Never write their real values into the repo.

## 1. Verify before shipping

```bash
go vet ./... && go test -race ./pkg/relay/... ./pkg/push/... ./cmd/relay/...
make relay-linux
file bin/agent-watch-relay-linux-amd64    # must say "statically linked"
```

## 2. Ship (needs SSH access; ask the owner if you lack it)

```bash
deploy/relay/deploy.sh <vps>
SSH_OPTS="-i <key> -o Port=<port>" deploy/relay/deploy.sh <vps>   # extra ssh/scp options
```

- **Clean tree only:** it refuses to run with uncommitted or untracked changes, so the binary always matches a commit. Commit first.
- **Version stamp:** `<Makefile VERSION>-<short sha>`; check it with `agent-watch-relay version` on the box.
- **`SSH_OPTS`** goes to both `ssh` and `scp`. Use `-o Port=…`, never `-p` (`scp` spells it `-P`).
- It keeps the replaced binary as `/usr/local/bin/agent-watch-relay.prev`, installs the new one, restarts the unit, and curls `/v1/healthz` on the `AW_LISTEN` address for up to 15 s. **If the new binary never answers, it prints the last journal lines, restores `.prev`, restarts, and exits non-zero.**
- It never touches `/etc/agent-watch-relay/env`. Needs root over SSH and `curl` on the box.

## 3. Check

```bash
curl -fsS https://relay.<domain>/v1/healthz
curl -s -o /dev/null -w '%{http_code}\n' https://relay.<domain>/v1/agents     # 401
ssh <vps> 'journalctl -u agent-watch-relay -n 50 --no-pager'
```

- The journal must show `host connected` within a minute (the bridge reconnects on its own).
- The journal must contain no tokens and no prompt text.

**With the owner's host token** (he can run it, or export it only in his own shell):

```bash
curl -fsS -H "Authorization: Bearer $AW_HOST_TOKEN" https://relay.<domain>/v1/host/status
```

## Rollback

`deploy.sh` rolls back by itself when the new binary fails its health check. To go back after a deploy that passed it, restore the kept binary:

```bash
ssh <vps> 'install -m 0755 /usr/local/bin/agent-watch-relay.prev /usr/local/bin/agent-watch-relay && systemctl restart agent-watch-relay'
```

Only one previous binary is kept: each deploy overwrites `.prev`.
