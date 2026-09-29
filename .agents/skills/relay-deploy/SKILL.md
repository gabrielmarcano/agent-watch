---
name: relay-deploy
description: "Build and ship a new agent-watch-relay binary to the VPS, restart it, and verify it through the reverse proxy (healthz, auth, SSE). Use when relay code changed and must go live, or to diagnose the deployed relay."
---

# Deploy and check the relay

How the deploy works (first-time setup, `deploy.sh`, `--sync-env`, rollback, the proxy, devices): `deploy/relay/README.md`. This skill is the procedure for **updates and checks**.

**You may deploy, run these checks over SSH and roll back whenever your task needs it** (`AGENTS.md` §3): say so in your report, and after a deploy update the **Deployed** line in `docs/STATUS.md`.

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

- Bump `RELAY_VERSION` per `VERSIONS` when the relay changed, and commit: `deploy.sh` refuses a dirty tree.

## 2. Ship (needs SSH access; ask the owner if you lack it)

Run **one** of these:

```bash
make deploy-relay                     # target and options from the owner's agent-watch.env
# or, without agent-watch.env:
deploy/relay/deploy.sh <vps>
# or, with extra ssh/scp options:
SSH_OPTS="-i <key> -o Port=<port>" deploy/relay/deploy.sh <vps>
```

- **Never read, print or edit `agent-watch.env`** (the guards refuse writes to it). `make deploy-relay` reads it itself.
- **`--sync-env`** (`make deploy-relay ARGS=--sync-env`) also rewrites the server's env file from `agent-watch.env`: it changes live secrets, so use it only when the task needs it, and say so.
- If the new binary fails its health check, `deploy.sh` rolls back by itself and exits non-zero: report the journal lines it printed.

## 3. Check

```bash
curl -fsS https://relay.<domain>/v1/healthz
curl -s -o /dev/null -w '%{http_code}\n' https://relay.<domain>/v1/agents     # 401
ssh <vps> 'journalctl -u agent-watch-relay -n 50 --no-pager'
ssh <vps> 'agent-watch-relay version'      # the version you shipped, with its commit
```

- The journal must show `host connected` within a minute (the bridge reconnects on its own).
- **SSE through the proxy** (with a device token the owner gives you): `curl -N -H "Authorization: Bearer <device_token>" https://relay.<domain>/v1/events` must print a `snapshot` at once, then a `:` keepalive regularly, and stay open for minutes. Bursts or silence mean the proxy buffers; a cut after a minute or two means its idle timeout is too short (`deploy/relay/README.md` § Proxy idle timeouts).
- The journal must contain no tokens and no prompt text.

**With the owner's host token** (he can run it, or export it only in his own shell):

```bash
curl -fsS -H "Authorization: Bearer $AW_HOST_TOKEN" https://relay.<domain>/v1/host/status
```

## 4. Roll back a deploy that passed its checks

`deploy/relay/README.md` § Rollback by hand.
