SHELL := /bin/bash

GO ?= go
BIN_DIR := bin
DIST_DIR := dist
COVERAGE_DIR := $(DIST_DIR)/coverage
TOOLS_DIR := $(BIN_DIR)/tools
DOCS_TOOLS_SCRIPT := scripts/dev/install_docs_tools.sh
DOCS_MD := $(shell find docs -type f -name '*.md' 2>/dev/null) README.md CONTRIBUTING.md AGENTLAB_DEV_SPECIFICATION.md PROXMOX_SPECS.md
STATICCHECK_VERSION ?= v0.5.1
GOVULNCHECK_VERSION ?= v1.1.2
FUZZ_TIME ?= 10s
STATICCHECK_BIN := $(TOOLS_DIR)/staticcheck
GOVULNCHECK_BIN := $(TOOLS_DIR)/govulncheck

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
TEST_CI_FLAGS ?= -count=1 -shuffle=on

LDFLAGS := -s -w \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Version=$(VERSION)' \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Commit=$(COMMIT)' \
	-X 'github.com/agentlab/agentlab/internal/buildinfo.Date=$(DATE)'

.PHONY: all build build-ssh-gateway lint quality staticcheck govulncheck test test-ci test-coverage test-race test-integration test-all fuzz coverage-audit coverage-html docs-tools docs-lint docs-links docs-typos docs-check docs-gen docs-verify clean

# Note: This project requires Go 1.24.0 or higher. Running 'go version' will show the installed version.

all: build

build: $(BIN_DIR)/agentlab $(BIN_DIR)/agentlabd $(DIST_DIR)/agentlab_linux_amd64 $(DIST_DIR)/agentlabd_linux_amd64

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(DIST_DIR):
	mkdir -p $(DIST_DIR)

$(COVERAGE_DIR):
	mkdir -p $(COVERAGE_DIR)

$(TOOLS_DIR):
	mkdir -p $(TOOLS_DIR)

$(STATICCHECK_BIN): | $(TOOLS_DIR)
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

$(GOVULNCHECK_BIN): | $(TOOLS_DIR)
	GOBIN=$(abspath $(TOOLS_DIR)) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

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

lint: quality

quality:
	@fmt_out="$$(gofmt -l .)"; \
	if [ -n "$$fmt_out" ]; then \
		echo "gofmt needed:"; \
		echo "$$fmt_out"; \
		exit 1; \
	fi
	$(GO) vet ./...
	$(MAKE) staticcheck
	$(MAKE) govulncheck

staticcheck: $(STATICCHECK_BIN)
	$(STATICCHECK_BIN) ./...

govulncheck: $(GOVULNCHECK_BIN)
	$(GOVULNCHECK_BIN) ./...

test:
	$(GO) test ./...

test-ci: quality
	$(GO) test $(TEST_CI_FLAGS) ./...
	$(GO) test $(TEST_CI_FLAGS) -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	$(GO) test -race ./...

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

fuzz:
	$(GO) test ./cmd/agentlab -run=^$ -fuzz=Fuzz -fuzztime=$(FUZZ_TIME)

docs-tools:
	$(DOCS_TOOLS_SCRIPT)

docs-lint:
	@command -v npx >/dev/null 2>&1 || { echo \"npx (Node.js) is required for markdownlint-cli2\"; exit 1; }
	npx --yes markdownlint-cli2 -c .markdownlint-cli2.yaml

docs-links:
	@if [ ! -x \"$(TOOLS_DIR)/lychee\" ]; then echo \"lychee not found. Run 'make docs-tools' first.\"; exit 1; fi
	\"$(TOOLS_DIR)/lychee\" --config .lychee.toml $(DOCS_MD)

docs-typos:
	@if [ ! -x \"$(TOOLS_DIR)/typos\" ]; then echo \"typos not found. Run 'make docs-tools' first.\"; exit 1; fi
	\"$(TOOLS_DIR)/typos\" --config _typos.toml $(DOCS_MD)

docs-check: docs-lint docs-links docs-typos

docs-gen:
	./scripts/dev/gen_cli_docs.sh

docs-verify:
	./scripts/dev/gen_cli_docs.sh
	@if [ -n "$$(git status --porcelain -- docs/cli.md)" ]; then \
		echo "docs/cli.md is out of date. Run 'make docs-gen'."; \
		git status --porcelain -- docs/cli.md; \
		exit 1; \
	fi

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)
