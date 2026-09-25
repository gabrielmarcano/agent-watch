# Phase 3c — Deploy the Relay to the VPS behind a TLS Reverse Proxy

> **Goal:** `https://relay.<domain>` serving the relay 24/7 on a small VPS, behind a TLS reverse proxy (nginx, Caddy or Nginx Proxy Manager; optionally with Cloudflare in front), restarting on failure and surviving reboots.
>
> **Status:** done (the files exist and the relay runs). Day-to-day operations, the Nginx Proxy Manager-in-docker topology, client IP and device revocation are in [`deploy/relay/README.md`](../../deploy/relay/README.md).

| | |
|---|---|
| **Depends on** | 3a (3b optional: without it the relay runs with push disabled) |
| **Touches** | `deploy/relay/**`, `docs/STATUS.md` |
| **Needs the owner?** | **Yes.** SSH access to the VPS, DNS (and Cloudflare, if used), the domain name, and the secrets. The agent prepares files and commands; the owner runs anything that needs credentials |

**Placeholders used below** (the owner provides the real values; never commit them):

| Placeholder | Meaning |
|---|---|
| `<vps>` | SSH target of the VPS, e.g. `root@203.0.113.10` or an `~/.ssh/config` alias |
| `<domain>` | Base domain (managed in Cloudflare if you use it) |
| `relay.<domain>` | Public hostname of the relay |

---

## Steps

### 1. Repository files (`deploy/relay/`)

The files are the source of truth; this table says what each one must keep. **No real secrets** in any of them.

| File | What it is |
|---|---|
| `agent-watch-relay.service` | systemd unit: runs `/usr/local/bin/agent-watch-relay serve` as `agentwatch`, `EnvironmentFile=/etc/agent-watch-relay/env`, `Restart=always`. Hardened: no capabilities, `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `UMask=0077`, only `AF_INET`/`AF_INET6`/`AF_UNIX` (the admin socket needs `AF_UNIX`), `SystemCallFilter=@system-service`; writable only `/var/lib/agent-watch-relay` (`StateDirectory`, mode `0700`). **Not yet checked with `systemd-analyze security`** |
| `env.example` | Every variable of `contracts.md` §5 with safe defaults: `AW_LISTEN=127.0.0.1:8080`, `AW_HOST_TOKEN` placeholder, `AW_DATA_DIR`, and commented `AW_FCM_CREDENTIALS`, `AW_NTFY_*`, `AW_PUSH_RESOLVED`, `AW_TRUSTED_PROXIES`, `AW_CLIENT_IP_HEADER` |
| `nginx.conf.example` | A full `server` block: TLS, WebSocket upgrade (`map $http_upgrade $connection_upgrade`), `X-Forwarded-For` / `X-Real-IP`, `proxy_buffering off` and `proxy_cache off` for SSE, `proxy_read_timeout`/`proxy_send_timeout 90s`. Relay env: `AW_TRUSTED_PROXIES=127.0.0.1/32` |
| `Caddyfile.example` | Caddy with a Cloudflare Origin Certificate and `flush_interval -1` for SSE. Relay env: `AW_TRUSTED_PROXIES=127.0.0.1/32` |
| `deploy.sh` | Builds and ships the binary (below). Touches secrets only with `--sync-env`, which updates the server's env file from `agent-watch.env` (`deploy/relay/README.md`) |
| `Dockerfile` | Optional container: distroless `nonroot`, a `0700` data dir owned by uid 65532, `VOLUME /var/lib/agent-watch-relay`. Build it from the repo root after `make relay-linux` |
| `README.md` | Operations: deploys, client IP per topology, Nginx Proxy Manager in docker, firewall, hardening, devices, lost watch |

**`deploy.sh <ssh-target>`** (optional `SSH_OPTS="-i <key> -o Port=<port>"`, passed to both `ssh` and `scp`):

1. Refuses a dirty tree (uncommitted or untracked files), so the binary always matches a commit.
2. `make relay-linux VERSION=<Makefile VERSION>-<short sha>`.
3. Copies it to a temp dir on the box, keeps the current binary as `/usr/local/bin/agent-watch-relay.prev`, installs the new one and restarts the unit.
4. Curls `/v1/healthz` on the `AW_LISTEN` address from `/etc/agent-watch-relay/env` for up to 15 s. If it never answers: prints the last journal lines, restores `.prev`, restarts, and exits non-zero.

Needs root over SSH (it writes `/usr/local/bin` and runs `systemctl`) and `curl` on the box.

### 2. One-time VPS setup (owner runs, or the agent with explicit permission)

```bash
ssh <vps>
useradd --system --home /var/lib/agent-watch-relay --shell /usr/sbin/nologin agentwatch
mkdir -p /etc/agent-watch-relay && chmod 0750 /etc/agent-watch-relay
# create /etc/agent-watch-relay/env from env.example; with agent-watch.env on the Mac, keep the
# AW_HOST_TOKEN placeholder: `make deploy-relay ARGS=--sync-env` writes the token `make config` generated.
# Without agent-watch.env: AW_HOST_TOKEN=$(openssl rand -hex 32), also given to the bridge `configure`.
chown root:agentwatch /etc/agent-watch-relay/env && chmod 0640 /etc/agent-watch-relay/env
# copy the unit
cp agent-watch-relay.service /etc/systemd/system/   # scp it first
systemctl daemon-reload && systemctl enable agent-watch-relay
```

- **Listen address:** the address your proxy reaches, never a public interface: `127.0.0.1:8080` for nginx/Caddy on the host; the docker bridge `172.17.0.1:8080` for Nginx Proxy Manager in docker, plus an `After=docker.service` drop-in (`deploy/relay/README.md`).
- **`AW_TRUSTED_PROXIES`:** your proxy's address or network, so the pairing rate limit sees the real client (`deploy/relay/README.md` has the table per topology).
- **A custom `AW_DATA_DIR`** must be added to `ReadWritePaths=` in a drop-in.

Then, from the Mac: `make deploy-relay ARGS=--sync-env` (target, options and relay keys from `agent-watch.env`), or `deploy/relay/deploy.sh <vps>` without it.

### 3. TLS in front of the relay (owner)

Any reverse proxy that terminates TLS and passes WebSockets and unbuffered SSE works: nginx or Caddy on the host (examples above), or Nginx Proxy Manager in docker (`deploy/relay/README.md`: forward to `http://172.17.0.1:8080` with **Websockets Support** on).

