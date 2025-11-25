# solana-validator-ha

A gossip-based high availability (HA) manager for Solana validators.

![solana-validator-ha](demo/e2e/preview.png)

## Demo

Automatic failover resulting from loss of `active (voting)` leader.

**`primary (active)`** disconnects and is ensured to be `passive`

![primary-lost](demo/e2e/primary.gif)

**`backup (passive)`** detects loss of leader and becomes `active`

![backup-active](demo/e2e/backup.gif)

## Features

- **🔍 Intelligent Peer Detection**: Automatically detects validator roles based on network gossip and RPC identity
- **🛡️ Self-Healing**: Validators transition between active/passive roles based on health and network visibility
- **🪝 Hooks**: Pre/Post failover hook support for role transitions
- **📊 Prometheus Metrics**: Rich metrics collection for monitoring and alerting
- **🏁 First-Responder Failover**: Race-based failover where fastest healthy, passive validator assumes active role when the cluster is leaderless

### Conceptual overview

`solana-validator-ha` aims to provide a simple, low-dependency HA solution to running 2 or more related validators where one of these should be an `active (voting)` leader with the others remaining `passive (non-voting)`. The set of validators each have a unique `passive` identity and a shared `active` identity. The program discovers validators' HA peers using the existing gossip protocol and each peer makes independent failover decisions when no active peer is discovered.

To give the best chance of success when things turn to 💩 two (‼️**VERY**‼️) important user-supplied configuration settings are required:

#### 1. 🟢 Active command

A command to run for a node to assume the `active` role. This is simply a reference to a user-supplied command that will be called on the current node when a failover is required and:

  1. The node is healthy (so that it can take over as leader);
  1. The node is discoverable and reachable on its gossip-advertised port; and
  3. No other peers have already assumed the `active` role. 
   
   ```yaml
      # solana-validator-ha-config.yaml
      #...
      failover:
       active:
         command: "set-identity-with-rollback.sh" # user-supplied command -everyone's setup is different :-)
         args: [
           "--to-identity-file", "{{ .PassiveIdentityKeypairFile }}",
           "--rollback-identity-file", "{{ .PassiveIdentityKeypairFile }}",
         ]
      #...
   ```

#### 2. 🔴 Passive command

A command to run to assume a `passive` role (a.k.a _Seppukku_). This is simply a reference to an **idempotent** user-supplied command that ensures the validator is set to `passive`. An validator that detects itself as disconnected from the Solana network will call this command to ensure it is `passive`. Operators may find it safest to configure validators to always start with a `passive` identity so that this command simply restarts the validator service and waits for it to report healthy. Something along the lines of:

   ```yaml
      #...
      failover:
       passive:
         # ⚠️ Everyone's setup is different, but this command should make damn sure the validator goes passive.
         # ⚠️ If set-identity fails, restart/stop the validator service, or pull the plug, or call your mum crying for help.
         # ⚠️ Do whatever you need to ensure this validator doesn't come back as active
         command: "seppukku.sh" # user-supplied command
         args: [
           "--passive-identity-file", "{{ .PassiveIdentityKeypairFile }}",
         ]
      #...
   ```

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

See [example-scripts/ha-set-role.sh](example-scripts/ha-set-role.sh) for an example failover script to set role `active|passive`.

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

  # leaderless_samples_threshold
  # required: false
  # default: 3 - (at least) 15s with poll_interval_duration at default of 5s
  # description:
  #   Number of gossip samples to allow without a leader (active, voting node) before considering the validator cluster leaderless
  #   and thus triggering a failover. A node running on an identity with a delinquent vote account is not consiodered to be a leader.
  leaderless_samples_threshold: 3

  # takeover_jitter_duration
  # required: false
  # default: 3s
  # description:
  #   A Go duration string for a random jitter delay to add to a passive peer before taking over as active. This is to safeguard against race conditions where
  #  two or more passive validators attempt to take over as passive at the same time. A warning will be issued if set below 1s as this may void the usefulness of jitter.
  takeover_jitter_duration: 3s

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

## Development and testing

```bash
make dev
make test
```

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

## License

This project is licensed under the MIT License - see the LICENSE file for details.
