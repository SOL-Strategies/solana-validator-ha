# solana-validator-ha

A gossip-based high availability (HA) manager for Solana validators.

## Demo

A simulated automatic HA failover resulting from `validator-1 (active)` disconnecting from the network given the validator set `[validator-1 (active), validator-2 (passive), validator-3 (passive)]`:

**validator-1** - disconnects, becomes `passive`, and `validator-3` becomes `active`

![demo-validator-1](demo/validator-1/demo.gif)

**validator-2** - detects `validator-1` disconnection, sees `validator-3` take over as `active`

![demo-validator-2](demo/validator-2/demo.gif)

**validator-3** - detects `validator-1` disconnection, takes over as `active`

![demo-validator-3](demo/validator-3/demo.gif)

## Features

- **🔍 Intelligent Peer Detection**: Automatically detects validator roles based on network gossip and RPC identity
- **🛡️ Self-Healing**: Validators transition between active/passive roles based on health and network visibility
- **🔗 Hook System**: Comprehensive pre/post hooks with `must_succeed` support for role transitions
- **📝 Template Support**: Commands and hooks support Go template variables
- **🧪 Dry Run Mode**: Test failover logic without executing actual commands
- **📊 Prometheus Metrics**: Rich metrics collection for monitoring and alerting
- **🌐 Multi-RPC Support**: Multiple cluster RPC URLs for redundancy and load distribution
- **⚡ Throttling Protection**: Smart RPC client load balancing to avoid rate limits
- **🏁 First-Responder Failover**: Race-based failover where fastest healthy passive validator assumes active role when no active validator found in gossip
- **🔧 Integration Testing**: Comprehensive Docker-based integration test suite

## Architecture

The system consists of several key components:

- **🎯 HA Manager**: Core logic for monitoring peers and making failover decisions
- **🌐 Gossip Monitor**: Interfaces with Solana gossip network to track validator states
- **📡 RPC Client**: Multi-endpoint RPC client with intelligent failover and throttling protection
- **📊 Prometheus Metrics**: Comprehensive metrics collection and HTTP endpoint
- **⚙️ Configuration**: YAML-based configuration with validation and templating support
- **🧪 Integration Tests**: Docker Compose-based testing with mock Solana network

### Conceptual overview

`solana-validator-ha` aims to provide a simple, low-dependency HA solution to running 2 or more related validators on a Solana cluster where one of these should be an `active (voting)` peer with the others remaining `passive (non-voting)`. The set of validators each have a unique `passive` identity and a shared `active` identity. The program discovers its HA peers using the Solana cluster's set of validators and each peer makes independent failover decisions when no active peer is discovered.

This approach safeguards against network disconnection or dead nodes, ensuring an `active` validator from the peer list is always online. To this end two (‼️**very**‼️) important user-supplied configuration settings are required:

1. A command to run to assume the `active` role. This is simply a reference to a user-supplied command that will be called on the current node only if a failover is required, the node is healthy, is discoverable on the Solana cluster, and no other peers have already assumed the `active` role. As such it should not return until it has successfully set and confirmed the node is active. Typically this would be something along the lines of:
   
   ```yaml
      #...
      failover:
       active:
         command: "set-identity-with-rollback.sh" # user-supplied command -everyone's setup is different :-)
         args: [
           "--to-identity-file", "{{ .Identities.PassiveKeypairFile }}",
           "--rollback-identity-file", "{{ .Identities.PassiveKeypairFile }}",
         ]
      #...
   ```

2. A command to run to assume a `passive` role (a.k.a _Seppukku_). This is simply a reference to an **idempotent** user-supplied command that ensures the validator is set to `passive`. An `active` validator that detects itself as disconnected from the Solana network will call this independently to ensure it doesn't come back online as `active` and cause duplicate identity atempts. Operators may find it safest to configure validators to always start with a `passive` identity so that this would simply require restarting the validator service and waiting for it to report healthy. Something along the lines of:

   ```yaml
      #...
      failover:
       passive:
         command: "seppukku.sh" # user-supplied command -everyone's setup is different :-)
         args: [
           "--passive-identity-file", "{{ .PassiveIdentityKeypairFile }}",
         ]
      #...
   ```


It is important to note that this does not safeguard against `delinquent` vote accounts. Failing over on these events cannot be guaranteed to fix things.

## Quick Start

### Build/Development Prerequisites

- **Go 1.24 or later**
- **Docker** (optional, for containerized deployment/development)

### Installation

1. **Clone the repository:**
   ```bash
   git clone https://github.com/sol-strategies/solana-validator-ha.git
   cd solana-validator-ha
   ```

2. **Build the application:**
   ```bash
   make build
   # or manually:
   go build -o bin/solana-validator-ha ./cmd/solana-validator-ha
   ```

