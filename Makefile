SHELL := /bin/bash

GO ?= go
BIN_DIR := bin
DIST_DIR := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Version=$(VERSION)' \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Commit=$(COMMIT)' \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Date=$(DATE)'

.PHONY: all build lint test clean

all: build

build: $(BIN_DIR)/agentlab $(BIN_DIR)/agentlabd $(DIST_DIR)/agentlab_linux_amd64 $(DIST_DIR)/agentlabd_linux_amd64

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(DIST_DIR):
	mkdir -p $(DIST_DIR)

$(BIN_DIR)/agentlab: | $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/agentlab

$(BIN_DIR)/agentlabd: | $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/agentlabd

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

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)
