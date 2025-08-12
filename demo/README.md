# Demo Mock Server

This directory contains a mock server that simulates the Solana network and validator responses for demo purposes.

## Overview

The mock server provides the following endpoints:

- `/validator-1/public-ip` - Returns the public IP for validator-1
- `/validator-1-rpc` - Handles RPC requests for validator-1 (getIdentity, getHealth)
- `/validator-2/public-ip` - Returns the public IP for validator-2
- `/validator-2-rpc` - Handles RPC requests for validator-2 (getIdentity, getHealth)
- `/validator-3/public-ip` - Returns the public IP for validator-3
- `/validator-3-rpc` - Handles RPC requests for validator-3 (getIdentity, getHealth)
- `/solana-network-rpc` - Handles Solana network RPC requests (getClusterNodes)

## Configuration

The mock server reads its configuration from `mock-config.yaml`. This file is read on each request, allowing you to dynamically change the server's responses by modifying the file.

### Configuration Structure

```yaml
validators:
  validator-1:
    public_ip: "192.168.1.101"
    on_startup_identity: "active"  # "active" or "passive"
    passive_pubkey: "CP6FdV1zoaB64zV7riBPcMNV4WrtH6NZqEcN8cR1yp3i"
    active_pubkey: "7TCCEYMNjpRRdVQiMMYkNWJ9ECVThcUaw8mp11AWhTKe"
    is_offline: false
    healthy: true
    is_active: true  # Runtime state - set automatically based on on_startup_identity
  
  validator-2:
    public_ip: "192.168.1.102"
    on_startup_identity: "passive"  # "active" or "passive"
    passive_pubkey: "EhH5vaRnSvYKeYpNkDeFh3x1RgqqsTUsYmkrFZ2iv8mE"
    active_pubkey: "7TCCEYMNjpRRdVQiMMYkNWJ9ECVThcUaw8mp11AWhTKe"
    is_offline: false
    healthy: true
    is_active: false  # Runtime state
```

### Configuration Options

- `validators`: Map of validator configurations
  - `public_ip`: The public IP address for the validator
  - `on_startup_identity`: Which identity to use on startup ("active" or "passive")
  - `passive_pubkey`: The validator's unique passive identity (used when not active)
  - `active_pubkey`: The validator's active identity (shared across all validators)
  - `is_offline`: Whether the validator appears offline in gossip network (true/false)
  - `healthy`: Whether the validator is healthy (true/false)
  - `is_active`: Runtime state - automatically set based on on_startup_identity

## Running the Mock Server

### Using the Management Script (Recommended)

The easiest way to manage the mock server is using the provided script:

```bash
# Start the mock server
./mock-server.sh start

# Stop the mock server and clean up dangling images
./mock-server.sh stop

# Restart the mock server
./mock-server.sh restart

# Check status and recent logs
./mock-server.sh status
```

### Using Docker Compose

1. Start the mock server:
   ```bash
   docker compose up -d
   ```

2. The server will be available at `http://localhost:8989`

3. To stop the server:
   ```bash
   docker compose down
   ```

### Using Docker

1. Build the image:
   ```bash
   cd mock-server
   docker build -t mock-server .
   ```

2. Run the container:
   ```bash
   docker run -p 8989:8989 -v $(pwd)/../mock-config.yaml:/config/mock-config.yaml:ro mock-server
   ```

## Usage Examples

### Get validator public IP
```bash
curl http://localhost:8989/validator-1/public-ip
# Returns: 192.168.1.100
```

### Get validator identity
```bash
curl -X POST http://localhost:8989/validator-1-rpc \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"getIdentity"}'
```

### Get validator health
```bash
curl -X POST http://localhost:8989/validator-1-rpc \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}'
```

### Get cluster nodes
```bash
curl -X POST http://localhost:8989/solana-network-rpc \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"getClusterNodes"}'
```

### Dynamic Control Endpoints

#### Set validator identity (active/passive)
```bash
# Set validator-1 to active
curl "http://localhost:8989/validator-1/set-identity/active"

# Set validator-1 to passive
curl "http://localhost:8989/validator-1/set-identity/passive"
```

#### Set validator gossip state (online/offline)
```bash
# Set validator-1 to offline (won't appear in getClusterNodes)
curl "http://localhost:8989/solana-network-rpc/set-gossip-state?validator=validator-1&online=false"

# Set validator-1 to online (will appear in getClusterNodes)
curl "http://localhost:8989/solana-network-rpc/set-gossip-state?validator=validator-1&online=true"
```

## Demo Scenarios

You can create different demo scenarios using the dynamic control endpoints:

### 1. Failover Scenario
```bash
# Start with validator-1 active
./mock-server.sh start

# Simulate validator-1 failure by setting it to passive
curl "http://localhost:8989/validator-1/set-identity/passive"

# Set validator-2 to active (failover)
curl "http://localhost:8989/validator-2/set-identity/active"
```

### 2. Split-Brain Scenario
```bash
# Set multiple validators to active
curl "http://localhost:8989/validator-1/set-identity/active"
curl "http://localhost:8989/validator-2/set-identity/active"
curl "http://localhost:8989/validator-3/set-identity/active"
```

### 3. Network Partition Scenario
```bash
# Take validator-1 offline in gossip network
curl "http://localhost:8989/solana-network-rpc/set-gossip-state?validator=validator-1&online=false"

# Take validator-2 offline
curl "http://localhost:8989/solana-network-rpc/set-gossip-state?validator=validator-2&online=false"
```

### 4. Recovery Scenario
```bash
# Bring validators back online
curl "http://localhost:8989/solana-network-rpc/set-gossip-state?validator=validator-1&online=true"
curl "http://localhost:8989/solana-network-rpc/set-gossip-state?validator=validator-2&online=true"

# Set proper active validator
curl "http://localhost:8989/validator-1/set-identity/active"
curl "http://localhost:8989/validator-2/set-identity/passive"
curl "http://localhost:8989/validator-3/set-identity/passive"
```

All changes take effect immediately and are reflected in both individual RPC calls and cluster nodes responses.

## Integration with Your Binary

To use this mock server with your solana-validator-ha binary:

1. Configure your binary to use the mock server endpoints
2. Set the cluster RPC URL to `http://localhost:8989/solana-network-rpc`
3. Set validator RPC URL to `http://localhost:8989/validator-1-rpc` (or appropriate validator)
4. Configure the public IP detection to use `http://localhost:8989/validator-1/public-ip`

This setup allows you to create controlled demo scenarios for generating gifs and testing failover behavior.