3. **Copy the binary to where you need it:**
   ```bash
   cp ./bin/solana-validator-ha /usr/local/bin/solana-validator-ha
   ```

## Configuration

The application uses a `YAML` configuration file with the following root sections:

### Log Configuration

```yaml
# log
# description:
#   Logging configuration
log:

  # level
  # required: false
  # default: info
  # description:
  #   Minimum log level to print. One of: debug, info, warn, error, fatal
  level: info

  # format
  # required: false
  # default: text
  # description:
  #   Log format. One of: text, logfmt, json
  format: text
```

### Validator Configuration
```yaml
# validator
# description:
#   Settings for the validator this program runs on
validator:

  # name
  # required: true
  # description:
  #   Vanity name for this validator peer - used for logging and metrics
  name: "primary-validator"
  
  # rpc_url
  # required: true
  # default: http://localhost:8899
  # description:
  #   Local RPC URL for querying health and identity status
  rpc_url: "http://localhost:8899"

  # public_ip_service_urls
  # required: false
  # default: see internal/config/validator.go
  # description:
  #   A list of URLs to try to ascertain the current node's public IPv4 address
  #   These should return the IP address as a string in the first line of the response
  public_ip_service_urls: []

  # identities
  # description:
  #   Identities this validator assumes for the given role
  identities:

    # active
    # required: true
    # description:
    #   Path to active keypair file - this is shared across peers
    active: "/path/to/active-identity.json"

    # passive
    # required: true
    # description:
    #   Path to passive keypair file - this is unique across peers
    passive: "/path/to/passive-identity.json"
```

### Prometheus Configuration
```yaml
# prometheus
# description:
#   Configuration for running the prometheus metrics server
prometheus:

  # port
  # required: false
  # default: 9099
  # description:
  #   Port to listen on and serve metrics on /metrics endpoint
  port: 9099

  # static_labels
  # required: false
  # description:
  #   A string key:value map of static labels to attach to all exposed prometheus metrics
  static_labels:
    brand: ha-validators
    cluster: mainnet-beta
    region: ha-region-1
```

### Cluster Configuration
```yaml
# cluster
# required: true
# description:
#    Solana cluster configuration
cluster:

# name
  # required: true
  # description:
  #   Solana cluster this validator is running on. One of mainnet-beta, devnet, or testnet
  name: "mainnet-beta"  # mainnet-beta, devnet, or testnet

  # rpc_urls
  # required: false
  # default: RPC URL for the supplied cluster.name
  # description:
  #   List of RPC URLs to query the Solana network for the given cluster.name. Private RPC URLs can be supplied here
  #   and if more than 1 is given the program will round-robin calls on them to avoid throttling. Supplying multiple URLs
  #   here safeguards against RPC glitches/drop-outs so that the program can maintain an accurate peer state from the solana network.
  rpc_urls: []  # Uses cluster defaults if empty
```

