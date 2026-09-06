package config

import (
	"testing"
	"time"

	solanagorpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/assert"
)

func TestCluster_SetDefaults(t *testing.T) {
	// Test mainnet-beta defaults
	cluster := &Cluster{Name: solanagorpc.MainNetBeta.Name}
	cluster.SetDefaults()
	assert.Equal(t, []string{solanagorpc.MainNetBeta.RPC}, cluster.RPCURLs)

	// Test testnet defaults
	cluster = &Cluster{Name: solanagorpc.TestNet.Name}
	cluster.SetDefaults()
	assert.Equal(t, []string{solanagorpc.TestNet.RPC}, cluster.RPCURLs)

	// Test devnet defaults
	cluster = &Cluster{Name: solanagorpc.DevNet.Name}
	cluster.SetDefaults()
	assert.Equal(t, []string{solanagorpc.DevNet.RPC}, cluster.RPCURLs)

	// Test with custom RPC URLs (should not override)
	customURLs := []string{"https://custom-rpc.com"}
	cluster = &Cluster{
		Name:    solanagorpc.MainNetBeta.Name,
		RPCURLs: customURLs,
	}
	cluster.SetDefaults()
	assert.Equal(t, customURLs, cluster.RPCURLs)

	// RPC timeout and cooldown defaults
	cluster = &Cluster{Name: solanagorpc.MainNetBeta.Name}
	cluster.SetDefaults()
	assert.Equal(t, 5*time.Second, cluster.RPCTimeoutDuration)
	assert.Equal(t, 60*time.Second, cluster.RPCURLCooldownDuration)

	// Pre-set values should not be overridden
	cluster = &Cluster{
		Name:                   solanagorpc.MainNetBeta.Name,
		RPCTimeoutDuration:     5 * time.Second,
		RPCURLCooldownDuration: 30 * time.Second,
	}
	cluster.SetDefaults()
	assert.Equal(t, 5*time.Second, cluster.RPCTimeoutDuration)
	assert.Equal(t, 30*time.Second, cluster.RPCURLCooldownDuration)
}

// validCluster returns a minimal valid Cluster with all required fields set.
func validCluster(name string) *Cluster {
	return &Cluster{
		Name:                   name,
		RPCURLs:                []string{"https://api.testnet.solana.com"},
		RPCTimeoutDuration:     5 * time.Second,
		RPCURLCooldownDuration: 60 * time.Second,
	}
}

func TestCluster_Validate(t *testing.T) {
	// Test with valid cluster names and RPC URLs
	validClusters := []string{
		solanagorpc.MainNetBeta.Name,
		solanagorpc.TestNet.Name,
		solanagorpc.DevNet.Name,
	}

	for _, clusterName := range validClusters {
		err := validCluster(clusterName).Validate()
		assert.NoError(t, err, "Cluster name %s should be valid", clusterName)
	}

	// Any non-empty name is valid (including custom/alpenglow clusters)
	err := validCluster("alpenglow").Validate()
	assert.NoError(t, err)
	err = validCluster("my-private-cluster").Validate()
	assert.NoError(t, err)

	// Empty name is not valid
	cluster := validCluster("")
	err = cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.name must not be empty")

	// Test with empty RPC URLs
	cluster = validCluster(solanagorpc.TestNet.Name)
	cluster.RPCURLs = []string{}
	err = cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.rpc_urls must be a non-empty list")

	// Test with invalid RPC URL
	cluster = validCluster(solanagorpc.TestNet.Name)
	cluster.RPCURLs = []string{"invalid-url"}
	err = cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.rpc_urls must be a list of valid RPC URLs")

	// Test with zero RPC timeout
	cluster = validCluster(solanagorpc.TestNet.Name)
	cluster.RPCTimeoutDuration = 0
	err = cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.rpc_timeout_duration must be greater than zero")

	// Test with zero cooldown
	cluster = validCluster(solanagorpc.TestNet.Name)
	cluster.RPCURLCooldownDuration = 0
	err = cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.rpc_url_cooldown_duration must be greater than zero")
}
