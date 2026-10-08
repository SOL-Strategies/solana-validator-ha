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
- **Expected Behavior**: Only one validator becomes active (first responder wins)
- **Validation**: Confirms that only one validator becomes active despite multiple candidates

### Scenarios 5–13: Alpenglow

These drive the mock's consensus simulation (`set_phase`, `set_vote_lag`, `stall_finalization`,
`set_local_genesis`, `exclude_vote_account`, `remove_bls_key`) and check both roles and metrics (`assert_metric`).
They run in file order and must stay after scenarios 1–4: once an HA client has seen the
Alpenglow genesis certificate it never leaves the alpenglow phase, and `reset` keeps the phase.

| Scenario | Expected behaviour |
|---|---|
| `05-migration-window` | During the migration a lagging active is not failed over; a disconnected one is |
| `06-alpenglow-zombie-active` | Active in gossip with votes not landing → one passive takes over |
| `07-alpenglow-cluster-stall` | Finalization stalls → no failover, `cluster_stalled` vetoes counted |
| `08-alpenglow-stall-plus-host-down` | Stall plus active disconnect → failover through gossip absence |
| `09-local-not-migrated` | Highest-ranked passive has no local genesis → the next one takes over |
| `10-vote-account-excluded` | Active vote account missing → no failover, `vote_account_excluded` vetoes counted |
| `11-phase-sticky` | RPC answers as TowerBFT after Alpenglow → HA clients stay in the alpenglow phase |
| `12-vote-account-not-in-voter-set` | Active vote account staked but without a BLS pubkey → no failover, verdict `vote_account_excluded` |
| `13-failover-ineffective` | Votes do not land on any node → one takeover only, `failover_ineffective` vetoes counted |

### Scenario 14: Network isolation

`fail_cluster_rpc` makes cluster RPC calls from one validator fail (the mock tells callers apart by
their docker IP); `isolate` also freezes that validator's local processed slot.

| Scenario | Expected behaviour |
|---|---|
| `14-network-isolation` | Cluster RPC down with the local slot moving → active stays; local slot also frozen → active demotes itself and a passive takes over |

## Failover Logic

The current system uses a **first-responder wins** approach:

1. **Leaderless Detection**: If no active peer is found for `leaderless_samples_threshold` consecutive samples
2. **Race Condition**: The first healthy, passive validator to detect the leaderless state becomes active
3. **No Priority System**: It's a race condition where the fastest validator wins

## Running Tests

### Quick Start

```bash
# From the project root
make integration-test

# Or directly from the integration directory
cd integration
./run-tests.sh
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

### Identity Setup

All validators share the same **active keypair** but have different **passive keypairs**:

- **Shared Active Identity**: `active-identity.json` (used by all validators)
- **Individual Passive Identities**: 
  - `passive-identity-1.json` (validator-1)
  - `passive-identity-2.json` (validator-2)
  - `passive-identity-3.json` (validator-3)

All validators use:
- **Dry Run Mode**: Commands are logged but not executed
- **Fast Polling**: 3-second intervals for quick testing
- **Mock Solana RPC**: Points to the mock network
- **Mock Public IP Service**: Returns the container's network IP

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

1. Add a new method to `test-orchestrator/main.go`
2. Update the `runAllScenarios()` function to include the new scenario
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