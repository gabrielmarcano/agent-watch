#!/bin/bash
# Helper script to forward Claude Code events to the local bridge server

EVENT_TYPE="$1"
INPUT=$(cat)

if [ -z "$INPUT" ]; then
  INPUT="{}"
fi

# Construct the payload containing the event type and the raw data from Claude
PAYLOAD=$(cat <<EOF
{
  "agent": "Claude",
  "event": "$EVENT_TYPE",
  "timestamp": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "data": $INPUT
}
EOF
)

# Send to the local bridge server asynchronously
curl -s -X POST \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD" \
  http://localhost:8420/webhook >/dev/null 2>&1 &
