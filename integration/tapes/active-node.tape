# active-node.tape
# VHS tape — records validator-1 (active) detecting it has dropped from gossip
# and stepping down to passive so a peer can take over.
# Output: docs/active-node.gif
#
# Prerequisites (run once before recording):
#   make build          # build the binary
#   make demo           # start mock-solana on localhost:8899

Output docs/active-node.gif

Set FontSize 13
Set Width 1600
Set Height 500
Set Theme "Catppuccin Mocha"
Set PlaybackSpeed 2

Sleep 300ms

Hide
Type "bash integration/scripts/demo-active-node.sh"
Enter
Show

# Timeline (real time):
#   0–5s   binary starts as active, peers visible in gossip
#   5s     self_healthy minimum_duration reached
#   12s    validator-1 disconnected from mock gossip (simulates network loss)
#   12–15s HA manager detects self not in gossip, runs passive (seppuku) command
# PlaybackSpeed 2 halves the GIF duration.
Sleep 30s
