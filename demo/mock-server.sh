#!/bin/bash

# Mock Server Management Script
# Usage: ./mock-server.sh [start|stop|restart|status]

set -e

CONTAINER_NAME="solana-validator-ha-mock-server"
COMPOSE_FILE="docker-compose.yml"

function start_mock_server() {
    echo "Starting mock server..."
    docker compose -f $COMPOSE_FILE up -d
    echo "Mock server started on http://localhost:8989"
    echo "Use 'docker compose logs -f' to view logs"
}

function stop_mock_server() {
    echo "Stopping mock server..."
    docker compose -f $COMPOSE_FILE down
    echo "Removing dangling images..."
    docker image prune -f
    echo "Mock server stopped and cleaned up"
}

function restart_mock_server() {
    echo "Restarting mock server..."
    stop_mock_server
    sleep 2
    start_mock_server
}

function show_status() {
    echo "Mock server status:"
    docker compose -f $COMPOSE_FILE ps
    echo ""
    echo "Container logs (last 10 lines):"
    docker compose -f $COMPOSE_FILE logs --tail=10
}

case "${1:-}" in
    start)
        start_mock_server
        ;;
    stop)
        stop_mock_server
        ;;
    restart)
        restart_mock_server
        ;;
    status)
        show_status
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status}"
        echo ""
        echo "Commands:"
        echo "  start   - Start the mock server"
        echo "  stop    - Stop the mock server and clean up dangling images"
        echo "  restart - Restart the mock server"
        echo "  status  - Show current status and recent logs"
        exit 1
        ;;
esac
