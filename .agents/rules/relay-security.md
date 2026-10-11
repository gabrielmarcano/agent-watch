---
trigger: model_decision
description: "Security rules for the relay server, push notifications and VPS deployment. Apply when touching pkg/relay, pkg/push, cmd/relay or deploy/"
paths:
  - "pkg/relay/**"
  - "pkg/push/**"
  - "cmd/relay/**"
  - "deploy/**"
---

# Relay and push rules (security is correctness here)

> Applies to: pkg/relay, pkg/push, cmd/relay, deploy/.

The relay can make agents on the owner's Mac type and approve things, so treat it as a remote-execution surface. Shapes and values (timeouts, keepalives, pairing limits, anti-spam windows): `docs/reference/contracts.md` §2–§5. Secrets and who may deploy: `AGENTS.md` §3. Operations: `deploy/relay/README.md`.

## Tokens and auth
- **Only the `Authorization: Bearer` header authenticates;** ignore query-string tokens.
- **Device and host tokens are 32 random bytes.** Store only `sha256` hashes, and compare with `crypto/subtle.ConstantTimeCompare`. `AW_HOST_TOKEN` is compared the same way.
- **Pairing codes** come from `crypto/rand`, and follow the pairing limits in `contracts.md` §2.1 (single use, expiry, rate limits per client IP and global).
- **Client IP:** forwarding headers are read **only** from peers in `AW_TRUSTED_PROXIES`; otherwise the TCP peer is the client. Never trust a header from an untrusted peer (the algorithm: `contracts.md` §5).
- **Never log** request bodies or history content, besides what `AGENTS.md` §3 forbids.

## Behaviour
- **A host is who its token says, never what the bridge says:** the relay stamps the host's id on everything a connection sends, and keys agents, history, commands and pushes by `(host, pane_id)`, never by `pane_id` alone (`contracts.md` §1.2, §3).
- **One connection per host;** a new one with the same host's token replaces the old, and other hosts' connections are untouched (`contracts.md` §3). Revoking a host closes its connection, and nothing it sent before is applied after.
- **Commands** go only to their agent's host and get one budget for sending and waiting, longer than the bridge's and shorter than the watch's, so inner layers always time out first (values: `contracts.md` §2.4). With their host offline they fail immediately (`host_offline`), and in-flight ones fail the moment their host disconnects, misses a ping, is replaced or is revoked. Only the connection a command went to may answer it.
- **Host keepalive:** ping the host; no pong in time → drop it (`contracts.md` §3).
- **Keepalives stay short** (SSE comments, WebSocket pings; `contracts.md` §2–§3): they must stay below the proxies' idle timeouts (`deploy/relay/README.md` § Proxy idle timeouts), so never lengthen them.
- **HTTP server:** no `ReadTimeout` or `WriteTimeout` (they kill SSE and WebSocket); keep `ReadHeaderTimeout` and `IdleTimeout` (`pkg/relay/server.go`). Shutdown cancels every request context, so open streams end at once.
- **SSE:**
  - subscribe **before** taking the snapshot;
  - keepalive comments and a deadline on every write (`contracts.md` §2);
  - drop slow subscribers instead of blocking.
- **Store:** atomic writes (temp file + fsync + rename, mode 0600). A corrupt file stops startup; never overwrite it silently.

## Push
- **FCM data carries exactly what `contracts.md` §4.1 lists per event,** all values as strings.
- **Anti-spam** follows `contracts.md` §4.3 exactly (debounce, window, digest). A quick re-block is **held**, never dropped. A push failure never affects relay state.
- **Dead FCM tokens:** unregister only on the errors `contracts.md` §4.1 names, never on anything broader.
- **`resolved`** goes only to senders that can withdraw a notification (`contracts.md` §4.1–§4.2).
