SHELL := /bin/bash

GO ?= go
BIN_DIR := bin
DIST_DIR := dist
COVERAGE_DIR := $(DIST_DIR)/coverage

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Version=$(VERSION)' \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Commit=$(COMMIT)' \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Date=$(DATE)'

.PHONY: all build build-ssh-gateway lint test test-coverage test-race test-integration test-all coverage-audit coverage-html clean

# Note: This project requires Go 1.24.0 or higher. Running 'go version' will show the installed version.

all: build

build: $(BIN_DIR)/agentlab $(BIN_DIR)/agentlabd $(DIST_DIR)/agentlab_linux_amd64 $(DIST_DIR)/agentlabd_linux_amd64

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(DIST_DIR):
	mkdir -p $(DIST_DIR)

$(COVERAGE_DIR):
	mkdir -p $(COVERAGE_DIR)

$(BIN_DIR)/agentlab: | $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/agentlab

$(BIN_DIR)/agentlabd: | $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/agentlabd

build-ssh-gateway: $(BIN_DIR)/agentlab-ssh-gateway

$(BIN_DIR)/agentlab-ssh-gateway: | $(BIN_DIR)
	$(GO) build -tags sshgateway -ldflags "$(LDFLAGS)" -o $@ ./cmd/agentlab-ssh-gateway

$(DIST_DIR)/agentlab_linux_amd64: | $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/agentlab

$(DIST_DIR)/agentlabd_linux_amd64: | $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/agentlabd

lint:
	@fmt_out="$$(gofmt -l .)"; \
	if [ -n "$$fmt_out" ]; then \
		echo "gofmt needed:"; \
		echo "$$fmt_out"; \
		exit 1; \
	fi
	$(GO) vet ./...

test:
	$(GO) test ./...

test-coverage:
	$(GO) test -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

coverage-audit:
	GO=$(GO) ./scripts/dev/coverage_audit.sh

coverage-html: | $(COVERAGE_DIR)
	$(GO) test -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html

test-race:
	$(GO) test -race ./...

test-integration:
	$(GO) test -tags=integration ./...

test-all: test test-race test-coverage

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)
