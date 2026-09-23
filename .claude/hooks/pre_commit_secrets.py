#!/usr/bin/env python3
"""PreToolUse hook (matcher: Bash): before `git commit`, scan staged changes for secrets.

Blocks (exit 2) when a staged file is a known secret file, or when added lines
contain private keys, service-account JSON, relay/host tokens, or ntfy tokens.
Secrets in git history are permanent, so this errs on the side of blocking.
"""
import json
import os
import re
import subprocess
import sys

ROOT = os.environ.get("CLAUDE_PROJECT_DIR") or os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

SECRET_FILES = re.compile(
    r"(^|/)(google-services\.json|firebase-service-account[^/]*\.json|\.env(\.[^/]*)?|store\.json)$"
)
ALLOWED_FILES = re.compile(r"(\.example|\.template|\.sample)$")
# Fixtures under pkg/agents/testdata ARE scanned: they are captured from real screens.
SKIP_DIRS = (".claude/hooks/",)

PATTERNS = [
    (re.compile(r"-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----"), "private key"),
    (re.compile(r'"private_key"\s*:\s*"-----BEGIN'), "service-account private key"),
    (re.compile(r'"type"\s*:\s*"service_account"'), "service-account JSON"),
    (re.compile(r"AW_HOST_TOKEN\s*=\s*['\"]?[0-9a-f]{64}"), "relay host token"),
    (re.compile(r"host_token\s*=\s*['\"][0-9a-f]{64}['\"]"), "bridge host token"),
    (re.compile(r"Bearer\s+[0-9a-f]{64}\b"), "bearer token"),
    (re.compile(r"\btk_[a-z0-9]{29}\b"), "ntfy access token"),
    (re.compile(r"AIza[0-9A-Za-z_\-]{35}"), "Google API key"),
    (re.compile(r"\bghp_[A-Za-z0-9]{36}\b|\bgithub_pat_[A-Za-z0-9_]{50,}"), "GitHub token"),
]


def main():
    try:
        data = json.load(sys.stdin)
    except Exception:
        sys.exit(0)
    command = (data.get("tool_input") or {}).get("command") or ""
    if not re.search(r"(^|[\s;&|])git\s+(-C\s+\S+\s+)?commit\b", command):
        sys.exit(0)

    staged = subprocess.run(
        ["git", "-C", ROOT, "diff", "--cached", "--name-only", "--diff-filter=ACM"],
        capture_output=True, text=True,
    ).stdout.split()
    findings = []
    for path in staged:
        if SECRET_FILES.search(path) and not ALLOWED_FILES.search(path):
            findings.append(f"{path}: secret file must never be committed")
            continue
        if path.startswith(SKIP_DIRS) or path.endswith((".png", ".jpg", ".jar", ".ico")):
            continue
        diff = subprocess.run(
            ["git", "-C", ROOT, "diff", "--cached", "-U0", "--", path], capture_output=True, text=True
        ).stdout
        added = "\n".join(l[1:] for l in diff.splitlines() if l.startswith("+") and not l.startswith("+++"))
        for rx, what in PATTERNS:
            if rx.search(added):
                findings.append(f"{path}: looks like a {what}")

    if findings:
        print("BLOCKED: possible secrets in the staged changes:\n  " + "\n  ".join(findings)
              + "\nUnstage them (`git restore --staged <file>`), move the value to the relay env file / "
                "bridge config.toml / your local google-services.json, and commit again. If this is a "
                "false positive (e.g. an obviously fake example), change the example so it does not match.",
              file=sys.stderr)
        sys.exit(2)
    sys.exit(0)


if __name__ == "__main__":
    main()
