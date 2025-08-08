package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	solanagorpc "github.com/gagliardetto/solana-go/rpc"
)

// Cluster represents the Solana cluster configuration
type Cluster struct {
	Name    string   `koanf:"name"`
	RPCURLs []string `koanf:"rpc_urls"`
}

// Validate validates the cluster configuration
func (c *Cluster) Validate() error {
	// cluster.name must be one of mainnet-beta, testnet, devnet
	var validClusterNames = []string{
		solanagorpc.MainNetBeta.Name,
		solanagorpc.DevNet.Name,
		solanagorpc.TestNet.Name,
	}

	// cluster.name must be one of the valid cluster names
	if !slices.Contains(validClusterNames, c.Name) {
		return fmt.Errorf("cluster name must be one of %s", strings.Join(validClusterNames, ", "))
	}

	// cluster.rpc_urls must be a non-empty list of valid RPC URLs
	if len(c.RPCURLs) == 0 {
		return fmt.Errorf("cluster.rpc_urls must be a non-empty list of valid RPC URLs")
	}

	for _, rpcURL := range c.RPCURLs {
		parsedURL, err := url.Parse(rpcURL)
		if err != nil {
			return fmt.Errorf("cluster.rpc_urls must be a list of valid RPC URLs: %w", err)
		}
		// Additional validation: must have a scheme and host
		if parsedURL.Scheme == "" || parsedURL.Host == "" {
			return fmt.Errorf("cluster.rpc_urls must be a list of valid RPC URLs: invalid URL %s", rpcURL)
		}
	}

	return nil
}

// SetDefaults sets default values for the cluster configuration
func (c *Cluster) SetDefaults() {
	// if cluster.rpc_urls is empty, set it to the default RPC URLs for the cluster
	if len(c.RPCURLs) == 0 {
		switch c.Name {
		case solanagorpc.MainNetBeta.Name:
			c.RPCURLs = []string{solanagorpc.MainNetBeta.RPC}
		case solanagorpc.TestNet.Name:
			c.RPCURLs = []string{solanagorpc.TestNet.RPC}
		case solanagorpc.DevNet.Name:
			c.RPCURLs = []string{solanagorpc.DevNet.RPC}
		}
	}
}
