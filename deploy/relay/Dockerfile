FROM gcr.io/distroless/static:nonroot
COPY bin/agent-watch-relay-linux-amd64 /agent-watch-relay
USER nonroot
ENTRYPOINT ["/agent-watch-relay", "serve"]
