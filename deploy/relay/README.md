# Relay operations

Setup and deployment are in [`docs/phases/3c-relay-deploy.md`](../../docs/phases/3c-relay-deploy.md). This page covers day-to-day operations on the VPS.

## Deploy a new build

```bash
deploy/relay/deploy.sh root@<vps>
SSH_OPTS="-i ~/.ssh/relay_ed25519 -o Port=2222" deploy/relay/deploy.sh root@<vps>
```

- **Clean tree only.** It refuses to run with uncommitted or untracked changes, so the binary always matches a commit.
- **Version stamp:** `<Makefile VERSION>-<short sha>`, e.g. `0.2.0-d5b8e33`. Check it with `agent-watch-relay version`.
- **`SSH_OPTS`** goes to both `ssh` and `scp`. Use `-o Port=…`, not `-p`: `scp` spells the port `-P`.
- **Rollback:** the replaced binary is kept as `/usr/local/bin/agent-watch-relay.prev`. After the restart the box curls `/v1/healthz` on the address in `AW_LISTEN` (from `/etc/agent-watch-relay/env`) for up to 15 s. If it never answers, the script prints the last journal lines, restores `.prev`, restarts, and exits non-zero.
- **Needs on the box:** root over SSH (it writes `/usr/local/bin` and runs `systemctl`) and `curl`.

## Client IP and the pairing rate limit

`POST /v1/pair` is limited per client IP. The relay reads forwarding headers only from the proxies in `AW_TRUSTED_PROXIES`; from anyone else it uses the TCP peer address.

| Topology | `AW_TRUSTED_PROXIES` | `AW_CLIENT_IP_HEADER` |
|---|---|---|
| nginx or Caddy on the same host | `127.0.0.1/32` | unset |
| Nginx Proxy Manager in docker (below) | the NPM network's subnet, or `172.16.0.0/12` | unset |
| Behind Cloudflare, origin reachable from Cloudflare **only** | the local proxy, as above | `CF-Connecting-IP` |
| Relay exposed directly (no proxy) | unset | unset |

- **`AW_TRUST_CF_IP` is gone.** If it is still in the env file, the relay ignores it and logs a warning at startup. Delete the line.
- **Never set `AW_CLIENT_IP_HEADER=CF-Connecting-IP` on a domain that is not proxied by Cloudflare.** Anyone can send that header, and the proxy passes it through.
- **Every container on a trusted docker network can claim any client IP.** Trust only networks whose containers you control; the narrowest subnet is best.

## Nginx Proxy Manager in docker

The live layout: NPM runs in docker on a custom network, terminates TLS, and proxies `relay.<domain>` to the relay running under systemd on the host.

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

## systemd unit hardening

`agent-watch-relay.service` runs as `agentwatch` with no capabilities, a read-only system (`ProtectSystem=strict`), `UMask=0077`, only `AF_INET`, `AF_INET6` and `AF_UNIX` sockets (`AF_UNIX` is required for `admin.sock`), and the `@system-service` syscall set. Only `/var/lib/agent-watch-relay` is writable.

- **Custom `AW_DATA_DIR`:** add it to `ReadWritePaths=` in a drop-in, or the relay cannot write its store.
- **After changing the unit:** `sudo systemctl daemon-reload && sudo systemctl restart agent-watch-relay`, then `systemd-analyze security agent-watch-relay` for the exposure score.

## Devices: list and revoke

`devices list` and `devices revoke` work **with the relay running**. There is no need to stop the service.

```bash
sudo agent-watch-relay devices list
sudo agent-watch-relay devices revoke <device_id>
```

- **Relay running:** the CLI talks to it through `/var/lib/agent-watch-relay/admin.sock`, a `0600` Unix socket that is never exposed over TCP.
  - The revoked token gets `401` on its next request.
  - The device's open SSE streams are closed at once.
  - `store.json` is saved immediately.
- **Relay stopped:** the CLI edits `store.json` itself, under the same lock the relay uses.
- **As root or as the service user:** both work. Files written as root keep the `agentwatch` owner, so the service can still read them.
- **Custom data dir:** if `/etc/agent-watch-relay/env` sets a different `AW_DATA_DIR`, pass the same value: `sudo AW_DATA_DIR=<dir> agent-watch-relay devices …`.
- **Docker:** `docker exec <container> /agent-watch-relay devices revoke <device_id>`.

## Lost or stolen watch

1. `sudo agent-watch-relay devices list`, then find the device by name and last-seen time.
2. `sudo agent-watch-relay devices revoke <device_id>`.
3. **Apple Watch (ntfy):** revoking does not stop ntfy notifications, because every watchOS device shares the topic. Put a new random `AW_NTFY_TOPIC` in `/etc/agent-watch-relay/env`, run `sudo systemctl restart agent-watch-relay`, and subscribe the remaining devices to the new topic.

## Files in `/var/lib/agent-watch-relay`

| File | Meaning |
|---|---|
| `store.json` | Devices (token hashes only) and history |
| `relay.lock` | Held by the running relay. A second relay on the same directory refuses to start |
| `admin.sock` | Local admin socket. It exists only while the relay runs |