**Optional: Cloudflare** (in the dashboard):

1. **DNS:** an `A` record `relay` → the VPS IP, **Proxied** (orange cloud).
2. **SSL/TLS mode:** **Full (strict)**.
3. **Origin Certificates:** create one for `relay.<domain>`, and install it where your proxy expects it.
4. **Network:** WebSockets **On** (the default).
5. **Caching:** add a Cache Rule "Bypass cache" for `relay.<domain>/*`. SSE and API responses must never be cached.
6. **Client IP:** set `AW_CLIENT_IP_HEADER=CF-Connecting-IP` **only** if the origin accepts traffic from Cloudflare alone; otherwise anyone can send that header.

**Firewall (VPS):** the relay port must not be reachable from the internet; check it with `ss -ltnp` (`deploy/relay/README.md`, step 5). **Do not enable `ufw` blindly on a docker host:** ports published by containers bypass it, its default `deny incoming` drops the proxy container's traffic to `172.17.0.1:8080`, and enabling it without an SSH rule locks you out. The README has the safe order of commands.

### 4. Verify

From the Mac:

```bash
curl -fsS https://relay.<domain>/v1/healthz                       # {"ok":true}
curl -s -o /dev/null -w '%{http_code}\n' https://relay.<domain>/v1/agents   # 401 without a token
curl -fsS -H "Authorization: Bearer $AW_HOST_TOKEN" https://relay.<domain>/v1/host/status
```

Then point the bridge at it and start the service:

```bash
make configure-bridge            # relay URL + token from agent-watch.env; token never in argv
herdr plugin action invoke --plugin herdr-agent-watch start
herdr plugin action invoke --plugin herdr-agent-watch status   # "relay: connected to relay.<domain>"; the relay api line shows "host_online":true
```

**SSE through the proxy**, after pairing a device (Phase 4):

```bash
curl -N -H "Authorization: Bearer <device_token>" https://relay.<domain>/v1/events
```

A `snapshot` must arrive immediately, then `:` keepalives every 15 s, for at least 3 minutes without disconnecting.

---

## Definition of done

- [ ] `deploy/relay/` contains the unit, `env.example`, the proxy example(s), `deploy.sh` (executable) and the optional Dockerfile. **No real secrets.**
- [ ] The relay runs on the VPS under systemd, and survives `systemctl restart` and a reboot.
- [ ] All the Verify commands pass; SSE stays open for more than 3 minutes through the proxy.
- [ ] The bridge on the Mac shows `relay_connected: true`.
- [ ] `docs/STATUS.md` Phase 3c ticked with the public URL, which is fine to record, but **not** the tokens.

---

## Pitfalls

- **Cloudflare "Flexible" SSL** sends plain HTTP to the origin. Use Full (strict).
- **Proxy buffering** makes SSE arrive in bursts or never. Use `flush_interval -1` in Caddy, `proxy_buffering off` in nginx.
- **Idle timeouts:** proxies (Cloudflare: about 100 s) close idle connections. The relay's 15 s SSE keepalive and the 30 s WebSocket pings (both the bridge and the relay send them) prevent that. Do not lengthen them.
- **Binding to `0.0.0.0`** publishes the relay without TLS. Bind to the address the proxy reaches.
- **`AW_TRUST_CF_IP` was removed.** An old env file that still sets it only gets a startup warning; set `AW_TRUSTED_PROXIES` instead.
- **Secrets in shell history:** prefer editing `/etc/agent-watch-relay/env` with an editor over `echo TOKEN >> env`.

---

## Prompt for the executing agent

```
You are executing Phase 3c (relay deployment) of the Agent Watch refactor in this repository.
Read docs/phases/3c-relay-deploy.md. Create the files under deploy/relay/ exactly as specified, with placeholders and no
real secrets. Do NOT run anything against the VPS or Cloudflare yourself: prepare the exact commands and hand them to the
owner, who runs the steps that need credentials. After the owner confirms deployment, run the Verify commands that need
no secrets, report the results, tick Phase 3c in docs/STATUS.md, and commit only deploy/relay and docs/STATUS.md.
```
