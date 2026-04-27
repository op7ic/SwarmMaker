# Makefile
# Author: Jerzy 'Yuri' Kramarz (op7ic)
# Copyright: See LICENSE file
# Github: https://github.com/op7ic/SwarmMaker
#
# SwarmMaker build system. Source code lives in src/swarmmaker/.

.PHONY: build install uninstall clean test lint fmt all release

BINARY_NAME=swarm-me
VERSION=0.1.0
BUILD_DIR=./build
SRC_DIR=./src/swarmmaker
CMD=$(SRC_DIR)/cmd/swarm-me

# Install to ~/.local/bin by default (no sudo needed).
# Override: make install PREFIX=/usr/local
PREFIX=$(HOME)/.local
INSTALL_DIR=$(PREFIX)/bin

LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"
GOFLAGS=-trimpath

all: fmt lint test build

build:
	@echo "Building $(BINARY_NAME) v$(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	cd $(SRC_DIR) && go build $(GOFLAGS) $(LDFLAGS) -o ../../$(BUILD_DIR)/$(BINARY_NAME) ./cmd/swarm-me
	@echo "Binary: $(BUILD_DIR)/$(BINARY_NAME)"

install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_DIR)..."
	@mkdir -p $(INSTALL_DIR)
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@chmod +x $(INSTALL_DIR)/$(BINARY_NAME)
	@echo ""
	@echo "Installed $(BINARY_NAME) v$(VERSION) to $(INSTALL_DIR)/$(BINARY_NAME)"
	@case ":$$PATH:" in \
		*":$(INSTALL_DIR):"*) ;; \
		*) echo "NOTE: $(INSTALL_DIR) is not in your PATH."; \
		   echo "  Add to ~/.bashrc or ~/.zshrc:"; \
		   echo "    export PATH=\"$(INSTALL_DIR):\$$PATH\""; \
		   echo "" ;; \
	esac
	@echo "Run 'swarm-me --help' to get started."

uninstall:
	@echo "Removing $(BINARY_NAME) from $(INSTALL_DIR)..."
	@rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Done."

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)

test:
	cd $(SRC_DIR) && go test ./... -race -count=1

lint:
	@command -v golangci-lint >/dev/null 2>&1 || (echo "golangci-lint not installed; lint cannot run" && exit 1)
	cd $(SRC_DIR) && golangci-lint run ./...

fmt:
	cd $(SRC_DIR) && gofmt -s -w .

# Cross-compile for all platforms
release:
	@echo "Building release binaries v$(VERSION)..."
	@mkdir -p $(BUILD_DIR)/release
	cd $(SRC_DIR) && GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o ../../$(BUILD_DIR)/release/$(BINARY_NAME)-linux-amd64 ./cmd/swarm-me
	cd $(SRC_DIR) && GOOS=linux   GOARCH=arm64 go build $(GOFLAGS) $(LDFLAGS) -o ../../$(BUILD_DIR)/release/$(BINARY_NAME)-linux-arm64 ./cmd/swarm-me
	cd $(SRC_DIR) && GOOS=darwin  GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o ../../$(BUILD_DIR)/release/$(BINARY_NAME)-darwin-amd64 ./cmd/swarm-me
	cd $(SRC_DIR) && GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) $(LDFLAGS) -o ../../$(BUILD_DIR)/release/$(BINARY_NAME)-darwin-arm64 ./cmd/swarm-me
	cd $(SRC_DIR) && GOOS=windows GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o ../../$(BUILD_DIR)/release/$(BINARY_NAME)-windows-amd64.exe ./cmd/swarm-me
	@echo "Release binaries in $(BUILD_DIR)/release/"

.DEFAULT_GOAL := build
