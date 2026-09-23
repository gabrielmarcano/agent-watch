---
name: relay-deploy
description: "Build and ship a new agent-watch-relay binary to the VPS, restart it, and verify it through Cloudflare (healthz, auth, SSE). Use when relay code changed and must go live, or to diagnose the deployed relay."
---

# Deploy and check the relay

The first-time VPS and Cloudflare setup is in `docs/phases/3c-relay-deploy.md`. This skill covers **updates and checks**.

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
bash deploy/relay/deploy.sh <vps>
```

The script copies the binary, installs it into `/usr/local/bin`, restarts the systemd unit, and prints its status. It never touches `/etc/agent-watch-relay/env`.

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

`deploy.sh` does not keep old binaries. Before a risky deploy, back up the current one:

```bash
ssh <vps> 'cp /usr/local/bin/agent-watch-relay /usr/local/bin/agent-watch-relay.prev'
```

Roll back with:

```bash
ssh <vps> 'mv /usr/local/bin/agent-watch-relay.prev /usr/local/bin/agent-watch-relay && systemctl restart agent-watch-relay'
```
