---
trigger: model_decision
description: "Security rules for the relay server, push notifications and VPS deployment. Apply when touching pkg/relay, pkg/push, cmd/relay or deploy/"
---

# Relay and push rules (security is correctness here)

> Applies to: pkg/relay, pkg/push, cmd/relay, deploy/.

The relay can make agents on the owner's Mac type and approve things, so treat it as a remote-execution surface. Spec: `docs/reference/contracts.md` §2–§5, guides `docs/phases/3a…3c`.

## Tokens and auth
- **Tokens travel only in the `Authorization: Bearer` header.** Ignore query-string tokens.
- **Device tokens are 32 random bytes.** Store only `sha256` hashes, and compare with `crypto/subtle.ConstantTimeCompare`. The host token is compared the same way.
- **Pairing codes:** 6 digits from `crypto/rand`, 5-minute TTL, single use, rate-limited per client IP and globally.
- **Client IP:** forwarding headers (`X-Forwarded-For`, `X-Real-IP`, the optional `AW_CLIENT_IP_HEADER`) are read **only** from peers in `AW_TRUSTED_PROXIES`; otherwise the TCP peer is the client. Never trust a header from an untrusted peer (`contracts.md` §5).
- **Never log** tokens, headers, request bodies, prompt text or history content.

## Behaviour
- **Exactly one host connection;** a new one replaces the old (close code 4000).
- **Commands** get one 7 s budget for sending and waiting (`timeout`), between the bridge's 6 s and the watch's 8 s: inner layers always time out first. With no host they fail immediately (`host_offline`), and in-flight ones fail the moment their host disconnects, misses a ping or is replaced.
- **Host keepalive:** ping the host every 30 s; no pong within 10 s → drop it.
- **HTTP server:** no `ReadTimeout` or `WriteTimeout` (they kill SSE and WebSocket). Keep `ReadHeaderTimeout: 10s` and `IdleTimeout: 120s`. Shutdown cancels every request context, so open streams end at once.
- **SSE:**
  - subscribe **before** taking the snapshot;
  - keepalive `:` every 15 s;
  - a 10 s deadline on every write;
  - drop slow subscribers instead of blocking.
- **Store:** atomic writes (temp file + fsync + rename, mode 0600). A corrupt file stops startup; never overwrite it silently.

## Push
- **FCM data values are all strings,** and every key is always present.
- **Anti-spam** (`contracts.md` §4.3): debounce 5 s per pane+event (a repeated `done` is dropped, a quick re-block is **held** to the end of the window, never dropped); the first push of a 10 s window goes out at once; more than 3 in the window → one digest. A push failure never affects relay state.
- **Dead FCM tokens:** unregister only on `UNREGISTERED` or a `message.token` field violation. A bare 404 is not a dead token.
- **`resolved`** goes only to senders that can withdraw a notification (FCM with `AW_PUSH_RESOLVED`), never to ntfy.
- **APNs is out of scope.** watchOS gets alerts through ntfy.

## Deploy
- **Real secrets never enter the repo:** `/etc/agent-watch-relay/env`, the Firebase JSON, the ntfy topic and tokens. Only `*.example` files are committed.
- **Commands that need VPS or Cloudflare credentials are handed to the owner,** not run by agents.
