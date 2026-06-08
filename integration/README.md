# Integration Tests

This directory contains comprehensive integration tests for the Solana Validator HA system. The tests simulate real-world scenarios with multiple validators and test failover behavior in a controlled environment.

## Architecture

The integration test environment consists of:

- **3 Validator Containers**: Each running the HA manager
- **Mock Solana Network**: Simulates Solana RPC responses and provides public IP service
- **Test Orchestrator**: Controls test scenarios and validates results

## Test Scenarios

The integration test validates the following scenarios:

### Scenario 1: One Active and Two Passive Peers
- **Initial State**: Validator-1 is active, Validator-2 and Validator-3 are passive
- **Expected Behavior**: All validators remain in their assigned roles, no failover occurs
- **Validation**: Confirms stable operation with one active and two passive peers

### Scenario 2: Active Peer Disconnection
- **Initial State**: Validator-1 is active, others are passive
- **Action**: Simulate network disconnection of Validator-1
- **Expected Behavior**: One of the passive validators becomes active (first responder wins)
- **Validation**: Confirms proper failover when active peer becomes unavailable

### Scenario 3: Multiple Passive Peers Compete
- **Initial State**: Validator-1 is active, Validator-2 and Validator-3 are passive
- **Action**: Disconnect Validator-1, causing both passive peers to attempt becoming active
- **Expected Behavior**: Only one validator becomes active, with configured peer priority or IP ranking resolving the race
- **Validation**: Confirms that only one validator becomes active despite multiple candidates

### Shared Backup Suite
- **Initial State**: Two profiles are active on different validators, with one shared backup eligible for both
- **Expected Behavior**: The shared backup can take one failed profile, but never serves two active profiles at once
- **Validation**: Confirms profile holder tracking, local-busy blocking, profile-priority selection, and multiple backups inside one profile

## Failover Logic

The current system uses a **ranked first-responder** approach:

1. **Leaderless Detection**: If no active peer is found for `leaderless_samples_threshold` consecutive samples
2. **Peer Ranking**: Eligible peers wait according to their configured peer priority, or by public IP order when no peer priorities are configured
3. **Profile Selection**: When one daemon sees multiple profiles eligible at once, it promotes the lowest `profiles.<name>.priority` value
4. **Single Local Active Identity**: A daemon already active for one profile is treated as busy and will not promote another profile

## Running Tests

### Quick Start

```bash
# From the project root
make integration-test

# Or directly from the integration directory
cd integration
./run-tests.sh

# Run the shared-backup profile suite
./run-tests.sh --shared
```

The shared-backup suite is also available from the project root:

```bash
make integration-test-shared
```

### Manual Testing

```bash
# Start the test environment
cd integration
docker compose up --build

# In another terminal, check logs
docker compose logs -f

# Stop the environment
docker compose down
```

## Configuration

Each validator has its own configuration file in `configs/`:

- `validator-1.yaml`: First validator
- `validator-2.yaml`: Second validator
- `validator-3.yaml`: Third validator
- `shared-validator-1.yaml`: First validator with `main` and `secondary` profiles
- `shared-validator-2.yaml`: Shared backup validator with `main` and `secondary` profiles
- `shared-validator-3.yaml`: Third validator with `main` and `secondary` profiles

### Identity Setup

The mock network reports one fixed active identity and fixed passive identities for each validator. Each validator config declares one `profiles.main` HA group with the same active identity, vote account, authorized voter, and self-inclusive peer map:

- **Active identity / vote account**: `ArkzFExXXHaA6izkNhTJJ5zpXdQpynffjfRMJu4Yq6H`
- **Passive identities**:
  - `AP4JyZq2vuN4u64FGFHTwdG11xHu1vZWVYQj21MPLrnw` (validator-1)
  - `DJ7w4p8Ve7qdSAmkpA3sviSbsd1HPUxd43x7MTH72JHT` (validator-2)
  - `5dXttfrjFEEExmZhVmVAdw2LzepNAhFYJTUgPCDk8CYD` (validator-3)

All validators use:
- **Mock Role Commands**: Active/passive commands call the mock control API instead of touching real validator identities
- **Fast Polling**: 3-second intervals for quick testing
- **Mock Solana RPC**: Points to the mock network
- **Mock Public IP Service**: Returns the container's network IP

The shared-backup compose file swaps in `configs/shared-validator-*.yaml`.
Those configs declare:

