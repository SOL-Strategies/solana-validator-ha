Output ./demo.gif

Set Shell "zsh"
Set FontSize 12

# Setup
Hide
Type "source ../.demo-aliases" Enter
Sleep 1s
Type "clear" Enter
Sleep 1s
Show

# Demo

# start ha manager
Type "solana-validator-ha -c config.yaml run" Sleep 500ms Enter

# out of band, call the mock server API endpoint to knock the validtor out of the network
Sleep 60s

Hide
Type "Ctrl+C" Enter
Sleep 1s
Type "exit" Enter
Show
