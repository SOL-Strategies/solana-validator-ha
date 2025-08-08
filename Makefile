.PHONY: build test clean docker-build docker-run help

# Variables
BINARY_NAME=solana-validator-ha
VERSION=$(shell git describe --tags --always --dirty)
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}"

# Default target
all: build

# Build targets for different platforms
BUILD_TARGETS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64

# Development build (current platform)
build:
	@echo "Building ${BINARY_NAME} for development..."
	go mod tidy
	go build -mod=mod ${LDFLAGS} -o bin/${BINARY_NAME} ./cmd/solana-validator-ha

# Build for Docker development (linux-amd64)
build-docker:
	@echo "Building ${BINARY_NAME} for Docker (linux-amd64)..."
	go mod tidy
	GOOS=linux GOARCH=amd64 go build -mod=mod ${LDFLAGS} -o bin/${BINARY_NAME}-linux-amd64 ./cmd/solana-validator-ha

# Build for all release platforms
build-all:
	@echo "Building ${BINARY_NAME} for all platforms..."
	go mod tidy
	@for target in $(BUILD_TARGETS); do \
		echo "Building for $$target..."; \
		GOOS=$$(echo $$target | cut -d'-' -f1) GOARCH=$$(echo $$target | cut -d'-' -f2) go build -mod=mod ${LDFLAGS} -o bin/${BINARY_NAME}-$$target$$(if [ "$$target" = "windows-amd64" ]; then echo ".exe"; fi) ./cmd/solana-validator-ha; \
	done

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
dev:
	@echo "Starting development environment..."
	docker compose -f docker-compose.dev.yml up --build

# Development setup (local)
dev-setup:
	@echo "Setting up development environment..."
	go mod download
	go mod tidy
	go install github.com/air-verse/air@latest
	@echo "Development environment ready! Run 'air' to start with hot reloading."

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
	@echo "  build        - Build for development (current platform)"
	@echo "  build-docker - Build for Docker development (linux-amd64)"
	@echo "  build-all    - Build for all release platforms"
	@echo "  test         - Run tests"
	@echo "  test-coverage- Run tests with coverage"
	@echo "  integration-test - Run integration tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Install dependencies"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run Docker container"
	@echo "  dev          - Start development environment (Docker)"
	@echo "  dev-setup    - Setup local development environment"
	@echo "  checksums    - Generate checksums"
	@echo "  install      - Install binary"
	@echo "  uninstall    - Uninstall binary"
	@echo "  help         - Show this help"
