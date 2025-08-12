#!/bin/bash

# Quick start script for the demo

set -e

echo "🚀 Starting Solana Validator HA Demo..."

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo "❌ Docker is not running. Please start Docker and try again."
    exit 1
fi

# Check if docker-compose is available
if ! command -v docker-compose &> /dev/null; then
    echo "❌ docker-compose is not installed. Please install it and try again."
    exit 1
fi

echo "📦 Building and starting mock server..."
cd "$(dirname "$0")"

# Build and start the mock server
docker-compose up -d --build

echo "⏳ Waiting for mock server to start..."
sleep 5

# Test the mock server
echo "🧪 Testing mock server endpoints..."

# Test public IP endpoint
echo "Testing public IP endpoint..."
PUBLIC_IP=$(curl -s http://localhost:8989/validator-1/public-ip)
echo "✅ Validator-1 public IP: $PUBLIC_IP"

# Test cluster nodes endpoint
echo "Testing cluster nodes endpoint..."
CLUSTER_NODES=$(curl -s -X POST http://localhost:8989/solana-network-rpc \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"getClusterNodes"}')
echo "✅ Cluster nodes response: $CLUSTER_NODES"

echo ""
echo "🎉 Demo setup complete!"
echo ""
echo "📋 Mock server is running at: http://localhost:8989"
echo "📁 Configuration file: mock-config.yaml"
echo "🔧 You can modify mock-config.yaml to change the demo scenario"
echo ""
echo "🚀 To run your binary with this mock server:"
echo "   ./solana-validator-ha run --config demo/config.yaml"
echo ""
echo "🛑 To stop the demo:"
echo "   docker-compose down"
echo ""
echo "📖 For more information, see README.md"
