BINARY    := s3m
BUILD_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin
MODULE    := github.com/dcorbell/s3m
GOFLAGS   := -trimpath
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS   := -X $(MODULE)/internal/buildinfo.Version=$(VERSION) -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) -X $(MODULE)/internal/buildinfo.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build build-linux-amd64 install uninstall test vet lint fmt clean run help

build: ## Build the binary for the host platform
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .

build-linux-amd64: ## Cross-compile static binary for linux/amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .

install: build ## Build and install to ~/.local/bin
	@mkdir -p $(INSTALL_DIR)
	cp $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "Installed $(BINARY) to $(INSTALL_DIR)/$(BINARY)"

uninstall: ## Remove from ~/.local/bin
	rm -f $(INSTALL_DIR)/$(BINARY)
	@echo "Removed $(BINARY) from $(INSTALL_DIR)"

test: ## Run all tests
	go test ./... -v

test-short: ## Run tests without verbose output
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go files
	gofmt -w .

fmt-check: ## Check formatting (fails if unformatted)
	@test -z "$$(gofmt -l .)" || (echo "Unformatted files:" && gofmt -l . && exit 1)

tidy: ## Tidy go.mod
	go mod tidy

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)

run: build ## Build and launch the TUI
	$(BUILD_DIR)/$(BINARY)

check: fmt-check vet test-short ## Run all checks (format, vet, tests)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
