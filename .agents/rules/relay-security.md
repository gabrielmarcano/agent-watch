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
- **Pairing codes:** 6 digits from `crypto/rand`, 5-minute TTL, single use, rate-limited per IP (`CF-Connecting-IP`) and globally.
- **Never log** tokens, headers, request bodies, prompt text or history content.

## Behaviour
- **Exactly one host connection;** a new one replaces the old (close code 4000).
- **Commands time out after 10 s** (`timeout`). With no host, they fail immediately (`host_offline`).
- **HTTP server:** no `WriteTimeout` (it kills SSE and WebSocket). Keep `ReadHeaderTimeout: 10s`.
- **SSE:**
  - subscribe **before** taking the snapshot;
  - keepalive `:` every 15 s;
  - drop slow subscribers instead of blocking.
- **Store:** atomic writes (temp file + fsync + rename, mode 0600). A corrupt file stops startup; never overwrite it silently.

## Push
- **FCM data values are all strings,** and every key is always present.
- **Anti-spam:** debounce 5 s per pane+event; digest when more than 3 in 10 s. A push failure never affects relay state.
- **APNs is out of scope.** watchOS gets alerts through ntfy.

## Deploy
- **Real secrets never enter the repo:** `/etc/agent-watch-relay/env`, the Firebase JSON, the ntfy topic and tokens. Only `*.example` files are committed.
- **Commands that need VPS or Cloudflare credentials are handed to the owner,** not run by agents.
