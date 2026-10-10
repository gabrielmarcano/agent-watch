# Relay operations

Everything about running the relay on a VPS: first-time setup, deploys, rollback, the proxy, hosts, devices and backups. The relay's environment variables and data files are defined in [`docs/reference/contracts.md`](../../docs/reference/contracts.md) §5. Who may run these commands: [`AGENTS.md`](../../AGENTS.md) §3.

## First-time setup (once per VPS)

**Needs:** an x86-64 (amd64) Linux VPS (`make deploy-relay` builds linux/amd64 only; on arm64, install the release's `linux_arm64` binary by hand as `/usr/local/bin/agent-watch-relay`), root over SSH, and `curl` on the box.

```bash
# On the Mac, from the repo root:
scp deploy/relay/env.example deploy/relay/agent-watch-relay.service <vps>:/tmp/

# On the VPS, as root:
useradd --system --home /var/lib/agent-watch-relay --shell /usr/sbin/nologin agentwatch
install -d -m 0750 -o root -g agentwatch /etc/agent-watch-relay   # the relay (agentwatch) must read the Firebase JSON kept here
install -m 0640 -o root -g agentwatch /tmp/env.example /etc/agent-watch-relay/env
install -m 0644 /tmp/agent-watch-relay.service /etc/systemd/system/
systemctl daemon-reload && systemctl enable agent-watch-relay
```

- **The env file:** set `AW_LISTEN` and `AW_TRUSTED_PROXIES` for your proxy (below).
  - **With `agent-watch.env` on the Mac:** leave the `AW_HOST_TOKEN` placeholder; `--sync-env` writes the real token.
  - **Without it:** `AW_HOST_TOKEN=$(openssl rand -hex 32)`, and give the same value to the bridge's `configure`.
  - **Its host:** `AW_HOST_ID` (default `main`) and `AW_HOST_NAME` (what the watch shows, e.g. `Mac`). Every other machine gets its own token: see Hosts, below.
- **Push:** copy the Firebase service-account JSON to `/etc/agent-watch-relay/` yourself (`install -m 0640 -o root -g agentwatch …`) and put its server path in `AW_FCM_CREDENTIALS`. It never goes through `agent-watch.env`.
- **Listen address:** the address your proxy reaches, never a public interface: `127.0.0.1:8080` for nginx or Caddy on the host, the docker bridge for Nginx Proxy Manager in docker (below).
- **TLS in front:** any reverse proxy that terminates TLS and passes WebSockets and unbuffered SSE: `nginx.conf.example`, `Caddyfile.example`, or Nginx Proxy Manager (below).
- **Then, from the Mac:** `make deploy-relay ARGS=--sync-env` installs the binary and starts the service.

### Behind Cloudflare (optional)

In the dashboard:
- the DNS record for `relay` **Proxied**;
- SSL/TLS **Full (strict)**, with an Origin Certificate installed in your proxy (Flexible sends plain HTTP to the origin);
- Network → WebSockets **on**;
- a Cache Rule **Bypass cache** for `relay.<domain>/*`: SSE and API responses must never be cached;
- the client IP header: see Client IP, below.

### Proxy idle timeouts

Proxies close connections that stay idle: Cloudflare after about 100 s, `nginx.conf.example` after 90 s. The relay's SSE keepalives and WebSocket pings (contracts §2.3, §3) are shorter than both: never lengthen them, and never set a proxy timeout below them.

## Deploy a new build

Run **one** of these:

```bash
make deploy-relay                     # target and options from agent-watch.env (AW_RELAY_SSH, AW_RELAY_SSH_OPTS)
make deploy-relay ARGS=--sync-env     # the same, and update /etc/agent-watch-relay/env from agent-watch.env

deploy/relay/deploy.sh root@<vps>     # without agent-watch.env
SSH_OPTS="-i ~/.ssh/<key> -o Port=2222" deploy/relay/deploy.sh root@<vps>
```

- **Clean tree only.** It refuses to run with uncommitted or untracked changes, so the binary always matches a commit. (`agent-watch.env` is git-ignored and does not count.)
- **Version:** bump `RELAY_VERSION` per [`VERSIONS`](../../VERSIONS); the binary adds its commit on its own (format: contracts §3). Check it with `agent-watch-relay version`; the connected bridge shows it too (`relay_version` in `agent-watch-bridge status --json --local`, the menu bar's Versions section).
- **Target:** an explicit `<ssh-target>` wins and uses `SSH_OPTS` only. Without one, `deploy.sh` reads `AW_RELAY_SSH` and `AW_RELAY_SSH_OPTS` from `agent-watch.env` (`AW_ENV_FILE=<path>` for another file); `SSH_OPTS`, if set, still overrides the file's options.
- **`SSH_OPTS` / `AW_RELAY_SSH_OPTS`** go to both `ssh` and `scp`. Use `-o Port=…`, not `-p`: `scp` spells the port `-P`.
- **Automatic rollback:** the replaced binary is kept as `/usr/local/bin/agent-watch-relay.prev`. After the restart the box curls `/v1/healthz` on the address in `AW_LISTEN` (from `/etc/agent-watch-relay/env`) for up to 15 s. If it never answers, the script prints the last journal lines, restores `.prev` (and the env file, after `--sync-env`), restarts, and exits non-zero.
- **Needs on the box:** see First-time setup. The script writes `/usr/local/bin` and `/etc/agent-watch-relay`, and runs `systemctl`.

### Rollback by hand

To go back after a deploy that passed its health check, restore the kept binary:

```bash
ssh <vps> 'install -m 0755 /usr/local/bin/agent-watch-relay.prev /usr/local/bin/agent-watch-relay && systemctl restart agent-watch-relay'
```

Only one previous binary is kept: each deploy overwrites `.prev`.

**Across relay 0.7.0** (`store.json` version 2, with hosts): an older relay still starts on the new file, but needs `AW_HOST_TOKEN`, finds no pane's history (its keys now name a host), and drops the registered hosts on its next save. After rolling forward again, add them anew (new tokens).

### `--sync-env`: the server's env file from `agent-watch.env`

Opt-in. It never runs on a plain deploy, and it changes live secrets.

- **What is synced:** the relay keys (`docs/reference/contracts.md` §7, read by `deploy.sh --sync-env`) that `agent-watch.env` sets on an uncommented line, even an empty one. A commented-out key leaves the server's line alone.
- **One server variable by hand** (e.g. `AW_PUSH_RESOLVED=1`): edit `/etc/agent-watch-relay/env` on the VPS as root, keeping its owner and mode, then `systemctl restart agent-watch-relay`. A later `--sync-env` overwrites that line if `agent-watch.env` sets the same key, so the owner should set it there too. `AW_DATA_DIR` is never synced (a new one also needs a `ReadWritePaths=` drop-in).
- **Checked on the Mac, before the build:** `AW_HOST_TOKEN` must be 64 hex characters (an empty token is never sent), and values with a quote, a backslash or `$` are refused (systemd would not read them literally).
- **On the server:** each key replaces its line in `/etc/agent-watch-relay/env` in place; other keys, comments and blank lines stay, and missing keys are appended at the end. The file keeps its owner and mode (`root:agentwatch`, `0640`). The server file must exist already (see First-time setup).
- **Backup:** when anything changed, the previous file is kept as `/etc/agent-watch-relay/env.bak-<UTC time>`. Backups hold the old secrets with the same mode; delete old ones by hand.
- **Secrets:** the values travel in a `0600` temp file over `scp` and are deleted after the run. Nothing is printed but key names: `env: changed: …; added: …; unchanged: …`.
- **Rollback:** if the relay is not healthy after the restart, both the binary and the env file are rolled back.

## Client IP and the pairing rate limit

`POST /v1/pair` is limited per client IP. How the relay picks the client IP from `AW_TRUSTED_PROXIES` and the forwarding headers: contracts §5. What to set per topology:

| Topology | `AW_TRUSTED_PROXIES` | `AW_CLIENT_IP_HEADER` |
|---|---|---|
| nginx or Caddy on the same host | `127.0.0.1/32` | unset |
| Nginx Proxy Manager in docker (below) | the NPM network's subnet, or `172.16.0.0/12` | unset |
| Behind Cloudflare, origin reachable from Cloudflare **only** | the local proxy, as above | `CF-Connecting-IP` |
| Relay exposed directly (no proxy) | unset | unset |

- **Never set `AW_CLIENT_IP_HEADER=CF-Connecting-IP` on a domain that is not proxied by Cloudflare.** Anyone can send that header, and the proxy passes it through.
- **Every container on a trusted docker network can claim any client IP.** Trust only networks whose containers you control; the narrowest subnet is best.

## Nginx Proxy Manager in docker

A common layout: NPM runs in docker on a custom network, terminates TLS, and proxies `relay.<domain>` to the relay running under systemd on the host.

```
internet ──443──▶ NPM container (custom docker network, 172.x.0.y)
                     │ http://172.17.0.1:8080
                     ▼
              host: agent-watch-relay (systemd), AW_LISTEN=172.17.0.1:8080
```

**1. Listen on the docker bridge address, not on all interfaces.**

`172.17.0.1` is the host's address on `docker0`. Containers reach it, including those on custom networks, but it is not the public interface. `0.0.0.0:8080` would publish the relay without TLS to the internet.

```bash
# /etc/agent-watch-relay/env
AW_LISTEN=172.17.0.1:8080
AW_TRUSTED_PROXIES=172.16.0.0/12     # or the exact NPM network subnet, see below
```

The NPM proxy host forwards to scheme `http`, host `172.17.0.1`, port `8080`, with **Websockets Support** on.

**2. Start the relay after docker.**

`172.17.0.1` exists only once docker has created `docker0`. At boot the relay can start first, and then `listen tcp 172.17.0.1:8080: bind: cannot assign requested address` makes it crash-loop until docker is up. Order it with a drop-in, which survives replacing the main unit file:

```bash
sudo mkdir -p /etc/systemd/system/agent-watch-relay.service.d
printf '[Unit]\nAfter=docker.service\n' | sudo tee /etc/systemd/system/agent-watch-relay.service.d/after-docker.conf
sudo systemctl daemon-reload
systemctl show agent-watch-relay -p After | tr ' ' '\n' | grep docker   # docker.service
```

**3. Trust NPM's network for the client IP.**

NPM connects from its container address on the custom network, and it sets `X-Forwarded-For` and `X-Real-IP`. Find the subnet:

```bash
docker inspect -f '{{range $n, $c := .NetworkSettings.Networks}}{{$n}} {{end}}' <npm-container>
docker network inspect <network> -f '{{range .IPAM.Config}}{{.Subnet}}{{end}}'
```

Use that subnet in `AW_TRUSTED_PROXIES`. `172.16.0.0/12` covers docker's default pools; if the network lives elsewhere (e.g. `192.168.x`), add it.

**4. Do not enable ufw blindly on a docker host.**

- Docker writes its own iptables rules. Ports published by containers (NPM's 80/443) **bypass ufw**, so ufw does not protect them.
- With ufw's default `deny incoming`, traffic from the NPM container to `172.17.0.1:8080` is incoming traffic to the host and gets **dropped**: the relay goes dark behind NPM.
- Enabling ufw without an SSH rule locks you out.

If you do enable it: allow SSH first (`ufw allow OpenSSH`), then allow the relay port from the docker networks only (`ufw allow from 172.16.0.0/12 to any port 8080 proto tcp`), and test from a second SSH session before closing the first.

**5. Check that port 8080 is closed to the outside, from the server itself.**

```bash
ss -ltnp | grep ':8080'                           # must show 172.17.0.1:8080, never 0.0.0.0, * or [::]
curl -fsS http://172.17.0.1:8080/v1/healthz       # {"ok":true}: NPM can reach it
PUBLIC_IP=$(hostname -I | awk '{print $1}')       # check it is the public address: ip -4 addr
curl -sS --max-time 3 http://$PUBLIC_IP:8080/v1/healthz; echo "exit=$?"   # must fail: connection refused
```

- The last command fails because nothing listens on the public address. `ss` is the authoritative check.
- Linux accepts a packet for `172.17.0.1` on any interface, but only a machine on the same private L2 network can send one. If the VPS shares a private network (e.g. a provider VPC) with machines you do not control, drop `172.17.0.1:8080` from non-docker interfaces with a firewall rule.

## systemd unit

The hardening is in `agent-watch-relay.service` itself; only `/var/lib/agent-watch-relay` is writable.

- **Custom `AW_DATA_DIR`:** add it to `ReadWritePaths=` in a drop-in, or the relay cannot write its store.
- **After changing the unit:** `sudo systemctl daemon-reload && sudo systemctl restart agent-watch-relay`, then `systemd-analyze security agent-watch-relay` for the exposure score.

## Devices: list and revoke

`devices list` and `devices revoke` work **with the relay running**. There is no need to stop the service.

```bash
sudo agent-watch-relay devices list               # ID, NAME, CREATED, LAST SEEN
sudo agent-watch-relay devices revoke <device_id>
```

- **Relay running:** the CLI talks to it through `admin.sock` in the data dir (a `0600` Unix socket, never exposed over TCP; its API: contracts §5).
  - The revoked token gets `401` on its next request.
  - The device's open SSE streams and in-flight requests are cancelled at once.
  - `store.json` is saved immediately.
- **Relay stopped:** the CLI edits `store.json` itself, under the same lock the relay uses.
- **As root or as the service user:** both work. Files written as root are handed to the owner of the data dir, so the service can still read them.
- **Custom data dir:** if `/etc/agent-watch-relay/env` sets a different `AW_DATA_DIR`, pass the same value: `sudo AW_DATA_DIR=<dir> agent-watch-relay devices …`.
- **Docker:** `docker exec <container> /agent-watch-relay devices revoke <device_id>`.

## Hosts: one per machine

Each machine that runs a bridge is one host with its own token; the watch shows one page per host. The host of `AW_HOST_TOKEN` (`AW_HOST_ID`, default `main`) is set in the env file; every other machine gets its token here. Like `devices`, these commands work **with the relay running or stopped**.

```bash
sudo agent-watch-relay hosts add <id> --name "<name>"   # prints the new token, once, alone on stdout
sudo agent-watch-relay hosts list                       # ID, NAME, ONLINE, CREATED, LAST SEEN
sudo agent-watch-relay hosts revoke <id>
```

- **`<id>`** is stable (the watch keys agents by it): `[a-z0-9-]{1,32}`. **`--name`** is what the watch shows; default: the id. Rules: contracts §1.6, §5.
- **The token is shown once:** the CLI generates it and the relay keeps only its hash. On that machine, put it in `agent-watch.env` as `AW_HOST_TOKEN` and run `make configure-bridge` ([`docs/GUIDE.md`](../../docs/GUIDE.md)). A lost token cannot be recovered: revoke the host and add it again.
- **Relay running:** a new token works at once, and the watch lists the host, offline until its bridge connects. A revoked host's connection is closed (1008), its token gets `401`, and its agents leave the watch's list; its history stays until it ages out.
- **`AW_HOST_TOKEN`'s host** is listed as `(AW_HOST_TOKEN)` and managed in `/etc/agent-watch-relay/env`, not here. Once another host is registered, `AW_HOST_TOKEN` may be removed; the relay then starts only with a registered host.
- **Custom data dir or `AW_HOST_ID`:** pass the same values as the env file, `sudo AW_DATA_DIR=<dir> AW_HOST_ID=<id> agent-watch-relay hosts …`: a `store.json` from a relay before 0.7.0 is migrated by whichever opens it first, and its history goes to `AW_HOST_ID`.
- **Docker:** `docker exec <container> /agent-watch-relay hosts …`.

## Lost or stolen watch

1. `sudo agent-watch-relay devices list`, then find the device by name and last-seen time.
2. `sudo agent-watch-relay devices revoke <device_id>`.
3. **Apple Watch (ntfy):** revoking does not stop ntfy notifications, because every watchOS device shares the topic. Put a new random `AW_NTFY_TOPIC` in `/etc/agent-watch-relay/env` and run `sudo systemctl restart agent-watch-relay` (or, if `agent-watch.env` manages it, change it there and run `make deploy-relay ARGS=--sync-env`), then subscribe the remaining devices to the new topic.

## Backups

What to keep: `store.json` in the data dir (hosts, devices and history; its contents: contracts §5) and `/etc/agent-watch-relay/env` (the relay's secrets). Both are secrets: keep the copies root-only.

```bash
sudo install -m 0600 /var/lib/agent-watch-relay/store.json /root/agent-watch-store.json.bak-$(date -u +%Y%m%d)
sudo install -m 0600 /etc/agent-watch-relay/env /root/agent-watch-env.bak-$(date -u +%Y%m%d)
```

- **Copying while the relay runs is safe:** it writes `store.json` atomically (temp file and rename), so a copy is always a whole file.
- **Restore:** stop the relay, put the file back with its owner and mode, start it again:

  ```bash
  sudo systemctl stop agent-watch-relay
  sudo install -m 0600 -o agentwatch -g agentwatch <backup> /var/lib/agent-watch-relay/store.json
  sudo systemctl start agent-watch-relay
  ```

  The env file goes back with `install -m 0640 -o root -g agentwatch <backup> /etc/agent-watch-relay/env`, then a restart.
- **Without a backup** the relay starts empty: every watch must pair again, every host but `AW_HOST_TOKEN`'s needs a new token, and the history is gone.
