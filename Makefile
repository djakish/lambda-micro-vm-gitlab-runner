VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath
BINDIR  := bin

# Architecture of the MicroVM (must match the Lambda base image): amd64 | arm64
MICROVM_ARCH ?= amd64

.PHONY: all build executor agent agent-linux setup test vet fmt tidy clean

all: build

## build both binaries for the host platform
build: executor agent

## build the driver and launch the interactive installer
setup: executor
	./$(BINDIR)/microvm-executor setup

## driver CLI for the runner host
executor:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINDIR)/microvm-executor ./cmd/microvm-executor

## in-VM agent for the host platform (handy for local testing)
agent:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINDIR)/microvm-agent ./cmd/microvm-agent

## in-VM agent cross-compiled for the MicroVM (Linux). The Dockerfile builds
## this from source too; this target is for local verification.
agent-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(MICROVM_ARCH) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINDIR)/microvm-agent-linux-$(MICROVM_ARCH) ./cmd/microvm-agent

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf $(BINDIR)
