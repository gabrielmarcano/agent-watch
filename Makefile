.PHONY: build bridge relay relay-linux test vet fmt check clean

VERSION ?= 0.2.0
LDFLAGS := -s -w -X main.version=$(VERSION)

build: bridge relay

bridge:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-bridge ./cmd/bridge

relay:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-relay ./cmd/relay

relay-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/agent-watch-relay-linux-amd64 ./cmd/relay

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd pkg

check: fmt vet test

clean:
	rm -rf bin
