# Build context: the repository root, after `make relay-linux`:
#   docker build -f deploy/relay/Dockerfile -t agent-watch-relay .
#   docker run -d --name agent-watch-relay -p 127.0.0.1:8080:8080 \
#     -e AW_HOST_TOKEN=... -v agent-watch-relay-data:/var/lib/agent-watch-relay agent-watch-relay

# distroless has no shell to create the data dir, so it is prepared here:
# owned by distroless's nonroot user (65532) and private (0700).
FROM busybox:1.36 AS datadir
RUN mkdir -p /data && chown 65532:65532 /data && chmod 0700 /data

FROM gcr.io/distroless/static:nonroot
COPY bin/agent-watch-relay-linux-amd64 /agent-watch-relay
COPY --from=datadir --chown=65532:65532 /data /var/lib/agent-watch-relay
ENV AW_DATA_DIR=/var/lib/agent-watch-relay \
    AW_LISTEN=:8080
# store.json, relay.lock and admin.sock live here. A named volume starts as a
# copy of this directory (right owner and mode); a bind-mounted host directory
# must be owned by 65532 and mode 0700 (chown 65532:65532 <dir>; chmod 0700 <dir>).
VOLUME ["/var/lib/agent-watch-relay"]
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/agent-watch-relay", "serve"]