- `profiles.main`: active identity `ArkzFExXXHaA6izkNhTJJ5zpXdQpynffjfRMJu4Yq6H`
- `profiles.secondary`: active identity `SysvarC1ock11111111111111111111111111111111`
- `validator-2` as the shared backup for both profiles

## Network Topology

```
172.20.0.2  - Mock Solana RPC Server (includes public IP service)
172.20.0.10 - Validator-1
172.20.0.11 - Validator-2
172.20.0.12 - Validator-3
172.20.0.100 - Test Orchestrator
```

## Monitoring

Each validator exposes metrics on different ports:

- **Validator-1**: `http://localhost:9090/metrics`
- **Validator-2**: `http://localhost:9091/metrics`
- **Validator-3**: `http://localhost:9092/metrics`

### Status Endpoints

- **Validator-1**: `http://localhost:9090/status`
- **Validator-2**: `http://localhost:9091/status`
- **Validator-3**: `http://localhost:9092/status`

## Mock Services

### Mock Solana RPC Server

The mock server provides:
- **RPC Endpoints**: `getClusterNodes`, `getBlocks`, `getBlock`, `getSlot`, `getIdentity`
- **Public IP Service**: `http://localhost:8899/public-ip` returns the caller's IP
- **Network Control**: `http://localhost:8899/network` for simulating disconnections
- **Active Validator Control**: `http://localhost:8899/control` for setting active validator
- **Profile State**: `http://localhost:8899/state` for the current `active_profiles` map

### Test Orchestrator

The orchestrator:
- **Controls Test Flow**: Manages the sequence of test scenarios
- **Simulates Failures**: Disconnects validators to test failover
- **Validates Results**: Ensures expected behavior in each scenario
- **Provides Logging**: Detailed logs of test execution

## Debugging

### View Validator Logs

```bash
# View all logs
docker compose logs

# View specific validator logs
docker compose logs validator-1
docker compose logs validator-2
docker compose logs validator-3

# Follow logs in real-time
docker compose logs -f
```

### Check Validator Status

```bash
# Check validator-1 status
curl http://localhost:9090/status

# Check validator-2 status
curl http://localhost:9091/status

# Check validator-3 status
curl http://localhost:9092/status
```

### Test Mock Services

```bash
# Test public IP service
curl http://localhost:8899/public-ip

# Test network control (disconnect validator-1)
curl -X POST http://localhost:8899/network \
  -H "Content-Type: application/json" \
  -d '{"disconnect_validator": "validator-1"}'

# Test active validator control
curl -X POST http://localhost:8899/control \
  -H "Content-Type: application/json" \
  -d '{"active_validator": "validator-2"}'
```

## Test Results

The test orchestrator validates:

- ✅ **Scenario 1**: Stable operation with one active, two passive
- ✅ **Scenario 2**: Proper failover when active peer disconnects
- ✅ **Scenario 3**: First responder wins prevents multiple active validators
- ✅ **Role Transitions**: Proper active ↔ passive role changes
- ✅ **Health Monitoring**: Status reporting and metrics collection

## Troubleshooting

### Common Issues

1. **Port Conflicts**: Ensure ports 8899, 9090-9092 are available
2. **Network Issues**: Check Docker network configuration
3. **Build Failures**: Ensure all dependencies are available
4. **Test Timeouts**: Increase timeout values if tests are slow

### Debug Mode

To run with verbose logging:

```bash
# Set debug environment variable
export DEBUG=true
make integration-test
```

### Clean Environment

```bash
# Clean up all containers and networks
docker compose down --volumes --remove-orphans
docker system prune -f
```

### Manual Test Execution

```bash
# Start services without orchestrator
docker compose up mock-solana validator-1 validator-2 validator-3

# Run orchestrator manually
docker compose run test-orchestrator
```

## Development

### Adding New Test Scenarios

1. Add a YAML file under `scenarios/` or `scenarios-shared/`
2. Use `active_profiles` assertions for profile-specific shared-backup behavior
3. Update this README with the new scenario description

### Modifying Mock Services

1. Update `mock-solana/main.go` for RPC changes
2. Update validator configurations in `configs/` for behavior changes
3. Test changes with `docker compose up --build`

### Extending Test Coverage

The current setup provides a foundation for testing:
- Network partition scenarios
- Multiple simultaneous failures
- Recovery and re-election scenarios
- Performance under load
- Configuration validation 
