.PHONY: build bridge relay relay-linux bar bar-test restart test vet fmt check check-versions clean
.PHONY: config configure-bridge deploy-relay watchos-config _need-bridge-env _need-deploy-env _need-watchos-env

# ── Component versions: VERSIONS ──────────────────────────────────────────
# The rules are in VERSIONS. A value given on the command line wins, e.g.
# make bridge BRIDGE_VERSION=0.0.0-test.
-include VERSIONS
need-version = $(if $(strip $($(1))),,$(error $(1) is not set: VERSIONS is missing or incomplete))
BRIDGE_LDFLAGS = -s -w -X main.version=$(BRIDGE_VERSION)
RELAY_LDFLAGS = -s -w -X main.version=$(RELAY_VERSION)
UNAME_S := $(shell uname -s)

# ── Shared configuration: agent-watch.env ─────────────────────────────────
# The one file a deployment edits (agent-watch.env.example documents it).
# Included for the guards below only: recipes never expand AW_HOST_TOKEN,
# the programs they run read it from the file themselves.
AW_ENV_FILE ?= agent-watch.env
-include $(AW_ENV_FILE)
AWENV := tools/config/awenv.sh
WATCHOS_XCCONFIG ?= watchos-app/Config.generated.xcconfig

need-env = $(if $(wildcard $(AW_ENV_FILE)),,$(error $(AW_ENV_FILE) not found: run `make config` first))
need-key = $(if $(strip $($(1))),,$(error $(1) is not set in $(AW_ENV_FILE)))
need-domain = $(call need-key,AW_RELAY_DOMAIN)$(if $(filter relay.example.com,$(strip $(AW_RELAY_DOMAIN))),$(error AW_RELAY_DOMAIN in $(AW_ENV_FILE) is still the example value: set your relay's host name))

build: bridge relay

# herdr-plugin.toml's [[build]] runs the same build (BRIDGE_VERSION from VERSIONS).
bridge:
	@:$(call need-version,BRIDGE_VERSION)
	CGO_ENABLED=0 go build -ldflags "$(BRIDGE_LDFLAGS)" -o bin/agent-watch-bridge ./cmd/bridge

relay:
	@:$(call need-version,RELAY_VERSION)
	CGO_ENABLED=0 go build -ldflags "$(RELAY_LDFLAGS)" -o bin/agent-watch-relay ./cmd/relay

relay-linux:
	@:$(call need-version,RELAY_VERSION)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(RELAY_LDFLAGS)" -o bin/agent-watch-relay-linux-amd64 ./cmd/relay

# macOS menu bar companion (bin/AgentWatchBar.app), stamped with MENUBAR_VERSION.
bar:
ifeq ($(UNAME_S),Darwin)
	@:$(call need-version,MENUBAR_VERSION)
	MENUBAR_VERSION=$(MENUBAR_VERSION) ./macos-bar/build.sh
else
	@echo "bar: the menu bar app only builds on macOS" >&2; exit 1
endif

# Tests the menu bar's logic (macos-bar/BarLogic.swift) with a swiftc-built
# harness. It runs the freshly built CLI with HOME set to an empty temp dir:
# it never launches the app, calls launchctl or reads the real home.
ifeq ($(UNAME_S),Darwin)
# Builds its own CLI in a temp dir: bin/agent-watch-bridge is what the
# installed service runs, and a test run must never replace it.
bar-test:
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	mkdir "$$tmp/home" && \
	CGO_ENABLED=0 go build -o "$$tmp/agent-watch-bridge" ./cmd/bridge && \
	swiftc -swift-version 6 macos-bar/BarLogic.swift macos-bar/Tests/main.swift -o "$$tmp/bartests" && \
	"$$tmp/bartests" "$$tmp/agent-watch-bridge" "$$tmp/home"
else
bar-test:
	@echo "bar-test: the menu bar app only builds on macOS" >&2; exit 1
endif

# Rebuild bin/agent-watch-bridge and restart the installed service without
# rewriting its definition. The service runs the binary its definition names
# (normally this checkout's bin/); `agent-watch-bridge start --binary` changes it.
restart: bridge
	./bin/agent-watch-bridge restart

# Create agent-watch.env from the example (mode 0600) and generate
# AW_HOST_TOKEN if it is empty. Safe to re-run: it never replaces a token.
config:
	@$(AWENV) init $(AW_ENV_FILE) agent-watch.env.example
	@echo "next: set AW_RELAY_DOMAIN and AW_RELAY_SSH in $(AW_ENV_FILE) (docs/GUIDE.md, Setup Guide)"

# Write the bridge config (relay URL + host token) from agent-watch.env, then
# `make restart`. configure rewrites the whole config.toml: pass the optional
# settings again, e.g. make configure-bridge ARGS="--claude-config-dir ~/work/claude-profile".
configure-bridge: _need-bridge-env bridge
	./bin/agent-watch-bridge configure --env-file $(AW_ENV_FILE) $(ARGS)

_need-bridge-env:
	@:$(call need-env)$(call need-domain)$(call need-key,AW_HOST_TOKEN)

# Deploy the relay to AW_RELAY_SSH (with AW_RELAY_SSH_OPTS). ARGS=--sync-env
# also copies the relay keys of agent-watch.env into the server's env file.
deploy-relay: _need-deploy-env
	AW_ENV_FILE=$(AW_ENV_FILE) deploy/relay/deploy.sh $(ARGS)

_need-deploy-env:
	@:$(call need-env)$(call need-key,AW_RELAY_SSH)

# Generate the git-ignored watchOS xcconfig (relay URL, bundle id):
# docs/reference/contracts.md §7.
watchos-config: _need-watchos-env
	@$(AWENV) xcconfig $(AW_ENV_FILE) $(WATCHOS_XCCONFIG)
	@echo "wrote $(WATCHOS_XCCONFIG)"

_need-watchos-env:
	@:$(call need-env)$(call need-domain)

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd pkg

# VERSIONS is well formed and herdr-plugin.toml's version equals BRIDGE_VERSION
# (the manifest cannot read the file). Also part of `make test`.
check-versions:
	go test -count=1 -run 'TestVersionsFile|TestPluginManifestMatchesVersions' ./pkg/buildinfo/

check: fmt vet check-versions test

clean:
	rm -rf bin
