#!/bin/bash

set -e

echo "🔑 Setting up test identity files..."

# Create test-files directory if it doesn't exist
mkdir -p ./test-files

# Create a simple Go program to generate proper solana keygen files
cat > ./test-files/generate-keys.go << 'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/gagliardetto/solana-go"
)

func main() {
	// Generate shared active keypair
	activeKeypair := solana.NewWallet()
	activeBytes := activeKeypair.PrivateKey
	activeArray := make([]int, len(activeBytes))
	for i, b := range activeBytes {
		activeArray[i] = int(b)
	}
	
	// Write active keypair
	activeData, _ := json.Marshal(activeArray)
	os.WriteFile("active-identity.json", activeData, 0644)
	
	// Generate different passive keypairs for each validator
	for i := 1; i <= 3; i++ {
		passiveKeypair := solana.NewWallet()
		passiveBytes := passiveKeypair.PrivateKey
		passiveArray := make([]int, len(passiveBytes))
		for j, b := range passiveBytes {
			passiveArray[j] = int(b)
		}
		
		// Write passive keypair
		passiveData, _ := json.Marshal(passiveArray)
		filename := fmt.Sprintf("passive-identity-%d.json", i)
		os.WriteFile(filename, passiveData, 0644)
	}
	
	fmt.Println("✅ Generated keypairs:")
	fmt.Printf("  Active public key: %s\n", activeKeypair.PublicKey().String())
	for i := 1; i <= 3; i++ {
		passiveKeypair := solana.NewWallet()
		fmt.Printf("  Passive-%d public key: %s\n", i, passiveKeypair.PublicKey().String())
	}
}
EOF

# Run the Go program to generate proper keypairs
cd ./test-files
go mod init test-keys
go mod tidy
go run generate-keys.go

# Clean up the Go files
rm -f generate-keys.go go.mod go.sum

echo "✅ Test identity files created successfully!"
echo "  - active-identity.json (shared by all validators)"
echo "  - passive-identity-1.json (validator-1 passive)"
echo "  - passive-identity-2.json (validator-2 passive)"
echo "  - passive-identity-3.json (validator-3 passive)"

# Set proper permissions
chmod 644 ./test-files/*.json

echo "📁 Test files are ready for integration testing" 