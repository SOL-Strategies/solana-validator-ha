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
		Identities: ValidatorIdentities{
			PassivePubkeyStr: "SysvarC1ock11111111111111111111111111111111",
		},
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

	// Test with legacy top-level active identity
	validator.Identities.ActivePubkeyStr = "11111111111111111111111111111111"
	err = validator.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.identities.active is no longer supported")
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

func TestValidatorIdentities_LoadPubkeyOnly(t *testing.T) {
	// Use valid 32-byte Solana public keys (base58-encoded)
	activePubkey := "11111111111111111111111111111111"
	passivePubkey := "SysvarC1ock11111111111111111111111111111111"

	// Test active pubkey-only + passive pubkey-only
	identities := &ValidatorIdentities{
		ActivePubkeyStr:  activePubkey,
		PassivePubkeyStr: passivePubkey,
	}

	err := identities.Load()
	require.NoError(t, err)

	assert.Nil(t, identities.ActiveKeyPair)
	assert.Nil(t, identities.PassiveKeyPair)
	assert.Equal(t, activePubkey, identities.ActivePubkey())
	assert.Equal(t, passivePubkey, identities.PassivePubkey())

	// Test active pubkey-only + passive keypair file
	passiveIdentityFile := createTempIdentityFile(t)
	t.Cleanup(func() { os.Remove(passiveIdentityFile) })

	identities2 := &ValidatorIdentities{
		ActivePubkeyStr:    activePubkey,
		PassiveKeyPairFile: passiveIdentityFile,
	}

	err = identities2.Load()
	require.NoError(t, err)

	assert.Nil(t, identities2.ActiveKeyPair)
	assert.NotNil(t, identities2.PassiveKeyPair)
	assert.Equal(t, activePubkey, identities2.ActivePubkey())
	assert.NotEmpty(t, identities2.PassivePubkey())

	// Test active keypair file + passive pubkey-only
	activeIdentityFile := createTempIdentityFile(t)
	t.Cleanup(func() { os.Remove(activeIdentityFile) })

	identities3 := &ValidatorIdentities{
		ActiveKeyPairFile: activeIdentityFile,
		PassivePubkeyStr:  passivePubkey,
	}

	err = identities3.Load()
	require.NoError(t, err)

	assert.NotNil(t, identities3.ActiveKeyPair)
	assert.Nil(t, identities3.PassiveKeyPair)
	assert.NotEmpty(t, identities3.ActivePubkey())
	assert.Equal(t, passivePubkey, identities3.PassivePubkey())

	// Test invalid active pubkey
	identities4 := &ValidatorIdentities{
		ActivePubkeyStr:  "not-a-valid-base58",
		PassivePubkeyStr: passivePubkey,
	}

	err = identities4.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse active_pubkey")

	// Test invalid passive pubkey
	identities5 := &ValidatorIdentities{
		ActivePubkeyStr:  activePubkey,
		PassivePubkeyStr: "not-a-valid-base58",
	}

	err = identities5.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse passive_pubkey")

	// Test neither active keypair nor pubkey
	identities6 := &ValidatorIdentities{
		PassivePubkeyStr: passivePubkey,
	}

	err = identities6.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either validator.identities.active")

	// Test neither passive keypair nor pubkey
	identities7 := &ValidatorIdentities{
		ActivePubkeyStr: activePubkey,
	}

	err = identities7.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either validator.identities.passive")
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
	identities.PassivePubkeyStr = identities.ActivePubkeyStr
	err = identities.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.identities.active and validator.identities.passive must be different")

	// Test with pubkey-only same identities (should fail)
	identitiesPubkey := &ValidatorIdentities{
		ActivePubkeyStr:  "SysvarC1ock11111111111111111111111111111111",
		PassivePubkeyStr: "SysvarC1ock11111111111111111111111111111111",
	}
	err = identitiesPubkey.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.identities.active and validator.identities.passive must be different")
}