### Failover Configuration
```yaml
# failover
# description:
#   Main failover settings
failover:

  # dry_run
  # required: false
  # default: false
  # description:
  #   In the event of a failover event, dry-run commands (use this to test the waters :-)
  dry_run: false

  # poll_inverval_duration
  # required: false
  # default: 5s
  # description:
  #   A Go duration string for how often to poll the local validator RPC and Solana cluster for the validator and its peers' state.
  #   and evaluate failover decisions
  poll_interval_duration: 5s

  # leaderless_threshold_duration
  # required: false
  # default: 15s
  # description:
  #   A Go duration string for how long to consider this validator cluster "leaderless" (no active validator seen on the Solana network)
  #   and thus trigger a failover event. Consider this in the context for poll_interval_duration to allow for occasional failed polls
  leaderless_threshold_duration: 15s

  # takeover_delay_duration
  # required: false
  # default: 3
  # description:
  #   A random jitter delay to add to a passive peer before taking over as active. This is to safeguard against race conditions where
  #  two or more passive validators attempt to take over as passive at the same time.
  takeover_jitter_seconds: 3

  # peers
  # required: true
  # min_length: 1 (at least one peer must be delcared, else we're not HA-ish)
  # description:
  #   A map of peer objects excluding current validator and their IP addresses.
  #   The keys are vanity names for metrics and logging, the IP addresses must be valid and unique
  #   This is what will be used for discovery on the Solana cluster.name
  peers:
    backup-validator-1:
      ip: 192.168.1.11
    backup-validator-2:
      ip: 192.168.1.12
    # ...

  # active
  # required: true
  # description:
  #   Commands and hooks to execute when the failover logic determines this validator should become active
  #   All command, args and env map values support Go template strings with the following data:
  #     - {{ .ActiveIdentityKeypairFile }} - Resolved absolute path to validator.identities.active
  #     - {{ .PassiveIdentityKeypairFile }} - Resolved absolute path to validator.identities.passive
  #     - {{ .ActiveIdentityPubkey }} - Active public key string from validator.identities.active
  #     - {{ .PassiveIdentityPubkey }} - Passive public key string from validator.identities.passive
  #     - {{ .SelfName }} - Name as declared in validator.name
  active:

    # command
    # required: true
    # description:
    #   Command to run to make the current validator assume an active role - be mindful of its importance
   command: set-identity-with-rollback.sh

   # env
   # required: false
   # description:
   #   Environment variables for active.command
   env:
    CUSTOM_ENV_VAR: "{{ .Identities.ActiveIdentityPubkey }}"

   # args
   # required: false
   # description:
   #   Args for active.command
   args: [
     "active",
     "--active-identity-file", "{{ .Identities.ActiveIdentityKeypairFile }}",
     "--passive-identity-file", "{{ .Identities.PassiveIdentityKeypairFile }}",
   ]

   # hooks
   # required: false
   # description
   #   Optional hooks to run before/after running active.command
   #   They are executed in the order they are declared. Pre-hooks optionally support must_succeed which if set to true
   #   Abort the execution of subsequent hooks and will not run active.command
   #   Hook names are vanity names for logging and are converted to lower-snake_case
   hooks:

    pre:
      - name: notify-slack-promoting
        command: /home/solana/solana-validator-ha/hooks/pre-active/send-slack-alert.sh
        must_succeed: false # optional, defaults to false
        env: {}
        args: [
          "--channel", "#save-my-bacon",
          "--message", "solana-validator-ha promoting {{ .SelfName }} to active by changing identities from {{ .PassiveIdentityPubkey }} -> {{ .ActiveIdentityPubkey }}"
        ]
      # ...

    post:
      - name: notify-slack-promoted
        command: /home/solana/solana-validator-ha/hooks/post-active/send-slack-alert.sh
        env: {}
        args: [
          "--channel", "#saved-my-bacon",
          "--message", "solana-validator-ha promoted {{ .SelfName }} to active with identity {{ .ActiveIdentityPubkey }}"
        ]
      # ...

  # passive
  # required: true
  # description:
  #   Commands and hooks to execute when the failover logic determines this validator should become passive
  #   All command and args values support Go template strings with the following data:
  #     - {{ .ActiveIdentityKeypairFile }} - Resolved absolute path to validator.identities.active
  #     - {{ .PassiveIdentityKeypairFile }} - Resolved absolute path to validator.identities.passive
  #     - {{ .ActiveIdentityPubkey }} - Active public key string from validator.identities.active
  #     - {{ .PassiveIdentityPubkey }} - Passive public key string from validator.identities.passive
  #     - {{ .SelfName }} - Name as declared in validator.name
  passive:

    # command
    # required: true
    # description:
    #   Command to run to make the current validator assume a passive role - be mindful of its importance.
    #   This should be idempotent such that multiple calls result in always having the validator be passive.
   command: seppukku.sh

   # args
   # required: false
   # description:
   #   Args for passive.command
   args: [
     "--passive-identity-file", "{{ .Identities.PassiveKeypairFile }}",
     "--stop-service-on-identity-set-failure",
     "--wait-for-and-force-identity-on-service-starting-up",
     # ... any other scenarios or logic your setup requires to handle ensuring the validator is either set to passive
     # or taken off the menu.
   ]

   # hooks
   # required: false
   # description
   #   Optional hooks to run before/after running passive.command
   #   They are executed in the order they are declared. Pre-hooks optionally support must_succeed which if set to true
   #   Abort the execution of subsequent hooks and will not run passive.command
   #   Hook names are vanity names for logging and are converted to lower-snake_case
   hooks:

    pre:
      - name: notify-slack-demoting
        command: /home/solana/solana-validator-ha/hooks/pre-passive/send-slack-alert.sh
        must_succeed: false # optional, defaults to false
        args: [
          "--channel", "#oh-shit-wake-people-up",
          "--message", "solana-validator-ha demoting {{ .SelfName }} to passive by changing identities from {{ .ActiveIdentityPubkey }} -> {{ .PassiveIdentityPubkey }}"
        ]
      # ...

    post:
      - name: notify-slack-demoted
        command: /home/solana/solana-validator-ha/hooks/post-passive/send-slack-alert.sh
        args: [
          "--channel", "#postmortem-shelf",
          "--message", "solana-validator-ha demoted {{ .SelfName }} to passive with identity {{ .PassiveIdentityPubkey }}"
        ]
      # ...

```

## Development

