#!/bin/bash
echo "Setting validator-$1 to $2"
echo "oops stderr msg 😉" >&2
curl -s http://localhost:8989/validator-$1/set-identity/$2
echo "Done"
