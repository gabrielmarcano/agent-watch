# Go Relay Architecture Deepening (`pkg/relay`, `pkg/push`, `cmd/relay`)

A temporary guide: delete it when the 4 slices below are done ([`docs/STATUS.md`](../STATUS.md) workflow), after moving any contract change (slice 1's `resolved` on removed/snapshot panes) to [`docs/reference/contracts.md`](../reference/contracts.md) §4.3.

---

## 0. Load-Bearing Invariants (Must Not Change)

Before touching any file in `pkg/relay` or `pkg/push`, keep these two invariants intact:

1. **`State.pub` + `State.mu` causal SSE ordering ([`pkg/relay/state.go`](../../pkg/relay/state.go), [`pkg/relay/sse.go`](../../pkg/relay/sse.go)):**
   - `State.pub` (`sync.Mutex`) serializes state mutations with SSE fanout, while `State.mu` (`sync.RWMutex`) is released *before* JSON marshalling and non-blocking `sub.trySend(ev)`.
   - Do **not** split SSE broadcasting out of `State` or introduce an asynchronous event bus between `State` and SSE subscribers: [`Server.handleEvents`](../../pkg/relay/sse.go) relies on `Subscribe()` followed by `Snapshot()` to guarantee that no state transition is ever missed or reordered relative to the initial snapshot.
2. **Single-file coalesced `store.json` ([`pkg/relay/store.go`](../../pkg/relay/store.go)):**
   - `Store` already throttles `TouchDevice` to 1 minute and coalesces background disk writes with a 1-second timer (`scheduleSaveLocked`), while keeping immediate atomic saves (`saveNowLocked`) for pairing and admin mutations.
   - Do **not** split `store.json` into multiple files: single-file atomic backup/rollback ([`deploy/relay/README.md`](../../deploy/relay/README.md)) and v1→v2 history migration depend on it.

Execute the 4 slices below in order, **one commit per slice**, running the verification suite (§5) after each slice.

---

## Slice 1: Complete the `Notifier` Seam Across `SnapshotMsg` and `AgentRemovedMsg`

### Problem
- [`Hub.handleWireMessage`](../../pkg/relay/hub.go) only calls `h.notifier.OnAgentUpdate` on `WireAgentUpdate`, ignoring `WireSnapshot` (`State.ReplaceAll`) and `WireAgentRemoved` (`State.Remove`).
- When a `blocked` pane is closed (`agent_removed`) or disappears/leaves `blocked` in a reconnect `snapshot`:
  1. `push.Dispatcher` never sends `EventResolved` to withdraw the active approval notification on the watch/phone;
  2. `blockedShown` and `presence.quiet` retain stale entries (which forced [`pkg/push/push.go`](../../pkg/push/push.go) to add the `maxShownBlocked = 1024` leak cap);
  3. [`NewServer`](../../pkg/relay/server.go) had to wire `d.Current = s.state.Get` and `d.HostOnline = s.state.HostOnline` so [`endPresenceLocked`](../../pkg/push/presence.go) could poll `relay.State` when presence ends.

### Design
1. **Extend `relay.Notifier` ([`pkg/relay/hub.go`](../../pkg/relay/hub.go)) and `NoopNotifier`:**
   - Add `OnAgentRemoved(host, paneID string)` (or emit removal/snapshot reconciliation from `Hub.handleWireMessage` when handling `WireSnapshot` and `WireAgentRemoved`).
   - On `WireAgentRemoved`: after `h.state.Remove(hostID, m.PaneID)`, call `h.notifier.OnAgentRemoved(hostID, m.PaneID)`.
   - On `WireSnapshot`: reconcile the host's previous panes against the new snapshot:
     - Note: a `snapshot` itself must **never** trigger brand-new `blocked` or `done` pushes (see [`contracts.md` §4.3](../reference/contracts.md#43-when-to-push-relay): transitions come from `agent_update`).
     - However, for any pane on `hostID` that was previously tracked by `Dispatcher`:
       - If the pane is **missing** from the new snapshot, treat it as removed (`OnAgentRemoved(hostID, paneID)`).
       - If the pane is **present** in the new snapshot, update held/quiet state (`d.latest`, `d.presence.quiet`, `d.pendingDone`), and if it was in `d.blockedShown` and its new status is no longer `model.StatusBlocked`, withdraw its notification with `EventResolved` at the new `StateChangeSeq` (or the shown prompt's `seq` if removed).
2. **`OnAgentRemoved(host, paneID string)` in [`pkg/push/push.go`](../../pkg/push/push.go):**
   - Clean up single-pane state the same way [`OnHostRevoked`](../../pkg/push/presence.go) cleans up a whole host:
     - Remove any held messages for `(host, paneID)` from `d.held` and `d.latest`.
     - If `d.pendingDone[key]` exists, stop its timer and delete it.
     - Delete `d.presence.quiet[key]`.
     - Delete `d.lastPush` entries for `key`.
     - If `shown, ok := d.blockedShown[key]; ok`, delete `d.blockedShown[key]` and dispatch `Message{Event: EventResolved, Host: host, PaneID: paneID, StateChangeSeq: shown.seq}` (same `seq` rule as `OnHostRevoked`, [`contracts.md` §4.1](../reference/contracts.md#41-fcm-http-v1-wear-os-and-android-phone)).
3. **Update [`docs/reference/contracts.md`](../reference/contracts.md) §4.3 ("Removed panes"):**
   - Update the "Removed panes" bullet in §4.3 to state that a pane whose `blocked` push went out and that is removed (`agent_removed` or omitted from a `snapshot`) or whose status in a `snapshot` is no longer `blocked` gets its `resolved` push immediately.

### Tests
- In `pkg/push`:
  - Closing a pane (`OnAgentRemoved`) while its `blocked` push is shown sends `EventResolved` with the shown prompt's `StateChangeSeq` and removes it from `blockedShown`.
  - Closing a pane while its `blocked` push is held in the window or in `presence.quiet` drops it so it never flushes later.
  - A reconnect `snapshot` where a previously `blocked` pane is now `working` or omitted withdraws the shown `blocked` notification and clears `presence.quiet`, without firing new `blocked` or `done` pushes for other panes in the snapshot.
- In `pkg/relay`:
  - End-to-end wire test: bridge sends `agent_update` (`blocked`) → `agent_removed` → `Notifier` receives `OnAgentRemoved`.

---

## Slice 2: Unify Online (`Server`) and Offline (`Store`) Admin Operations

### Problem
- Admin operations (`devices list|revoke`, `hosts add|list|revoke`) are implemented twice in [`pkg/relay/admin.go`](../../pkg/relay/admin.go) and [`cmd/relay/main.go`](../../pkg/relay/main.go): once on `*Server` (used via `admin.sock` when the daemon is running) and once in `Admin*` functions called from `withRelayOrStore` offline closures.
- This duplication caused three concrete behavioral bugs when the relay is stopped:
  1. [`AdminListHosts`](../../pkg/relay/admin.go) calls `st.ListHosts()`, omitting the `AW_HOST_TOKEN` bootstrap host (`Env: true`) that [`Server.hostInfos`](../../pkg/relay/admin.go) includes when online.
  2. [`AdminRevokeHost`](../../pkg/relay/admin.go) returns `ErrHostNotFound` (`404`) when revoking the `AW_HOST_TOKEN` host offline, whereas [`Server.RevokeHost`](../../pkg/relay/admin.go) returns `ErrHostIsEnv` (`409`).
  3. [`AdminAddHost`](../../pkg/relay/admin.go) requires [`cmd/relay/main.go`](../../pkg/relay/main.go) to parse `/etc/agent-watch-relay/env` and pass `envHostID` as a 5th parameter only for the offline path.

### Design
1. **Unify host/device admin logic in [`pkg/relay/admin.go`](../../pkg/relay/admin.go):**
   - Represent the optional bootstrap env host (from `AW_HOST_TOKEN`, `AW_HOST_ID`, `AW_HOST_NAME`) with the same validation/shadowing rule that [`NewServer`](../../pkg/relay/server.go) uses (if a stored host already holds the same token hash as `AW_HOST_TOKEN`, `AW_HOST_TOKEN` is ignored).
   - Provide a single set of operations over `(*Store, envHost *HostInfo)` (with optional live callbacks when attached to a running `*Server` for `hub.RevokeHost`, `state.RemoveHost`, `state.InitHost`, `state.HostOnline`, `hub.ConnectedHostCount`):
     - `ListDevices() []DeviceInfo`
     - `RevokeDevice(id string) error`
     - `ListHosts() []HostInfo` (includes the env host with `Env: true`, sorted by ID, both online and offline)
     - `AddHost(id, name, tokenHash string) (HostInfo, error)` (checks `envHost` collision in one place)
     - `RevokeHost(id string) error` (checks `envHost` first and returns `ErrHostIsEnv` both online and offline)
2. **Simplify [`cmd/relay/main.go`](../../pkg/relay/main.go):**
   - Have `withRelayOrStore` construct the offline admin target with the loaded env host configuration once, so `runDevices` and `runHosts` call the exact same admin methods whether routed through `admin.sock` or directly against the locked store.

### Tests
- In `pkg/relay/multihost_test.go` (or `admin_test.go`):
  - Verify online/offline parity for `AdminListHosts`, `AdminAddHost`, and `AdminRevokeHost` when `AW_HOST_TOKEN` is set:
    - Offline `AdminListHosts` includes the env host (`Env: true`, `Online: false`).
    - Offline `AdminRevokeHost(envHostID)` returns `ErrHostIsEnv` (and exits non-zero with the same message as online).
    - Offline `AdminAddHost` with `envHostID` returns `ErrHostExists`.

---

## Slice 3: Deepen `Hub.Command` Dispatch and Unify Error Semantics

### Problem
- [`Server.runCommand`](../../pkg/relay/api.go) and [`Server.runHostCommand`](../../pkg/relay/api.go) both manually query `State.HostOnline` and `State.HerdrOnline` before calling [`Hub.Command`](../../pkg/relay/hub.go), which immediately acquires `h.mu` and re-checks `h.conns[hostID]`.
- `Hub.Command` returns `(model.CommandResultMsg, error)` using two conflicting error conventions:
  - When the bridge is offline, disconnects mid-flight, or times out, it returns `err == nil` with a synthesized `model.CommandResultMsg{OK: false, Error: ...}`.
  - When `ctx.Err() != nil` ([`pkg/relay/hub.go:599`](../../pkg/relay/hub.go#L599)), it returns a non-nil Go `error`, which [`api.go:323`](../../pkg/relay/api.go#L323) maps to HTTP `500 internal_error` (`model.ErrInternal`) instead of a clean timeout/offline status.

### Design
1. **Single command readiness + execution seam on `Hub` ([`pkg/relay/hub.go`](../../pkg/relay/hub.go)):**
   - Have `Hub.Command` check host connection and `state.HerdrOnline(hostID)` in one place and always return command failures through a single consistent mechanism (either always a populated `model.CommandResultMsg{OK: false, Error: code, Message: msg}` with no second error channel, or a typed domain error that `writeCommandFailure` maps directly to HTTP status codes).
   - Ensure `ctx.Err()` while awaiting a bridge response maps cleanly to `model.ErrBridgeTimeout` (`504`) instead of `500 internal_error`.
2. **Deduplicate [`runCommand`](../../pkg/relay/api.go) and [`runHostCommand`](../../pkg/relay/api.go):**
   - Keep pane target resolution (`resolveCommandHost`) for pane-scoped commands and host existence check for host-scoped commands (`create_session`), then delegate the rest to a single shared helper over `Hub.Command`.

### Tests
- In `pkg/relay/hub_test.go` and `pkg/relay/api_test.go`:
  - All existing command status codes (`404 pane_not_found`, `404 host_not_found`, `409 host_required`, `503 host_offline` / `bridge_offline`, `503 herdr_offline`, `504 bridge_timeout`) remain 100% unchanged (`contracts.md` §2.4).
  - Verify that a canceled/expired command context produces `504 bridge_timeout` rather than `500 internal_error`.

---

## Slice 4: Seal Host Revocation and Handshake Verification into One Atomic Gate

### Problem
- Preventing a racing host WebSocket handshake from surviving concurrent host revocation currently relies on an implicit 3-file ordering contract across [`pkg/relay/admin.go`](../../pkg/relay/admin.go), [`pkg/relay/hub.go`](../../pkg/relay/hub.go), and [`pkg/relay/auth.go`](../../pkg/relay/auth.go):
  1. `Server.RevokeHost` must call `s.store.RevokeHost(id)` *before* `s.hub.RevokeHost(id)`;
  2. `Hub.ServeHost` must call `h.auth.VerifyHostToken(token)` before `websocket.Accept` and then call `h.auth.VerifyHostToken(token)` a **second time** inside `h.mu.Lock()` before registering `h.conns[host.ID]`.

### Design
1. **Encapsulate host lifecycle in `Hub` ([`pkg/relay/hub.go`](../../pkg/relay/hub.go)):**
   - Move the full online host revocation sequence (`store.RevokeHost` → retire active connection under `h.mu` → `notifier.OnHostRevoked` → `state.RemoveHost`) behind a single coordinated method (or have `Hub.RevokeHost` own the store+connection+notifier+state transition under its gate) so callers cannot invoke them out of order.
   - Keep the double-check under `h.mu` during `ServeHost` adjacent to the revocation method in the same file (`hub.go`), making the race invariant local to one module.

### Tests
- Verify existing concurrent handshake vs. `RevokeHost` tests in `pkg/relay/multihost_test.go` continue to pass under `go test -race ./...`.

---

## 5. Verification Before Claiming Done

After each slice (and at the end):

```bash
gofmt -l cmd pkg deploy   # must be empty
go vet ./...
go test -race ./...
CGO_ENABLED=0 go build ./...
```

Do **not** run `make build` (it overwrites `bin/agent-watch-bridge`, which the owner's LaunchAgent runs).
