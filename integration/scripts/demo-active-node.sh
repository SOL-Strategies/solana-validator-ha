#!/bin/bash
# demo-active-node.sh
# Runs validator-1 (active) directly so VHS captures its terminal output.
# Shows the active node detecting it has dropped from gossip and stepping down to passive.
#
# Requires: make demo (mock-solana running on localhost:8899) and make build.

set -euo pipefail

# Always run from the project root regardless of how this script is invoked.
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../.."

MOCK_URL="http://localhost:8899"

# Reset: validator-1 starts as active.
curl -sf -X POST -H "Content-Type: application/json" \
    -d '{"action":"reset","target":"validator-1"}' \
    "$MOCK_URL/action" >/dev/null

# Disconnect validator-1 after 12s (background — simulates network loss).
# The HA manager detects it has dropped from gossip and runs the passive (seppuku) command.
(sleep 12 && \
 curl -sf -X POST -H "Content-Type: application/json" \
     -d '{"action":"disconnect","target":"validator-1"}' \
     "$MOCK_URL/action" >/dev/null) &
disown $!

# Exec the HA binary — replaces this shell so VHS records its output directly.
exec ./bin/solana-validator-ha run --config integration/configs/demo-validator-1.yaml
