# Phase 3c — Deploy the Relay to the VPS behind Cloudflare

> **Goal:** `https://relay.<domain>` serving the relay 24/7 on the DigitalOcean VPS, behind Cloudflare, with TLS end to end, restarting on failure and surviving reboots.

| | |
|---|---|
| **Depends on** | 3a (3b optional: without it the relay runs with push disabled) |
| **Touches** | `deploy/relay/**`, `docs/STATUS.md` |
| **Needs the owner?** | **Yes.** SSH access to the VPS, Cloudflare DNS, the domain name, and the secrets. The agent prepares files and commands; the owner runs anything that needs credentials |

**Placeholders used below** (the owner provides the real values; never commit them):

| Placeholder | Meaning |
|---|---|
| `<vps>` | SSH target of the VPS, e.g. `root@203.0.113.10` or an `~/.ssh/config` alias |
| `<domain>` | Base domain managed in Cloudflare |
| `relay.<domain>` | Public hostname of the relay |

---

## Steps

### 1. Repository files (`deploy/relay/`)

**`deploy/relay/agent-watch-relay.service`** (systemd, the recommended way):

```ini
[Unit]
Description=Agent Watch relay
After=network-online.target
Wants=network-online.target

[Service]
User=agentwatch
Group=agentwatch
EnvironmentFile=/etc/agent-watch-relay/env
ExecStart=/usr/local/bin/agent-watch-relay serve
Restart=always
RestartSec=5
# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/agent-watch-relay
StateDirectory=agent-watch-relay
StateDirectoryMode=0700

[Install]
WantedBy=multi-user.target
```

**`deploy/relay/env.example`** (committed; the real `/etc/agent-watch-relay/env` is not):

```bash
AW_LISTEN=127.0.0.1:8080
AW_HOST_TOKEN=__generate_with_openssl_rand_hex_32__
AW_DATA_DIR=/var/lib/agent-watch-relay
# AW_FCM_CREDENTIALS=/etc/agent-watch-relay/firebase-service-account.json
# AW_NTFY_URL=https://ntfy.sh
# AW_NTFY_TOPIC=__random_unguessable_topic__
# AW_NTFY_TOKEN=
AW_TRUST_CF_IP=true
```

**`deploy/relay/Caddyfile.example`**. Use it only if the VPS has no reverse proxy yet. Caddy handles TLS, WebSocket and SSE with no extra flags:

```caddy
relay.<domain> {
    tls /etc/caddy/certs/origin.pem /etc/caddy/certs/origin-key.pem   # Cloudflare Origin Certificate
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1        # stream SSE immediately
    }
}
```

