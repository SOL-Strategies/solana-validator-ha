# solana-validator-ha

A gossip-based high availability (HA) manager for Solana validators. This tool helps automate **unexpected** failovers due to `<insert one of endless reasons>`.
To automate **planned** failovers, see [solana-validator-failover](https://github.com/SOL-Strategies/solana-validator-failover)

![active node handover](docs/active-node.png)

## Demo

`validator-1` (active) loses network connectivity. A passive peer detects the leaderless cluster and takes over automatically.

Passive node (`validator-2`) monitoring the cluster and promoting itself to active:

![passive node takeover](docs/passive-node.gif)

Active node (`validator-1`) detecting it has dropped from gossip and stepping down:

![active node handover](docs/active-node.gif)

## How it works

`solana-validator-ha` provides a simple, low-dependency HA solution for running 2 or more Solana validators together, where one is `active` (voting) and the rest are `passive` (non-voting). All peers share the same `active` keypair identity and each has its own unique `passive` keypair identity.

Each peer runs `solana-validator-ha` independently. It monitors the Solana gossip network to detect whether any peer is currently active and voting. When no active peer has been seen for a configurable number of consecutive samples (the _leaderless threshold_), a failover is triggered. Each peer makes this decision independently using the same gossip data, with a rank-based delay to prevent multiple peers from racing to become active simultaneously.

A node will only become active in a failover if:

1. It appears in gossip (the validator process is running and reachable on the network);
2. Its local RPC reports healthy; and
3. It has been continuously healthy for at least `failover.self_healthy.minimum_duration` (guards against startup health flaps).

To make this work, two (‼️**VERY**‼️) important user-supplied commands are required:

### 🟢 Active command

Called when this node should assume the `active` role. See [example-scripts/ha-set-role.sh](example-scripts/ha-set-role.sh) for inspiration.

```yaml
failover:
  active:
    command: "set-identity-with-rollback.sh"
    args: [
      "--active-identity-file", "{{ .ActiveIdentityKeypairFile }}",
      "--passive-identity-file", "{{ .PassiveIdentityKeypairFile }}",
    ]
```

### 🔴 Passive command

Called when this node should assume a `passive` (non-voting) role — a.k.a _Seppuku_. This command **must be idempotent**: it may be called any time the node detects it should not be active (e.g. when it drops out of gossip). The safest pattern is to configure validators to **always** start with a `passive` identity, so this command can simply restart the validator service and wait for it to come back passive. See [example-scripts/ha-set-role.sh](example-scripts/ha-set-role.sh) for inspiration.

```yaml
failover:
  passive:
    # ⚠️ This must make absolutely sure the validator goes passive.
    # ⚠️ If set-identity fails, restart/stop the service, pull the plug,
    # ⚠️ or call your mum crying for help. Do WHATEVER is necessary.
    command: "seppuku.sh"
    args: [
      "--passive-identity-file", "{{ .PassiveIdentityKeypairFile }}",
    ]
```

> **Note:** `post-passive` hooks only run if the passive command succeeds, as a safeguard against false positives.

## Features

- **🔍 Intelligent Peer Detection**: Automatically detects validator roles based on network gossip and RPC identity
- **🛡️ Startup Health Protection**: Requires a configurable minimum continuous healthy streak before a node can become a failover candidate
- **🪝 Hooks**: Pre/Post failover hook support for role transitions
- **📊 Prometheus Metrics**: Rich metrics collection for monitoring and alerting
- **🏁 First-Responder Failover**: Race-based failover with deterministic rank-based delay so the highest-priority eligible passive validator assumes the active role. Default ordering is ascending IP sort; operators can override with explicit `failover.priority` per node

## Installation

### Download binary

Download and install the latest [release](https://github.com/SOL-Strategies/solana-validator-ha/releases) binary for your system.

### From source

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

```yaml
log:

  # required: false | default: info
  # Minimum log level. One of: debug, info, warn, error, fatal
  level: info

  # required: false | default: text
  # Log format. One of: text, logfmt, json
  format: text

validator:

  # required: true
  # Vanity name for this validator — used in logging and metrics
  name: "primary-validator"

  # required: false | default: http://localhost:8899
  # Local RPC URL for querying health and identity status
  rpc_url: "http://localhost:8899"

  # required: false | default: see internal/config/validator.go
  # List of URLs used to determine this node's public IPv4 address.
  # Each URL should return the IP as a plain string on the first line of the response.
  public_ip_service_urls: []

  identities:

    # required: true (or set active_pubkey)
    # Path to the active keypair file — shared across all HA peers.
    # Takes precedence over active_pubkey if both are set.
    active: "/path/to/active-identity.json"

    # required: true (or set active)
    # Base58-encoded active pubkey. Used when the keypair file is not available on this node.
    active_pubkey: 111111ActivePubkey1111111111111111111111111

    # required: true (or set passive_pubkey)
    # Path to the passive keypair file — unique per peer.
    # Takes precedence over passive_pubkey if both are set.
    passive: "/path/to/passive-identity.json"

    # required: true (or set passive)
    # Base58-encoded passive pubkey. Used when the keypair file is not available on this node.
    passive_pubkey: 111111PassivePubkey1111111111111111111111111

prometheus:

  # required: false | default: 9090
  # Port to serve Prometheus metrics on /metrics
  port: 9090

  # required: false | default: 9091
  # Port to serve the health check endpoint on /health
  health_check_port: 9091

  # required: false
  # Static key:value labels attached to all exposed metrics
  static_labels:
    brand: ha-validators
    cluster: mainnet-beta
    region: ha-region-1

cluster:

  # required: true
  # Solana cluster this validator is running on. One of: mainnet-beta, devnet, testnet
  name: "mainnet-beta"

  # required: false | default: cluster default RPC URL for cluster.name
  # RPC URLs used to query gossip state. Supplying multiple URLs provides resilience
  # against individual RPC drop-outs. URLs that return 403/429/503 are automatically
  # deprioritised for 60 s before being retried.
  # The local validator RPC URL may be included here (logs a warning) and acts as a
  # rate-limit-immune fallback. For sub-second peer detection add it alongside at least
  # one remote URL and configure HA peers as mutual --entrypoint flags in agave.
  # See "Using Local RPC as a Failsafe Fallback" below for details.
  rpc_urls: []

failover:

  # required: false | default: false
  # When true, log failover commands instead of running them — useful for testing config.
  dry_run: false

  # required: false | default: 5s
  # How often to refresh gossip state and evaluate failover decisions.
  poll_interval_duration: 5s

  # required: false | default: 3  (~15s at the default 5s poll interval)
  # How many consecutive gossip samples without an active, non-delinquent voting peer
  # before the cluster is considered leaderless and a failover is triggered.
  leaderless_samples_threshold: 3

  # Overrides the number of slots a peer must be behind the tip to be considered delinquent.
  # ⚠️ Set with caution — too low a value causes false positives on transient hiccups.
  # The Agave default is 128 slots (~51s), defined as DELINQUENT_VALIDATOR_SLOT_DISTANCE:
  #   https://github.com/anza-xyz/agave/blob/master/rpc-client-types/src/request.rs
  # Since Agave v2.0, --health-check-slot-distance also defaults to 128 via the same constant:
  #   https://github.com/anza-xyz/agave/blob/master/validator/src/commands/run/args/json_rpc_config.rs
  # Both thresholds agree — there is no gap between delinquency detection and health status.
  # If you set value below 128, add --health-check-slot-distance <value> to your validator
  # startup flags to keep the thresholds aligned — a startup warning will remind you if not.
  # Values <= 1 are clamped to 2 on startup.
  delinquent_slot_distance_override:

    # required: false | default: false
    enabled: false

    # required: false | default: 128
    # Slots behind the tip at which a peer is considered delinquent (when enabled: true).
    value: 128

  # Guards against startup health flaps: a validator that briefly reports healthy during
  # startup before falling behind and going unhealthy again.
  self_healthy:

    # required: false | default: 30s
    # How long the local validator RPC must continuously report healthy before this
    # node is eligible to become active in a failover.
    minimum_duration: 30s

    # required: false | default: 2s
    # How often to sample local RPC health. Runs independently of poll_interval_duration
    # so the healthy streak timer is not skewed by gossip refresh latency.
    poll_interval_duration: 2s

  # required: true | min: 1
  # Map of HA peers, excluding this node — it is added automatically at startup.
  # Keys are vanity names used in logging and metrics. IPs must be valid, unique IPv4 addresses.
  peers:
    backup-validator-1:
      ip: 192.168.1.11
    backup-validator-2:
      ip: 192.168.1.12

  # required: false
  # Explicit failover priority for this node. Lower value = higher priority (takes over first).
  # When set, every peer in failover.peers must also declare a priority, and all values must be
  # unique and non-negative. If omitted (default), peers are ranked by ascending IP address.
  # All nodes in the HA cluster should declare the same priority ordering to avoid races.
  # See "Failover Priority" below for details.
  priority: 0

  # required: true
  # Commands and hooks to run when this node should become active.
  # command, args, and env values support Go template strings:
  #   {{ .ActiveIdentityKeypairFile }}  — absolute path to validator.identities.active
  #   {{ .PassiveIdentityKeypairFile }} — absolute path to validator.identities.passive
  #   {{ .ActiveIdentityPubkey }}       — active pubkey string
  #   {{ .PassiveIdentityPubkey }}      — passive pubkey string
  #   {{ .SelfName }}                   — value of validator.name
  active:

    # required: true
    command: set-identity-with-rollback.sh

    # required: false
    env:
      CUSTOM_ENV_VAR: "{{ .ActiveIdentityPubkey }}"

    # required: false
    args: [
      "active",
      "--active-identity-file",  "{{ .ActiveIdentityKeypairFile }}",
      "--passive-identity-file", "{{ .PassiveIdentityKeypairFile }}",
    ]

    # required: false
    # Hooks run in declaration order. A pre-hook with must_succeed: true aborts
    # subsequent hooks and skips the active command if it fails.
    hooks:
      pre:
        - name: notify-slack-promoting
          command: /home/solana/solana-validator-ha/hooks/pre-active/send-slack-alert.sh
          must_succeed: false
          env: {}
          args: [
            "--channel", "#save-my-bacon",
            "--message", "solana-validator-ha promoting {{ .SelfName }} to active ({{ .PassiveIdentityPubkey }} -> {{ .ActiveIdentityPubkey }})"
          ]

      post:
        - name: notify-slack-promoted
          command: /home/solana/solana-validator-ha/hooks/post-active/send-slack-alert.sh
          env: {}
          args: [
            "--channel", "#saved-my-bacon",
            "--message", "solana-validator-ha promoted {{ .SelfName }} to active with identity {{ .ActiveIdentityPubkey }}"
          ]

  # required: true
  # Commands and hooks to run when this node should become passive.
  # Supports the same template variables as active above.
  # This command must be idempotent — it may be called multiple times in succession.
  # post-passive hooks only run if the passive command succeeds.
  passive:

    # required: true
    command: seppuku.sh

    # required: false
    args: [
      "--passive-identity-file", "{{ .PassiveIdentityKeypairFile }}",
      "--stop-service-on-identity-set-failure",
      "--wait-for-and-force-identity-on-service-starting-up",
    ]

    # required: false
    hooks:
      pre:
        - name: notify-slack-demoting
          command: /home/solana/solana-validator-ha/hooks/pre-passive/send-slack-alert.sh
          must_succeed: false
          args: [
            "--channel", "#oh-shit-wake-people-up",
            "--message", "solana-validator-ha demoting {{ .SelfName }} to passive ({{ .ActiveIdentityPubkey }} -> {{ .PassiveIdentityPubkey }})"
          ]

      post:
        - name: notify-slack-demoted
          command: /home/solana/solana-validator-ha/hooks/post-passive/send-slack-alert.sh
          args: [
            "--channel", "#postmortem-shelf",
            "--message", "solana-validator-ha demoted {{ .SelfName }} to passive with identity {{ .PassiveIdentityPubkey }}"
          ]

update:

  # required: false | default: true
  # Set to false to disable all update checks (startup and periodic).
  check_enabled: true

  # required: false | default: 24h
  # How often to check for a new release when running in continuous mode.
  check_interval_duration: 24h
```

### Using Local RPC as a Failsafe Fallback

Public RPC endpoints can rate-limit or block requests (`403 Forbidden`, `429 Too Many Requests`), which prevents `getClusterNodes` from returning gossip data and causes log noise like:

```
ERRO [gossip_state]: failed to get cluster nodes
  error= method call failed on all RPC endpoints method: GetClusterNodes,
         attempted_urls: [https://api.mainnet-beta.solana.com],
         errors: [403 "Access forbidden"]
```

Adding the local validator RPC alongside your remote URLs provides a fallback that is always available and never rate-limited:

```yaml
cluster:
  name: "mainnet-beta"
  rpc_urls:
    - "https://api.mainnet-beta.solana.com"  # remote — may rate-limit
    - "http://localhost:8899"                # local — immune to rate limits
```

A warning is logged at startup to remind you of the trade-off:

```
WARN config: cluster.rpc_urls contains the local validator RPC URL (http://localhost:8899)
     — ensure HA peers are configured as mutual --entrypoint flags for direct gossip,
       otherwise peer state may be stale
```

#### When the local RPC is ideal: mutual `--entrypoint` flags

When each HA peer is listed as an `--entrypoint` in the other peers' agave config, they exchange gossip directly over UDP (CRDS). The local RPC then has sub-second fresh data for every peer — better than remote RPCs that see gossip after network propagation.

```
# On validator-1 (185.26.11.91)
--entrypoint validator-2.example.com:8001

# On validator-2
--entrypoint validator-1.example.com:8001
```

#### When the local RPC is a last-resort fallback

Without mutual `--entrypoint` flags, the local validator learns about peers through the wider gossip network. Peer data may be 30–60 s stale. This is still better than a complete `getClusterNodes` failure: the system keeps working with slightly older data rather than losing all visibility. The multi-URL retry logic means the remote URLs are always tried first; the local RPC is only reached if all remote calls fail.

#### Automatic URL cooldown

When any URL (local or remote) returns a `403`, `429`, or `503` response, `solana-validator-ha` automatically deprioritises that URL for 60 s and logs a warning:

```
WARN [rpc_client]: RPC endpoint rate-limited or access forbidden, cooling down
     method=GetClusterNodes url=https://api.mainnet-beta.solana.com cooldown=1m0s
```

During the cooldown the URL is still retried as a last resort, but healthy URLs are always attempted first.

## Failover Priority

By default, when multiple passive nodes are all eligible to take over, they use their public IP addresses (ascending sort) to break the tie. The node with the lowest IP gets rank 0 and takes over immediately; higher-ranked nodes wait `rank × poll_interval_duration` before attempting takeover.

Operators can override this ordering with explicit priorities:

```yaml
# On the node that should take over first (highest priority):
failover:
  priority: 0
  peers:
    backup-validator-1:
      ip: 192.168.1.11
      priority: 1
    backup-validator-2:
      ip: 192.168.1.12
      priority: 2

# On backup-validator-1:
failover:
  priority: 1
  peers:
    primary-validator:
      ip: 192.168.1.10
      priority: 0
    backup-validator-2:
      ip: 192.168.1.12
      priority: 2
```

**Rules:**

- Lower `priority` value = higher priority (rank 0 = takes over immediately).
- If any node declares a `priority`, **all** nodes (self + every entry in `failover.peers`) must declare one. A partially-configured cluster is rejected at startup.
- Priority values must be unique across self and all peers. Duplicates are rejected at startup.
- All nodes in the cluster should agree on the same priority ordering to avoid races. `solana-validator-ha` cannot enforce cross-node consistency, but logs the effective rank and ranking source (`config priority` vs `IP address`) at takeover time so mismatches are easy to spot.

## Development and testing

```bash
make dev
make test
```

## Monitoring & Metrics

The application exposes Prometheus metrics on the configured port (default: 9090) and a health check endpoint on a separate configurable port (default: 9091):

### Core Metrics
- **`solana_validator_ha_metadata`**: Validator metadata with role and status labels
- **`solana_validator_ha_peer_count`**: Number of peers visible in gossip
- **`solana_validator_ha_self_in_gossip`**: Whether this validator appears in gossip (1=yes, 0=no)
- **`solana_validator_ha_failover_status`**: Current failover status
- **`solana_validator_ha_update_available`**: Whether a newer release is available (1=yes, 0=no). Updated on startup and periodically per `update.check_interval_duration`

### Metric Labels
- `validator_name`: Configured validator name
- `public_ip`: Validator's public IP address
- `validator_role`: Current role (active/passive/unknown)
- `validator_status`: Health status (healthy/unhealthy)
- Plus any configured static labels

### Health Endpoints
- **`/metrics`**: Prometheus metrics (on `prometheus.port`, default: 9090)
- **`/health`**: Basic health check (on `prometheus.health_check_port`, default: 9091)

## License

This project is licensed under the MIT License - see the LICENSE file for details.
