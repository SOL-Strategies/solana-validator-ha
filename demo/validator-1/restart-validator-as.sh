#!/bin/bash
# set identity to passive and bring it back online
../set-identity.sh 1 $1 && sleep 20 && ../set-gossip-state.sh 1 true
