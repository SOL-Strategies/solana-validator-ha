package config

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	solanago "github.com/gagliardetto/solana-go"
)

// createTempIdentityFile creates a temporary identity file for testing
func createTempIdentityFile(t *testing.T) string {
	// Generate a new keypair
	keypair := solanago.NewWallet()
	
	// Create temporary file
	tempFile, err := os.CreateTemp("", "identity-*.json")
	require.NoError(t, err)
	defer tempFile.Close()

	// Write the keypair to the file in solana keygen format
	// The keygen format is a JSON array with the private key bytes
	keyBytes := keypair.PrivateKey
	keyArray := make([]int, len(keyBytes))
	for i, b := range keyBytes {
		keyArray[i] = int(b)
	}
	
	// Write as JSON array
	jsonData := fmt.Sprintf("[%s]", strings.Trim(strings.Replace(fmt.Sprint(keyArray), " ", ",", -1), "[]"))
	_, err = tempFile.WriteString(jsonData)
	require.NoError(t, err)

	return tempFile.Name()
} 