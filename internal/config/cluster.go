package config

import (
	"fmt"
	"net/url"
	"time"

	solanagorpc "github.com/gagliardetto/solana-go/rpc"
)

// Cluster represents the Solana cluster configuration
type Cluster struct {
	Name    string   `koanf:"name"`
	RPCURLs []string `koanf:"rpc_urls"`
	// RPCTimeoutDuration is the per-call timeout for cluster RPC requests.
	// Default: 2s. Lower values detect hanging endpoints faster; set higher if your RPC is slow.
	RPCTimeoutDuration time.Duration `koanf:"rpc_timeout_duration"`
	// RPCURLCooldownDuration is how long a URL that returns 403/429/503 is deprioritised.
	// Default: 15s. Lower values retry throttled URLs sooner.
	RPCURLCooldownDuration time.Duration `koanf:"rpc_url_cooldown_duration"`
}

// Validate validates the cluster configuration
func (c *Cluster) Validate() error {
	// cluster.name must be set; any non-empty name is accepted (including custom
	// or future clusters such as Alpenglow), since RPC URLs are validated below.
	if c.Name == "" {
		return fmt.Errorf("cluster.name must not be empty")
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

	if c.RPCTimeoutDuration <= 0 {
		return fmt.Errorf("cluster.rpc_timeout_duration must be greater than zero")
	}
	if c.RPCURLCooldownDuration <= 0 {
		return fmt.Errorf("cluster.rpc_url_cooldown_duration must be greater than zero")
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
	if c.RPCTimeoutDuration == 0 {
		c.RPCTimeoutDuration = 5 * time.Second
	}
	if c.RPCURLCooldownDuration == 0 {
		c.RPCURLCooldownDuration = 60 * time.Second
	}
}
