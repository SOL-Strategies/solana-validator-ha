# Makefile for solana-validator-ha

# Variables
BINARY_NAME := solana-validator-ha
BUILD_DIR := bin
LDFLAGS := -ldflags="-s -w"
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
	@go mod tidy
	@CGO_ENABLED=0 go build -mod=mod $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/solana-validator-ha

# Docker build (linux-amd64)
.PHONY: build-docker
build-docker:
	@echo "Building $(BINARY_NAME) for Docker..."
	@mkdir -p $(BUILD_DIR)
	@go mod tidy
	@VERSION=$$(cat cmd/version.txt | tr -d '\n'); \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=mod $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-$$VERSION-linux-amd64 ./cmd/solana-validator-ha

# Cross-platform build for all platforms
.PHONY: build-all
build-all:
	@echo "Building $(BINARY_NAME) for all platforms..."
	@echo "Debug: Current directory: $$(pwd)"
	@echo "Debug: Contents of cmd/:"
	@ls -la cmd/ || echo "cmd/ directory not found"
	@echo "Debug: Contents of cmd/solana-validator-ha/:"
	@ls -la cmd/solana-validator-ha/ || echo "cmd/solana-validator-ha/ directory not found"
	@mkdir -p $(BUILD_DIR)
	@go mod tidy
	@VERSION=$$(cat cmd/version.txt | tr -d '\n'); \
	for platform in $(PLATFORMS); do \
		OS=$$(echo $$platform | cut -d'/' -f1); \
		ARCH=$$(echo $$platform | cut -d'/' -f2); \
		OUTPUT_NAME=$(BINARY_NAME)-$$VERSION-$$OS-$$ARCH; \
		echo "Building for $$OS/$$ARCH..."; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -mod=mod $(LDFLAGS) -o $(BUILD_DIR)/$$OUTPUT_NAME ./cmd/solana-validator-ha; \
	done
	@echo "Compressing binaries..."
	@cd $(BUILD_DIR) && \
	for binary in $(BINARY_NAME)-*; do \
		if [ -f "$$binary" ] && [ "$${binary##*.}" != "sha256" ]; then \
			echo "Compressing $$binary..."; \
			gzip "$$binary"; \
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

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f bin/${BINARY_NAME}*
	rm -f bin/*.sha256
	rm -f coverage.out coverage.html

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
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  integration-test - Run integration tests"
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
