# Makefile for solana-validator-ha

# Variables
BINARY_NAME := solana-validator-ha
BUILD_DIR := bin
SMOKE_BINARY := $(BUILD_DIR)/.smoke/$(BINARY_NAME)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
ARTIFACT_VERSION ?= $(patsubst v%,%,$(VERSION))
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -ldflags="-s -w -X github.com/sol-strategies/solana-validator-ha/cmd.version=$(VERSION) -X github.com/sol-strategies/solana-validator-ha/cmd.buildCommit=$(COMMIT) -X github.com/sol-strategies/solana-validator-ha/cmd.buildTime=$(BUILD_TIME)"
export COMPOSE_BAKE := true

# Build targets
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Default target
.PHONY: all
all: build

# Local development build
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 go build -mod=mod $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/solana-validator-ha

# Docker build (linux-amd64)
.PHONY: build-docker
build-docker:
	@echo "Building $(BINARY_NAME) for Docker..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=mod $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-$(ARTIFACT_VERSION)-linux-amd64 ./cmd/solana-validator-ha

# Cross-platform build for all platforms
.PHONY: build-all
build-all:
	@echo "Building $(BINARY_NAME) for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@set -e; ARTIFACT_VERSION='$(ARTIFACT_VERSION)'; \
	for platform in $(PLATFORMS); do \
		OS=$$(echo $$platform | cut -d'/' -f1); \
		ARCH=$$(echo $$platform | cut -d'/' -f2); \
		OUTPUT_NAME=$(BINARY_NAME)-$$ARTIFACT_VERSION-$$OS-$$ARCH; \
		echo "Building for $$OS/$$ARCH..."; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -mod=mod $(LDFLAGS) -o $(BUILD_DIR)/$$OUTPUT_NAME ./cmd/solana-validator-ha; \
	done
	@HOST_OS=$$(go env GOHOSTOS); HOST_ARCH=$$(go env GOHOSTARCH); \
	HOST_BINARY=$(BUILD_DIR)/$(BINARY_NAME)-$(ARTIFACT_VERSION)-$$HOST_OS-$$HOST_ARCH; \
	if [ -x "$$HOST_BINARY" ]; then \
		"$$HOST_BINARY" --help | grep -q 'Version: $(VERSION)' && \
		"$$HOST_BINARY" version | grep -q 'commit: $(COMMIT)' && \
		NO_COLOR=1 "$$HOST_BINARY" replay internal/recording/testdata/two-node/*.json >/dev/null; \
	fi
	@echo "Compressing binaries..."
	@cd $(BUILD_DIR) && \
	for binary in $(BINARY_NAME)-*; do \
		if [ -f "$$binary" ] && [ "$${binary##*.}" != "sha256" ] && [ "$${binary##*.}" != "gz" ]; then \
			echo "Compressing $$binary..."; \
			gzip -f "$$binary"; \
		fi; \
	done
	@echo "Generating checksums..."
	@cd $(BUILD_DIR) && \
	for binary in $(BINARY_NAME)-*.gz; do \
		if [ -f "$$binary" ]; then \
			echo "Generating checksum for $$binary..."; \
			sha256sum "$$binary" > "$$binary.sha256"; \
		fi; \
	done
	@echo "Build complete. Compressed binaries and checksums are in $(BUILD_DIR)/"

.PHONY: replay-preview
replay-preview:
	@go run ./cmd/solana-validator-ha replay internal/recording/testdata/$(or $(REPLAY_SCENARIO),two-node)/*.json

.PHONY: smoke-test
smoke-test:
	@echo "Building smoke-test binary..."
	@mkdir -p $(dir $(SMOKE_BINARY))
	@CGO_ENABLED=0 go build -mod=mod $(LDFLAGS) -o $(SMOKE_BINARY) ./cmd/solana-validator-ha
	@$(SMOKE_BINARY) >/dev/null
	@$(SMOKE_BINARY) --help | grep -q 'Version: $(VERSION)'
	@$(SMOKE_BINARY) --help | grep -q 'replay'
	@$(SMOKE_BINARY) --help | grep -q 'version'
	@$(SMOKE_BINARY) --version | grep -q '$(VERSION)'
	@$(SMOKE_BINARY) version | grep -q 'commit: $(COMMIT)'
	@$(SMOKE_BINARY) replay --help >/dev/null
	@NO_COLOR=1 $(SMOKE_BINARY) replay internal/recording/testdata/two-node/*.json >/dev/null

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run integration tests
integration-test:
	@echo "Running integration tests..."
	cd integration && ./run-tests.sh
	@echo "Integration tests completed!"

# Start the integration docker compose environment and wait for services to be ready.
# Run this before `make integration-test`. For GIF recording use `make demo` instead.
.PHONY: integration
integration:
	@echo "Starting integration environment..."
	@cd integration && docker compose up --build -d
	@echo "Waiting 30s for services to be ready..."
	@sleep 30
	@echo "Integration environment ready."

# Tear down the integration environment.
.PHONY: integration-down
integration-down:
	@echo "Stopping integration environment..."
	@cd integration && docker compose down --volumes --remove-orphans

# Start mock-solana for GIF recording (runs the HA binary locally — no validator containers).
# Run this before `make gifs`.
.PHONY: demo
demo:
	@echo "Starting demo environment (mock-solana only)..."
	@docker compose -f integration/docker-compose.demo.yml up --build -d
	@echo "Waiting 5s for mock-solana to be ready..."
	@sleep 5
	@echo "Demo environment ready. Run 'make gifs' to record GIFs."

# Tear down the demo environment.
.PHONY: demo-down
demo-down:
	@echo "Stopping demo environment..."
	@docker compose -f integration/docker-compose.demo.yml down --volumes --remove-orphans

# Record demo GIFs using VHS (https://github.com/charmbracelet/vhs).
# Requires: vhs (go install github.com/charmbracelet/vhs@latest)
# Run `make demo` first to start mock-solana, then `make gifs` to record.
# Produces: docs/passive-node.gif  docs/active-node.gif
.PHONY: gifs
gifs: build
	@mkdir -p docs
	@echo "Recording passive-node GIF..."
	@vhs integration/tapes/passive-node.tape
	@echo "Recording active-node GIF..."
	@vhs integration/tapes/active-node.tape
	@echo "GIFs saved to docs/"


# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run

# Docker build
docker-build:
	@echo "Building Docker image..."
	docker build -t ${BINARY_NAME}:${VERSION} .

# Docker run
docker-run:
	@echo "Running Docker container..."
	docker run -p 9090:9090 -v $(PWD)/config.yaml:/app/config.yaml ${BINARY_NAME}:${VERSION} run --config /app/config.yaml

# Development with hot reload
.PHONY: dev
dev:
	@echo "Starting development environment with hot reload..."
	@docker compose -f docker-compose.dev.yml up --build solana-validator-ha

# Stop Docker development
.PHONY: dev-stop
dev-stop:
	@echo "Stopping development environment..."
	@docker compose -f docker-compose.dev.yml down

# Development setup (local)
dev-setup:
	@echo "Setting up development environment..."
	go mod download
	go mod tidy
	go install github.com/air-verse/air@latest
	@echo "Development environment ready! Run 'air' to start with hot reloading."

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)

# Generate checksums
checksums:
	@echo "Generating checksums..."
	cd bin && for file in ${BINARY_NAME}-*; do \
		sha256sum "$$file" > "$$file.sha256"; \
	done

# Install the binary
install: build
	@echo "Installing ${BINARY_NAME}..."
	sudo cp bin/${BINARY_NAME} /usr/local/bin/

# Uninstall the binary
uninstall:
	@echo "Uninstalling ${BINARY_NAME}..."
	sudo rm -f /usr/local/bin/${BINARY_NAME}

# Show help
help:
	@echo "Available targets:"
	@echo "  build          - Build the binary locally"
	@echo "  build-all      - Build binaries for all platforms (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64)"
	@echo "  build-docker   - Build for Docker (linux-amd64)"
	@echo "  clean          - Clean build artifacts"
	@echo "  test           - Run tests"
	@echo "  replay-preview - Render real JSON fixtures (set REPLAY_SCENARIO; default two-node)"
	@echo "  smoke-test     - Verify binary help, version, and replay commands"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  integration-test - Run integration tests"
	@echo "  integration      - Start integration docker compose environment"
	@echo "  integration-down - Stop integration docker compose environment"
	@echo "  demo             - Start mock-solana for GIF recording (lighter than integration)"
	@echo "  demo-down        - Stop demo environment"
	@echo "  gifs             - Record demo GIFs with VHS (run after make demo)"
	@echo "  deps           - Install dependencies"
	@echo "  fmt            - Format code"
	@echo "  lint           - Run linter"
	@echo "  docker-build   - Build Docker image"
	@echo "  docker-run     - Run Docker container"
	@echo "  dev             - Start development environment with hot reload (Docker)"
	@echo "  dev-stop        - Stop development environment"
	@echo "  dev-setup       - Setup local development environment"
	@echo "  checksums      - Generate checksums"
	@echo "  install        - Install binary"
	@echo "  uninstall      - Uninstall binary"
	@echo "  help           - Show this help"
