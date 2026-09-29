---
name: herdr-probe
description: "Verify a herdr socket/CLI fact (method params, response shape, event payload, error code, key name) against the live herdr with read-only calls, before relying on it in code or docs. Use whenever docs/reference/herdr-socket-api.md does not cover something, seems outdated, or herdr was upgraded."
---

# Probe herdr safely (read-only)

**Allowed without asking:** the read-only methods and commands of `.agents/rules/herdr-integration.md` § Safety.

**Anything that types, starts, closes or renames** goes through the `capture-fixture` sandbox. The guards block most of it (`tools/guards/README.md`), but not ordinary tab or workspace renames: those are on you.

## 1. The schema is the fastest source of truth

```bash
herdr --version                                   # compare with herdr-socket-api.md's header
herdr api schema --json > /tmp/herdr-schema.json
python3 - <<'EOF'
import json
s = json.load(open('/tmp/herdr-schema.json'))['schemas']
rq = s['request']['$defs']
print(sorted(k for k in rq if k.endswith('Params')))        # every params type
print(json.dumps(rq.get('AgentReadParams'), indent=1))      # one of them in detail
print(sorted(s['subscription_event']['$defs']))             # event payload types
EOF
```

## 2. Call one read-only method

```bash
python3 - <<'EOF'
import json, os, socket
def call(method, params):
    c = socket.socket(socket.AF_UNIX)
    c.connect(os.environ.get('HERDR_SOCKET_PATH') or os.path.expanduser('~/.config/herdr/herdr.sock'))
    c.sendall((json.dumps({"id": "probe", "method": method, "params": params}) + "\n").encode())
    return c.makefile().readline()          # one request per connection
print(call("ping", {}))
print(call("agent.list", {})[:600])
EOF
```

From the CLI, also read-only: `herdr agent list`, `herdr agent read <pane_id> --source visible --format text`.

## 3. Watch events for a few seconds

```bash
python3 - <<'EOF'
import json, os, socket
c = socket.socket(socket.AF_UNIX); c.connect(os.environ.get('HERDR_SOCKET_PATH') or os.path.expanduser('~/.config/herdr/herdr.sock')); c.settimeout(20)
c.sendall((json.dumps({"id": "sub", "method": "events.subscribe", "params": {"subscriptions": [
    {"type": "pane.created"}, {"type": "pane.focused"}]}}) + "\n").encode())
f = c.makefile()
try:
    for _ in range(10): print(f.readline().strip())
except Exception as e: print("done:", e)
EOF
```

## 4. Record what you learned

- If the fact is new or differs from `docs/reference/herdr-socket-api.md`, update that file (and `pkg/herdrtest` if the fake must mimic it) **in the same commit** as the code that relies on it.
- Mention the herdr version you probed.
