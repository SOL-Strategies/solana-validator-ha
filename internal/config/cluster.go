package config

import (
	"bufio"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	solanagorpc "github.com/solana-foundation/solana-go/v2/rpc"
)

// Cluster represents the Solana cluster configuration
type Cluster struct {
	Name    string   `koanf:"name"`
	RPCURLs []string `koanf:"rpc_urls"`
	// rpcURLLabels retains safe source URIs for endpoints loaded from files.
	rpcURLLabels map[string]string
	// RPCTimeoutDuration is the per-call timeout for cluster RPC requests.
	// Default: 2s. Lower values detect hanging endpoints faster; set higher if your RPC is slow.
	RPCTimeoutDuration time.Duration `koanf:"rpc_timeout_duration"`
	// RPCURLCooldownDuration is how long a URL that returns 403/429/503 is deprioritised.
	// Default: 15s. Lower values retry throttled URLs sooner.
	RPCURLCooldownDuration time.Duration `koanf:"rpc_url_cooldown_duration"`
	// Consensus controls how the cluster's consensus protocol (TowerBFT or Alpenglow) is determined.
	Consensus Consensus `koanf:"consensus"`
}

// Validate validates the cluster configuration and resolves file-backed RPC URLs.
// Resolution is committed only on success; subsequent validation uses the loaded values.
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

	resolved := make([]string, len(c.RPCURLs))
	labels := c.RPCURLLabels()
	for i, source := range c.RPCURLs {
		endpoint, label, err := resolveRPCURL(source)
		if err != nil {
			return fmt.Errorf("cluster.rpc_urls must be a list of valid RPC URLs: cluster.rpc_urls[%d]: %w", i, err)
		}
		resolved[i] = endpoint
		if label != "" {
			labels[endpoint] = label
		}
	}

	if c.RPCTimeoutDuration <= 0 {
		return fmt.Errorf("cluster.rpc_timeout_duration must be greater than zero")
	}
	if c.RPCURLCooldownDuration <= 0 {
		return fmt.Errorf("cluster.rpc_url_cooldown_duration must be greater than zero")
	}

	c.RPCURLs = resolved
	c.rpcURLLabels = labels

	return nil
}

// RPCURLLabels returns a copy of the safe labels for file-backed endpoint URLs.
func (c *Cluster) RPCURLLabels() map[string]string {
	labels := maps.Clone(c.rpcURLLabels)
	if labels == nil {
		labels = make(map[string]string)
	}
	return labels
}

func resolveRPCURL(source string) (endpoint, label string, err error) {
	parsed, err := url.Parse(source)
	if err != nil {
		// url.Parse errors can embed the entire input, including credentials.
		return "", "", fmt.Errorf("invalid URL or file URI")
	}
	endpoint = source
	if parsed.Scheme == "file" {
		if !strings.HasPrefix(source, "file://") ||
			(parsed.Host != "" && parsed.Host != "localhost") ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
			strings.Contains(source, "#") || parsed.Opaque != "" ||
			!filepath.IsAbs(parsed.Path) || strings.ContainsRune(parsed.Path, 0) {
			return "", "", fmt.Errorf("file URI must specify an absolute local path without credentials, query, or fragment")
		}
		endpoint, err = readRPCURLFile(parsed.Path)
		if err != nil {
			return "", "", fmt.Errorf("file %s: %w", source, err)
		}
		label = source
	}
	parsed, err = url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		if label != "" {
			return "", "", fmt.Errorf("file %s: first non-blank, non-comment line must be an HTTP/HTTPS URL with a hostname", label)
		}
		return "", "", fmt.Errorf("endpoint must be an HTTP/HTTPS URL with a hostname")
	}
	return endpoint, label, nil
}

func readRPCURLFile(path string) (string, error) {
	// Check before opening so a FIFO or device does not block startup.
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot stat file")
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot open file for reading")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("cannot read a regular file")
	}

	const maxLineBytes = 64 * 1024
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), maxLineBytes+2)
	for scanner.Scan() {
		if len(scanner.Bytes()) > maxLineBytes {
			return "", fmt.Errorf("line exceeds 64 KiB")
		}
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			return line, nil
		}
	}
	if scanner.Err() != nil {
		return "", fmt.Errorf("cannot read file or line exceeds 64 KiB")
	}
	return "", fmt.Errorf("no non-blank, non-comment URL line")
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
	c.Consensus.SetDefaults()
}
