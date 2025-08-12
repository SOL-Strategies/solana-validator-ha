#!/bin/bash
curl -s "http://localhost:8989/solana-network-rpc/set-gossip-state?validator=validator-$1&online=$2"
