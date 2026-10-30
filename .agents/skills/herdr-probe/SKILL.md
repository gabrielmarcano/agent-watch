---
name: herdr-probe
description: "Verify a herdr-core (hc) socket/CLI fact (method params, response shape, event payload, error code, key name) against the live herdr-core with read-only calls, before relying on it in code or docs. Use whenever docs/reference/herdr-socket-api.md does not cover something, seems outdated, or herdr-core was upgraded."
---

# Probe herdr-core (`hc`) safely (read-only)

**Allowed without asking:** the read-only methods and commands of `.agents/rules/herdr-integration.md` § Safety.

**Anything that types, starts, kills, closes or modifies state** goes through an isolated `HC_SESSION=aw-sandbox` or a terminal started with `hc run --bg --label aw=sandbox` (`capture-fixture` skill). The guards block mutating calls on the owner's terminals (`tools/guards/README.md`).

## 1. The OpenRPC schema is the fastest source of truth

```bash
hc --version                                      # compare with herdr-socket-api.md's header
hc schema > /tmp/hc-schema.json
python3 - <<'EOF'
import json
doc = json.load(open('/tmp/hc-schema.json'))
for m in doc.get('methods', []):
    params = [f"{p['name']}{'' if p.get('required') else '?'}" for p in m.get('params', [])]
    print(f"{m['name']}({', '.join(params)})")
print("Schemas:", sorted(doc.get('components', {}).get('schemas', {}).keys()))
EOF
```

## 2. Call one read-only method on the socket

`herdr-core` speaks newline-delimited JSON-RPC 2.0 on `$HC_TERMINAL_SOCKET` (default `~/.local/state/herdr-core/default/default.sock`):

```bash
python3 - <<'EOF'
import json, os, socket
def call(method, params=None):
    sock = os.environ.get('HC_TERMINAL_SOCKET') or os.path.expanduser('~/.local/state/herdr-core/default/default.sock')
    c = socket.socket(socket.AF_UNIX)
    c.connect(sock)
    req = {"jsonrpc": "2.0", "id": "probe", "method": method, "params": params or {}}
    c.sendall((json.dumps(req) + "\n").encode())
    return c.makefile().readline()
print(call("hello", {}))
print(call("server.status", {}))
print(call("terminal.list", {})[:600])
EOF
```

From the CLI, also read-only: `hc ls --json`, `hc get <addr> --json`, `hc read <addr> --lines 50`.

## 3. Watch events for a few seconds

`events.subscribe` returns a snapshot (`{seq, terminals}`) as its RPC result and then streams `{"jsonrpc":"2.0","method":"event","params":{...}}` notifications (plus `heartbeat` every 15 s):

```bash
python3 - <<'EOF'
import json, os, socket
sock = os.environ.get('HC_TERMINAL_SOCKET') or os.path.expanduser('~/.local/state/herdr-core/default/default.sock')
c = socket.socket(socket.AF_UNIX); c.connect(sock); c.settimeout(20)
c.sendall((json.dumps({"jsonrpc": "2.0", "id": "sub", "method": "events.subscribe", "params": {}}) + "\n").encode())
f = c.makefile()
try:
    for _ in range(5): print(f.readline().strip()[:300])
except Exception as e: print("done:", e)
EOF
```

## 4. Record what you learned

- If the fact is new or differs from `docs/reference/herdr-socket-api.md`, update that file (and `pkg/herdrtest` if the fake must mimic it) **in the same commit** as the code that relies on it.
- Mention the `hc --version` you probed.
