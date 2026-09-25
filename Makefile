.PHONY: build bridge relay relay-linux bar bar-test restart test vet fmt check clean

# Keep VERSION in sync with herdr-plugin.toml (version and the [[build]] ldflags).
VERSION ?= 0.2.0
LDFLAGS := -s -w -X main.version=$(VERSION)
UNAME_S := $(shell uname -s)

build: bridge relay

bridge:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-bridge ./cmd/bridge

relay:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-relay ./cmd/relay

relay-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-relay-linux-amd64 ./cmd/relay

# macOS menu bar companion (bin/AgentWatchBar.app).
bar:
ifeq ($(UNAME_S),Darwin)
	VERSION=$(VERSION) ./macos-bar/build.sh
else
	@echo "bar: the menu bar app only builds on macOS" >&2; exit 1
endif

# Tests the menu bar's logic (macos-bar/BarLogic.swift) with a swiftc-built
# harness. It runs the freshly built CLI with HOME set to an empty temp dir:
# it never launches the app, calls launchctl or reads the real home.
ifeq ($(UNAME_S),Darwin)
bar-test: bridge
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	mkdir "$$tmp/home" && \
	swiftc -swift-version 6 macos-bar/BarLogic.swift macos-bar/Tests/main.swift -o "$$tmp/bartests" && \
	"$$tmp/bartests" "$(CURDIR)/bin/agent-watch-bridge" "$$tmp/home"
else
bar-test:
	@echo "bar-test: the menu bar app only builds on macOS" >&2; exit 1
endif

# Rebuild bin/agent-watch-bridge and restart the installed service without
# rewriting its definition. The service runs the binary its definition names
# (normally this checkout's bin/); `agent-watch-bridge start --binary` changes it.
restart: bridge
	./bin/agent-watch-bridge restart

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd pkg

check: fmt vet test

clean:
	rm -rf bin
