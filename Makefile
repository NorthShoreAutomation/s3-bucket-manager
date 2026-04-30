BINARY    := s3m
BUILD_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin
MODULE    := github.com/dcorbell/s3m
GOFLAGS   := -trimpath
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS   := -X $(MODULE)/internal/buildinfo.Version=$(VERSION) -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) -X $(MODULE)/internal/buildinfo.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build build-linux-amd64 install uninstall test test-short vet fmt fmt-check tidy clean run check help

##@ Build

build: ## Build the binary for the host platform
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .

build-linux-amd64: ## Cross-compile a static binary for linux/amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)

##@ Install

install: build ## Build and install to ~/.local/bin
	@mkdir -p $(INSTALL_DIR)
	cp $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "Installed $(BINARY) to $(INSTALL_DIR)/$(BINARY)"

uninstall: ## Remove from ~/.local/bin
	rm -f $(INSTALL_DIR)/$(BINARY)
	@echo "Removed $(BINARY) from $(INSTALL_DIR)"

##@ Run

run: build ## Build and launch the TUI
	$(BUILD_DIR)/$(BINARY)

##@ Test & Quality

test: ## Run all tests (verbose)
	go test ./... -v

test-short: ## Run all tests (quiet)
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go files (gofmt -w)
	gofmt -w .

fmt-check: ## Check formatting; fails if any file is unformatted
	@test -z "$$(gofmt -l .)" || (echo "Unformatted files:" && gofmt -l . && exit 1)

check: fmt-check vet test-short ## Run fmt-check, vet, and tests

##@ Maintenance

tidy: ## Tidy go.mod and go.sum
	go mod tidy

##@ Help

help: ## Show this help
	@awk 'BEGIN { \
		FS = ":.*## "; \
		printf "\n  \033[1;36m$(BINARY)\033[0m \033[2m$(VERSION)\033[0m  \033[2m— S3 bucket manager\033[0m\n\n"; \
		printf "  \033[1mUsage:\033[0m  make \033[36m<target>\033[0m\n"; \
	} \
	/^##@ / { printf "\n  \033[1;33m%s\033[0m\n", substr($$0, 5); next } \
	/^[a-zA-Z0-9_-]+:.*## / { printf "    \033[36m%-20s\033[0m %s\n", $$1, $$2 } \
	END { printf "\n" }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
