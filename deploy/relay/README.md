# Relay operations

Setup and deployment are in [`docs/phases/3c-relay-deploy.md`](../../docs/phases/3c-relay-deploy.md). This page covers day-to-day operations on the VPS.

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