### Available Commands
```bash
# Building
make build                # Build for development (current platform)
make build-all            # Build for all release platforms
make clean                # Clean build artifacts

# Testing
make test                 # Run unit tests
make test-coverage        # Run tests with coverage report
make integration-test     # Run Docker-based integration tests

# Development
make dev                  # Start development environment (Docker)
make dev-setup            # Setup local development with hot reload
make fmt                  # Format code
make lint                 # Run linter

# Docker
make docker-build         # Build Docker image
make docker-run           # Run Docker container

# Installation
make install              # Install binary to /usr/local/bin
make uninstall            # Remove installed binary
```

### Development Environment

For local development with hot reloading:
```bash
make dev-setup  # Install air for hot reloading
air             # Start with hot reloading
```

For Docker-based development:
```bash
make dev        # Start with Docker Compose
```

## Testing

### Unit Tests
```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run specific package tests
go test ./internal/config -v

# Test with debug mode
TEST_MODE=true go test ./...
```

### Integration Tests

The project includes comprehensive Docker-based integration tests:

```bash
# Run integration tests
make integration-test

# Or directly
cd integration && ./run-tests.sh
```

**Test Scenarios:**
1. **Stable Operation**: One active + two passive peers (no failover)
2. **Active Failover**: Active peer disconnects, passive takes over
3. **Race Condition**: Multiple passive peers compete (first responder wins)
4. **Health Validation**: Unhealthy peers don't become active

## Monitoring & Metrics

The application exposes Prometheus metrics on the configured port (default: 9090):

### Core Metrics
- **`solana_validator_ha_metadata`**: Validator metadata with role and status labels
- **`solana_validator_ha_peer_count`**: Number of peers visible in gossip
- **`solana_validator_ha_self_in_gossip`**: Whether this validator appears in gossip (1=yes, 0=no)
- **`solana_validator_ha_failover_status`**: Current failover status

### Metric Labels
- `validator_name`: Configured validator name
- `public_ip`: Validator's public IP address
- `validator_role`: Current role (active/passive/unknown)
- `validator_status`: Health status (healthy/unhealthy)
- Plus any configured static labels

### Health Endpoints
- **`/metrics`**: Prometheus metrics
- **`/health`**: Basic health check

## Failover Logic

The system uses a **first-responder wins** approach:

1. **🔍 Continuous Monitoring**: Poll Solana gossip every `failover.poll_interval_duration`
2. **⚠️ Leaderless Detection**: Trigger if no active peer found for `failover.leaderless_threshold_duration`
3. **🏃 Race Condition**: First healthy passive validator to detect leaderless state wins
4. **⏱️ Takeover Delay**: Brief delay + jitter to reduce collision probability
5. **✅ Health Validation**: Only healthy validators in gossip can become active

## Docker Deployment

### Production Deployment
```bash
# Build production image
make docker-build

# Run with custom config
docker run -d \
  --name solana-ha \
  -p 9090:9090 \
  -v /path/to/config.yaml:/app/config.yaml \
  -v /path/to/keypairs:/app/keypairs \
  solana-validator-ha:latest run --config /app/config.yaml
```

### Docker Compose
```yaml
version: '3.8'
services:
  solana-ha:
    build: .
    ports:
      - "9090:9090"
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./keypairs:/app/keypairs
    command: ["run", "--config", "/app/config.yaml"]
```

## Building for Release

### Automated Releases
The project includes GitHub Actions for automated releases:
1. Create a semantic version tag (e.g., `v1.0.0`)
2. GitHub Actions automatically builds for all platforms
3. Generates checksums and creates release

### Manual Build
```bash
# Build for current platform
make build

# Build for all platforms
make build-all

# Generate checksums
make checksums
```

**Supported Platforms:**
- Linux (amd64, arm64)
- macOS (amd64, arm64) 
- Windows (amd64)

## Troubleshooting

### Common Issues

1. **Validator not appearing in gossip**
   - Check network connectivity
   - Verify public IP detection
   - Ensure Solana validator is running

2. **RPC connection failures**
   - Verify `rpc_url` is accessible
   - Check if multiple `rpc_urls` help with redundancy
   - Monitor rate limiting with multiple URLs

3. **Failover not triggering**
   - Check `leaderless_threshold_duration` setting
   - Verify peer configuration
   - Enable debug logging

### Debug Mode
```bash
# Enable debug logging
LOG_LEVEL=debug ./bin/solana-validator-ha run --config config.yaml

# Use dry run mode
# Set dry_run: true in config.yaml
```

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Make your changes
4. Add tests for new functionality
5. Run tests: `make test`
6. Run integration tests: `make integration-test`
7. Format code: `make fmt`
8. Submit a pull request

### Development Guidelines
- Follow Go conventions and use `gofmt`
- Add unit tests for new functionality
- Update integration tests for behavioral changes
- Document configuration changes
- Use semantic versioning for releases

## License

This project is licensed under the MIT License - see the LICENSE file for details.
