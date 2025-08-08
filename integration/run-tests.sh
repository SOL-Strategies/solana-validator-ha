#!/bin/bash

set -e

echo "🚀 Starting Solana Validator HA Integration Tests"
echo "=================================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    print_error "Docker is not running. Please start Docker and try again."
    exit 1
fi

# Check if required ports are available
check_port() {
    local port=$1
    if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1; then
        print_warning "Port $port is already in use. Tests may fail."
        return 1
    fi
    return 0
}

print_status "Checking port availability..."
check_port 8899  # Mock Solana RPC
check_port 9090  # Validator-1 metrics
check_port 9091  # Validator-2 metrics
check_port 9092  # Validator-3 metrics

# Clean up any existing containers
print_status "Cleaning up existing containers..."
docker compose down --volumes --remove-orphans 2>/dev/null || true

# Build and start the test environment
print_status "Building and starting test environment..."
docker compose up --build -d

# Wait for services to be ready
print_status "Waiting for services to be ready..."
sleep 20

# Check if all services are running
print_status "Checking service status..."
if ! docker compose ps | grep -q "Up"; then
    print_error "Some services failed to start. Check logs with: docker compose logs"
    exit 1
fi

print_status "All services are running!"

# Show service status
echo ""
print_status "Service Status:"
docker compose ps

echo ""
print_status "Test Environment URLs:"
echo "  Mock Solana RPC:     http://localhost:8899"
echo "  Public IP Service:   http://localhost:8899/public-ip"
echo "  Validator-1 Status:  http://localhost:9090/status"
echo "  Validator-2 Status:  http://localhost:9091/status"
echo "  Validator-3 Status:  http://localhost:9092/status"

echo ""
print_status "Running integration test scenarios..."
echo "=========================================="

# Wait for the test orchestrator to complete
print_status "Waiting for test orchestrator to complete..."
timeout=300  # 5 minutes timeout
start_time=$(date +%s)

while true; do
    # Check if orchestrator has completed successfully
    if docker compose logs test-orchestrator 2>/dev/null | grep -q "✅ Integration test completed successfully!"; then
        echo ""
        print_status "✅ All integration tests passed!"
        echo ""
        print_status "Test Summary:"
        echo "  ✅ Scenario 1: One active and two passive peers"
        echo "  ✅ Scenario 2: Active peer disconnection"
        echo "  ✅ Scenario 3: Multiple passive peers compete"
        echo ""
        print_status "You can view logs with: docker compose logs -f"
        print_status "Stop the environment with: docker compose down"
        exit 0
    fi
    
    # Check if orchestrator has failed
    if docker compose logs test-orchestrator 2>/dev/null | grep -q "❌ Integration test failed"; then
        echo ""
        print_error "❌ Integration tests failed!"
        echo ""
        print_status "Debugging information:"
        echo "  View logs: docker compose logs"
        echo "  Check validator status: curl http://localhost:9090/status"
        echo "  Test mock services: curl http://localhost:8899/public-ip"
        echo ""
        print_status "To clean up: docker compose down --volumes"
        exit 1
    fi
    
    # Check timeout
    current_time=$(date +%s)
    elapsed=$((current_time - start_time))
    if [ $elapsed -gt $timeout ]; then
        echo ""
        print_error "❌ Test timeout after ${timeout} seconds!"
        echo ""
        print_status "Debugging information:"
        echo "  View logs: docker compose logs"
        echo "  Check validator status: curl http://localhost:9090/status"
        echo "  Test mock services: curl http://localhost:8899/public-ip"
        echo ""
        print_status "To clean up: docker compose down --volumes"
        exit 1
    fi
    
    sleep 5
done 