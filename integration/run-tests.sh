#!/bin/bash

set -e

mode="default"
compose_file="docker-compose.yml"
scenarios_dir="scenarios"

if [ "${1:-}" = "--shared" ]; then
    mode="shared-backup"
    compose_file="docker-compose.shared.yml"
    scenarios_dir="scenarios-shared"
elif [ "$#" -gt 0 ]; then
    echo "Usage: $0 [--shared]"
    exit 2
fi

echo "🚀 Starting Solana Validator HA Integration Tests (${mode})"
echo "=================================================="

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

print_status()  { echo -e "${GREEN}[INFO]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }
print_error()   { echo -e "${RED}[ERROR]${NC} $1"; }

compose() {
    docker compose -f "$compose_file" "$@"
}

# Check Docker
if ! docker info > /dev/null 2>&1; then
    print_error "Docker is not running. Please start Docker and try again."
    exit 1
fi

# Warn if required ports are in use
check_port() {
    local port=$1
    if lsof -Pi :"$port" -sTCP:LISTEN -t >/dev/null 2>&1; then
        print_warning "Port $port is already in use — tests may fail."
    fi
}

print_status "Checking port availability..."
check_port 8899  # mock-solana RPC
check_port 9090  # validator-1 metrics
check_port 9091  # validator-2 metrics
check_port 9092  # validator-3 metrics

# Show scenarios that will run
if [ ! -d "$scenarios_dir" ]; then
    print_error "Scenario directory not found: $scenarios_dir"
    exit 1
fi

shopt -s nullglob
scenario_files=("$scenarios_dir"/*.yaml)
shopt -u nullglob
if [ "${#scenario_files[@]}" -eq 0 ]; then
    print_error "No scenario YAML files found in $scenarios_dir"
    exit 1
fi

print_status "Scenarios to run from ${scenarios_dir}:"
for f in "${scenario_files[@]}"; do
    name=$(grep '^name:' "$f" | head -1 | sed 's/name: *//')
    echo "  • $name  ($f)"
done
echo ""

# Clean up existing containers
print_status "Cleaning up existing containers..."
compose down --volumes --remove-orphans 2>/dev/null || true

# Build and start
print_status "Building and starting test environment with ${compose_file}..."
compose up --build -d

print_status "Waiting for services to be ready..."
sleep 20

if ! compose ps | grep -q "Up"; then
    print_error "Some services failed to start. Check logs with: docker compose -f ${compose_file} logs"
    exit 1
fi

print_status "All services are running."
echo ""
print_status "Service URLs:"
echo "  Mock Solana RPC:   http://localhost:8899"
echo "  Validator-1:       http://localhost:9090/metrics"
echo "  Validator-2:       http://localhost:9091/metrics"
echo "  Validator-3:       http://localhost:9092/metrics"
echo ""
print_status "Running integration test scenarios..."
echo "=========================================="

# Poll orchestrator logs until it finishes (5-minute timeout)
timeout=300
start_time=$(date +%s)

while true; do
    if compose logs test-orchestrator 2>/dev/null | grep -q "✅ Integration test completed successfully!"; then
        echo ""
        print_status "✅ All integration tests passed!"
        echo ""
        print_status "To view full logs:  docker compose -f ${compose_file} logs -f"
        print_status "To tear down:       docker compose -f ${compose_file} down"
        exit 0
    fi

    if compose logs test-orchestrator 2>/dev/null | grep -q "❌ Integration test failed"; then
        echo ""
        print_error "❌ Integration tests failed!"
        echo ""
        print_status "Orchestrator log:"
        compose logs test-orchestrator 2>/dev/null | tail -30
        echo ""
        print_status "Debugging:"
        echo "  Full logs:          docker compose -f ${compose_file} logs"
        echo "  Validator-1 metrics: curl http://localhost:9090/metrics"
        echo "  Tear down:          docker compose -f ${compose_file} down --volumes"
        exit 1
    fi

    current_time=$(date +%s)
    elapsed=$((current_time - start_time))
    if [ "$elapsed" -gt "$timeout" ]; then
        echo ""
        print_error "❌ Test timeout after ${timeout}s!"
        echo ""
        print_status "Orchestrator log:"
        compose logs test-orchestrator 2>/dev/null | tail -30
        echo ""
        print_status "To tear down: docker compose -f ${compose_file} down --volumes"
        exit 1
    fi

    sleep 5
done
