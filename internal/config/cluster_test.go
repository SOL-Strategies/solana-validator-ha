package config

import (
	"testing"

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
}

func TestCluster_Validate(t *testing.T) {
	// Test with valid cluster names and RPC URLs
	validClusters := []string{
		solanagorpc.MainNetBeta.Name,
		solanagorpc.TestNet.Name,
		solanagorpc.DevNet.Name,
	}

	for _, clusterName := range validClusters {
		cluster := &Cluster{
			Name:    clusterName,
			RPCURLs: []string{"https://api.testnet.solana.com"},
		}
		err := cluster.Validate()
		assert.NoError(t, err, "Cluster name %s should be valid", clusterName)
	}

	// Test with invalid cluster name
	cluster := &Cluster{Name: "invalid-cluster"}
	err := cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster name must be one of")

	// Test with empty RPC URLs
	cluster = &Cluster{Name: solanagorpc.TestNet.Name, RPCURLs: []string{}}
	err = cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.rpc_urls must be a non-empty list")

	// Test with invalid RPC URL
	cluster = &Cluster{
		Name:    solanagorpc.TestNet.Name,
		RPCURLs: []string{"invalid-url"},
	}
	err = cluster.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.rpc_urls must be a list of valid RPC URLs")
}
