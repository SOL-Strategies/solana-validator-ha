# passive-node.tape
# VHS tape — records validator-2 (passive) detecting a leaderless cluster and promoting itself.
# Output: docs/passive-node.gif
#
# Prerequisites (run once before recording):
#   make build          # build the binary
#   make demo           # start mock-solana on localhost:8899

Output docs/passive-node.gif

Set FontSize 13
Set Width 1600
Set Height 500
Set Theme "Catppuccin Mocha"
Set PlaybackSpeed 2

Sleep 300ms

Hide
Type "bash integration/scripts/demo-passive-node.sh"
Enter
Sleep 1s
Show

# Timeline (real time):
#   0–5s   binary starts, discovers peers, reports healthy streak begins
#   5s     self_healthy minimum_duration reached (eligible for failover)
#   12s    validator-1 disconnected from mock gossip
#   12–21s leaderless samples accumulate (3 × 3s poll)
#   21s+   validator-2 promotes itself to active
# PlaybackSpeed 2 halves the GIF duration.
Sleep 40s
