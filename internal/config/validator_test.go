package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidator_SetDefaults(t *testing.T) {
	validator := &Validator{}
	validator.SetDefaults()

	assert.Equal(t, "http://localhost:8899", validator.RPCURL)
}

func TestValidator_Validate(t *testing.T) {
	// Test with valid validator
	validator := &Validator{
		Name:   "test-validator",
		RPCURL: "http://localhost:8899",
	}

	err := validator.Validate()
	assert.NoError(t, err)

	// Test with empty name
	validator.Name = ""
	err = validator.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.name must be defined")

	// Test with empty RPC URL
	validator.Name = "test-validator"
	validator.RPCURL = ""
	err = validator.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.rpc_url must be a valid URL")

	// Test with invalid RPC URL
	validator.RPCURL = "invalid-url"
	err = validator.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.rpc_url must be a valid URL")

	// Test with valid URL
	validator.RPCURL = "https://api.testnet.solana.com"
	err = validator.Validate()
	assert.NoError(t, err)
}

func TestValidatorIdentities_Load(t *testing.T) {
	// Create temporary identity files
	activeIdentityFile := createTempIdentityFile(t)
	passiveIdentityFile := createTempIdentityFile(t)

	// Clean up identity files after test
	t.Cleanup(func() {
		os.Remove(activeIdentityFile)
		os.Remove(passiveIdentityFile)
	})

	// Test loading from temporary identity files
	identities := &ValidatorIdentities{
		ActiveKeyPairFile:  activeIdentityFile,
		PassiveKeyPairFile: passiveIdentityFile,
	}

	err := identities.Load()
	require.NoError(t, err)

	assert.NotNil(t, identities.ActiveKeyPair)
	assert.NotNil(t, identities.PassiveKeyPair)
	assert.NotEqual(t, identities.ActiveKeyPair.PublicKey().String(), identities.PassiveKeyPair.PublicKey().String())
}

func TestValidatorIdentities_Validate(t *testing.T) {
	// Create temporary identity files
	activeIdentityFile := createTempIdentityFile(t)
	passiveIdentityFile := createTempIdentityFile(t)

	// Clean up identity files after test
	t.Cleanup(func() {
		os.Remove(activeIdentityFile)
		os.Remove(passiveIdentityFile)
	})

	// Load identities first
	identities := &ValidatorIdentities{
		ActiveKeyPairFile:  activeIdentityFile,
		PassiveKeyPairFile: passiveIdentityFile,
	}

	err := identities.Load()
	require.NoError(t, err)

	// Test with different identities
	err = identities.Validate()
	assert.NoError(t, err)

	// Test with same identities (should fail)
	identities.PassiveKeyPair = identities.ActiveKeyPair
	err = identities.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.identities.active and validator.identities.passive must be different")
}