**`deploy/relay/nginx.conf.example`**. Use it only if the VPS already runs nginx. The important lines:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;       # WebSocket
    proxy_set_header Connection $connection_upgrade;
    proxy_set_header Host $host;
    proxy_buffering off;                          # SSE
    proxy_read_timeout 1h;                        # long-lived SSE/WS
}
```

**`deploy/relay/deploy.sh`**: builds and ships the binary. It never touches secrets.

```bash
#!/usr/bin/env bash
set -euo pipefail
VPS="${1:?usage: deploy.sh <ssh-target>}"
make relay-linux
scp bin/agent-watch-relay-linux-amd64 "$VPS":/tmp/agent-watch-relay
ssh "$VPS" 'install -m 0755 /tmp/agent-watch-relay /usr/local/bin/agent-watch-relay && systemctl restart agent-watch-relay && systemctl --no-pager status agent-watch-relay | head -5'
```

`Dockerfile` (optional; only if the owner prefers containers on the VPS):

```dockerfile
FROM gcr.io/distroless/static:nonroot
COPY bin/agent-watch-relay-linux-amd64 /agent-watch-relay
USER nonroot
ENTRYPOINT ["/agent-watch-relay", "serve"]
```

### 2. One-time VPS setup (owner runs, or the agent with explicit permission)

```bash
ssh <vps>
useradd --system --home /var/lib/agent-watch-relay --shell /usr/sbin/nologin agentwatch
mkdir -p /etc/agent-watch-relay && chmod 0750 /etc/agent-watch-relay
openssl rand -hex 32            # → AW_HOST_TOKEN (also goes into the bridge `configure`)
# create /etc/agent-watch-relay/env from env.example with the real values
chown root:agentwatch /etc/agent-watch-relay/env && chmod 0640 /etc/agent-watch-relay/env
# copy the unit
cp agent-watch-relay.service /etc/systemd/system/   # scp it first
systemctl daemon-reload && systemctl enable agent-watch-relay
```

Then run `deploy/relay/deploy.sh <vps>` from the Mac.

### 3. Cloudflare (owner, in the dashboard)

1. **DNS:** an `A` record `relay` → the VPS IP, **Proxied** (orange cloud).
2. **SSL/TLS mode:** **Full (strict)**.
3. **Origin Certificates:** create one for `relay.<domain>`, and install it where the Caddyfile or nginx expects it.
4. **Network:** WebSockets **On** (the default).
5. **Caching:** add a Cache Rule "Bypass cache" for `relay.<domain>/*`. SSE and API responses must never be cached.
6. **Firewall (VPS):** allow 443 only from Cloudflare IP ranges (`ufw` + `https://www.cloudflare.com/ips-v4`). Port 8080 listens on `127.0.0.1` only.

### 4. Verify

From the Mac:

```bash
curl -fsS https://relay.<domain>/v1/healthz                       # {"ok":true}
curl -s -o /dev/null -w '%{http_code}\n' https://relay.<domain>/v1/agents   # 401 without a token
curl -fsS -H "Authorization: Bearer $AW_HOST_TOKEN" https://relay.<domain>/v1/host/status
```

Then point the bridge at it and start the service:

```bash
./bin/agent-watch-bridge configure --relay-url wss://relay.<domain> --host-token "$AW_HOST_TOKEN"
herdr plugin action invoke --plugin herdr-agent-watch start
herdr plugin action invoke --plugin herdr-agent-watch status   # relay_connected: true, host_online: true
```

**SSE through Cloudflare**, after pairing a device (Phase 4):

```bash
curl -N -H "Authorization: Bearer <device_token>" https://relay.<domain>/v1/events
```

A `snapshot` must arrive immediately, then `:` keepalives every 15 s, for at least 3 minutes without disconnecting.

---

## Definition of done

- [ ] `deploy/relay/` contains the unit, `env.example`, the proxy example(s), `deploy.sh` (executable) and the optional Dockerfile. **No real secrets.**
- [ ] The relay runs on the VPS under systemd, and survives `systemctl restart` and a reboot.
- [ ] All the Verify commands pass; SSE stays open for more than 3 minutes through Cloudflare.
- [ ] The bridge on the Mac shows `relay_connected: true`.
- [ ] `docs/STATUS.md` Phase 3c ticked with the public URL, which is fine to record, but **not** the tokens.

---

## Pitfalls

- **Cloudflare "Flexible" SSL** sends plain HTTP to the origin. Use Full (strict).
- **Proxy buffering** makes SSE arrive in bursts or never. Use `flush_interval -1` in Caddy, `proxy_buffering off` in nginx.
- **Idle timeouts:** Cloudflare closes idle connections after about 100 s. The relay's 15 s SSE keepalive and the bridge's 30 s WebSocket ping prevent that. Do not lengthen them.
- **Secrets in shell history:** prefer editing `/etc/agent-watch-relay/env` with an editor over `echo TOKEN >> env`.

---

## Prompt for the executing agent

```
You are executing Phase 3c (relay deployment) of the Agent Watch refactor in /Users/me/Code/personal/agent-watch-herdr.
Read docs/phases/3c-relay-deploy.md. Create the files under deploy/relay/ exactly as specified, with placeholders and no
real secrets. Do NOT run anything against the VPS or Cloudflare yourself: prepare the exact commands and hand them to the
owner, who runs the steps that need credentials. After the owner confirms deployment, run the Verify commands that need
no secrets, report the results, tick Phase 3c in docs/STATUS.md, and commit only deploy/relay and docs/STATUS.md.
```
