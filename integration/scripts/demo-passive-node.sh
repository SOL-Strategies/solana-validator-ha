#!/bin/bash
# demo-passive-node.sh
# Runs validator-2 (passive) directly so VHS captures its terminal output.
# Shows the passive node detecting a leaderless cluster and promoting itself to active.
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
# validator-2 detects the leaderless cluster and promotes itself to active.
(sleep 12 && \
 curl -sf -X POST -H "Content-Type: application/json" \
     -d '{"action":"disconnect","target":"validator-1"}' \
     "$MOCK_URL/action" >/dev/null) &
disown $!

# Exec the HA binary — replaces this shell so VHS records its output directly.
exec ./bin/solana-validator-ha run --config integration/configs/demo-validator-2.yaml
