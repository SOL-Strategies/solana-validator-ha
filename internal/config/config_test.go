package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	// Test with default parameters
	cfg, err := New(NewConfigParams{})
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.logger)

	// Test with custom GetPublicIPFunc
	customFunc := func() (string, error) { return "192.168.1.1", nil }
	cfg, err = New(NewConfigParams{GetPublicIPFunc: customFunc})
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.GetPublicIPFunc)
}

func TestLoadFromFile(t *testing.T) {
	// Create a temporary config file
	tempFile := createTempConfigFile(t)
	defer os.Remove(tempFile)

	cfg, err := New(NewConfigParams{})
	require.NoError(t, err)

	err = cfg.LoadFromFile(tempFile)
	require.NoError(t, err)
	assert.Equal(t, tempFile, cfg.File)
	assert.Equal(t, "test-validator", cfg.Validator.Name)
	assert.Equal(t, "http://localhost:8899", cfg.Validator.RPCURL)
}

func TestNewFromConfigFile(t *testing.T) {
	// Create a temporary config file
	tempFile := createTempConfigFile(t)
	defer os.Remove(tempFile)

	cfg, err := NewFromConfigFile(tempFile)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Equal(t, tempFile, cfg.File)
}

func TestInitialize(t *testing.T) {
	// Create a temporary config file with identity files
	tempFile := createTempConfigFileWithIdentities(t)
	defer os.Remove(tempFile)

	cfg, err := New(NewConfigParams{})
	require.NoError(t, err)

	err = cfg.LoadFromFile(tempFile)
	require.NoError(t, err)

	err = cfg.Initialize()
	require.NoError(t, err)
}

func TestSetDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()

	// Check that defaults are set
	assert.Equal(t, "http://localhost:8899", cfg.Validator.RPCURL)
	assert.Equal(t, 9090, cfg.Prometheus.Port)
}

func TestValidate(t *testing.T) {
	// Test with valid config (without identities to avoid loading files)
	cfg := &Config{
		Validator: Validator{
			Name:   "test-validator",
			RPCURL: "http://localhost:8899",
		},
		Cluster: Cluster{
			Name:    "testnet",
			RPCURLs: []string{"https://api.testnet.solana.com"},
		},
		Prometheus: Prometheus{
			Port: 9090,
		},
		Failover: Failover{
			DryRun:                     false,
			PollIntervalDuration:       30 * time.Second,
			LeaderlessSamplesThreshold: 10,
			TakeoverJitterSeconds:      10,
			Active: Role{
				Command: "systemctl start solana",
			},
			Passive: Role{
				Command: "systemctl stop solana",
			},
			Peers: Peers{
				"validator-1": {IP: "192.168.1.10"},
			},
		},
	}

	// Set defaults before validation (as done in Initialize)
	cfg.setDefaults()

	err := cfg.validate()
	assert.NoError(t, err)

	// Test with invalid validator name
	cfg.Validator.Name = ""
	err = cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.name must be defined")

	// Test with invalid RPC URL
	cfg.Validator.Name = "test-validator"
	cfg.Validator.RPCURL = "invalid-url"
	err = cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validator.rpc_url must be a valid URL")
}

func createTempConfigFile(t *testing.T) string {
	// Create temporary identity files
	activeIdentityFile := createTempIdentityFile(t)
	passiveIdentityFile := createTempIdentityFile(t)

	// Clean up identity files after test
	t.Cleanup(func() {
		os.Remove(activeIdentityFile)
		os.Remove(passiveIdentityFile)
	})

	content := `
validator:
  name: "test-validator"
  rpc_url: "http://localhost:8899"
  identities:
    active: "` + activeIdentityFile + `"
    passive: "` + passiveIdentityFile + `"

cluster:
  name: "testnet"
  rpc_urls:
    - "https://api.testnet.solana.com"

prometheus:
  port: 9090
  static_labels:
    environment: "test"

failover:
  dry_run: true
  poll_interval_duration: "30s"
  leaderless_threshold_duration: "5m"
  takeover_jitter_seconds: 10
  active:
    command: "systemctl start solana"
  passive:
    command: "systemctl stop solana"
  peers:
    validator-1:
      ip: "192.168.1.10"
    validator-2:
      ip: "192.168.1.11"
`

	tempFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)

	_, err = tempFile.WriteString(content)
	require.NoError(t, err)

	err = tempFile.Close()
	require.NoError(t, err)

	return tempFile.Name()
}

func createTempConfigFileWithIdentities(t *testing.T) string {
	// Create temporary identity files
	activeIdentityFile := createTempIdentityFile(t)
	passiveIdentityFile := createTempIdentityFile(t)

	// Clean up identity files after test
	t.Cleanup(func() {
		os.Remove(activeIdentityFile)
		os.Remove(passiveIdentityFile)
	})

	content := `
validator:
  name: "test-validator"
  rpc_url: "http://localhost:8899"
  identities:
    active: "` + activeIdentityFile + `"
    passive: "` + passiveIdentityFile + `"

cluster:
  name: "testnet"
  rpc_urls:
    - "https://api.testnet.solana.com"

prometheus:
  port: 9090
  static_labels:
    environment: "test"

failover:
  dry_run: true
  poll_interval_duration: "30s"
  leaderless_threshold_duration: "5m"
  takeover_delay_duration: "10s"
  active:
    command: "systemctl start solana"
  passive:
    command: "systemctl stop solana"
  peers:
    validator-1:
      ip: "192.168.1.10"
    validator-2:
      ip: "192.168.1.11"
`

	tempFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)

	_, err = tempFile.WriteString(content)
	require.NoError(t, err)

	err = tempFile.Close()
	require.NoError(t, err)

	return tempFile.Name()
}
