#!/bin/bash
cd validator-$1
../../bin/solana-validator-ha run -c config.yaml run
