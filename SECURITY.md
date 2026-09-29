# Security Policy

Agent Watch can press keys in your terminal agents, so security reports matter here.

## Reporting a Vulnerability

**Please do not open a public issue.** Report it privately through GitHub:
[**Report a vulnerability**](https://github.com/gabrielmarcano/agent-watch/security/advisories/new) (Security → Advisories → Report a vulnerability).

Please include:
- the component (bridge, relay, Wear OS app, menu bar app, watchOS app);
- the version (`agent-watch-bridge version`, `agent-watch-relay version`, or the watch's Settings);
- steps to reproduce, and what an attacker could do with it.

This is a one-person project: I aim to acknowledge a report within a week and will keep you updated while it is fixed. Credit is given in the advisory unless you prefer otherwise.

## Supported Versions

Only the latest [release](https://github.com/gabrielmarcano/agent-watch/releases) and `main` get security fixes.

## Scope

In scope, for example:
- anything that lets a device, a watch or a network attacker send input to an agent without a valid, paired device token;
- a way around the bridge's checks before it presses keys (sequence number, prompt fingerprint, one answer per prompt);
- leaks of tokens, prompts or transcript text through the relay, the logs or the watch.

Out of scope:
- a compromised computer or VPS that runs the bridge or the relay;
- problems in herdr or in the agents themselves (report those upstream);
- the watchOS app while it is the legacy pre-relay client ([status](docs/STATUS.md)).

How Agent Watch is meant to be secure: [the security model](docs/GUIDE.md#security-model).
