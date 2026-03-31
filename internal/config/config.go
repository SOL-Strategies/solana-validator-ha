package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
)

const (
	ClusterNameCustom = "custom"
)

// Config represents the complete configuration
type Config struct {
	// Log
	Log Log `koanf:"log"`
	// Validator is the local validator configuration
	Validator Validator `koanf:"validator"`
	// Cluster is the Solana cluster configuration
	Cluster Cluster `koanf:"cluster"`
	// Prometheus is the Prometheus metrics configuration
	Prometheus Prometheus `koanf:"prometheus"`
	// Failover is the failover decision parameters
	Failover Failover `koanf:"failover"`
	// Update holds update-check settings
	Update Update `koanf:"update"`
	// File is the file that the config was loaded from
	File string `koanf:"-"`
	// GetPublicIPFunc is a function that returns the public IP address of the current validator
	// it defaults to using external services to get the public IP address, useful for testing to set to
	// something else
	GetPublicIPFunc func() (string, error)

	logger *log.Logger
}

// NewConfigParams represents parameters for creating a new Config
type NewConfigParams struct {
	GetPublicIPFunc func() (string, error)
}

// New creates a new Config
func New(params NewConfigParams) (config *Config, err error) {
	config = &Config{
		logger: log.WithPrefix("config"),
	}

	if params.GetPublicIPFunc != nil {
		config.GetPublicIPFunc = params.GetPublicIPFunc
	}

	return config, nil
}

// NewFromConfigFile creates a new Config from a config file path
func NewFromConfigFile(configFile string) (*Config, error) {
	// Create new config
	cfg, err := New(NewConfigParams{})
	if err != nil {
		return nil, err
	}

	// Load from file
	if err := cfg.LoadFromFile(configFile); err != nil {
		return nil, err
	}

	// Initialize
	if err := cfg.Initialize(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadFromFile loads configuration from file into the struct
func (c *Config) LoadFromFile(filePath string) error {
	// Expand ~ to home directory
	if strings.HasPrefix(filePath, "~/") || filePath == "~" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("error getting home directory: %w", err)
		}
		if filePath == "~" {
			filePath = homeDir
		} else {
			filePath = strings.Replace(filePath, "~", homeDir, 1)
		}
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("error resolving absolute path: %w", err)
	}

	// Resolve symlinks to get the actual file path
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// If EvalSymlinks fails (e.g., path doesn't exist yet), use the absolute path
		// The file provider will handle the actual file opening
		resolvedPath = absPath
	}

	k := koanf.New(".")
	c.File = resolvedPath

	// Set bool defaults that cannot be expressed as Go zero values.
	// k.Set must be called before k.Load so the file can override them.
	k.Set("update.check_enabled", true) //nolint:errcheck

	// Load YAML config file
	if err := k.Load(file.Provider(c.File), yaml.Parser()); err != nil {
		return fmt.Errorf("error loading config file: %w", err)
	}

	// Unmarshal into this config struct
	if err := k.Unmarshal("", c); err != nil {
		return fmt.Errorf("error unmarshaling config: %w", err)
	}

	return nil
}

// Initialize processes and validates the loaded configuration
func (c *Config) Initialize() error {
	// Set defaults
	c.setDefaults()

	// load identity key pair files
	if err := c.Validator.Identities.Load(); err != nil {
		return err
	}

	// validate configuration (after identity files are loaded)
	if err := c.validate(); err != nil {
		return err
	}

	// render failover commands, args and hooks
	err := c.Failover.RenderRoleCommands(RoleCommandTemplateData{
		ActiveIdentityKeypairFile:  c.Validator.Identities.ActiveKeyPairFile,
		ActiveIdentityPubkey:       c.Validator.Identities.ActivePubkey(),
		PassiveIdentityKeypairFile: c.Validator.Identities.PassiveKeyPairFile,
		PassiveIdentityPubkey:      c.Validator.Identities.PassivePubkey(),
		SelfName:                   c.Validator.Name,
	})
	if err != nil {
		return err
	}

	return nil
}

// validate validates the configuration
func (c *Config) validate() error {
	err := c.Log.Validate()
	if err != nil {
		return err
	}

	err = c.Validator.Validate()
	if err != nil {
		return err
	}

	err = c.Cluster.Validate()
	if err != nil {
		return err
	}

	err = c.Prometheus.Validate()
	if err != nil {
		return err
	}

	err = c.Failover.Validate()
	if err != nil {
		return err
	}

	// cluster.rpc_urls may contain the local validator RPC URL, but only when HA peers
	// are configured as mutual --entrypoint flags (enabling direct CRDS gossip exchange).
	// Without mutual --entrypoints, local gossip data for peers may be stale. It is safe
	// as a last-resort fallback alongside other URLs since it is immune to rate limiting.
	validatorRPCHost, err := urlHost(c.Validator.RPCURL)
	if err != nil {
		return fmt.Errorf("failed to parse validator.rpc_url host: %w", err)
	}
	for _, clusterRPCURL := range c.Cluster.RPCURLs {
		clusterRPCHost, err := urlHost(clusterRPCURL)
		if err != nil {
			continue // Already validated in Cluster.Validate()
		}
		if clusterRPCHost == validatorRPCHost {
			c.logger.Warnf("cluster.rpc_urls contains the local validator RPC URL (%s) - ensure HA peers are configured as mutual --entrypoint flags for direct gossip, otherwise peer state may be stale", c.Validator.RPCURL)
			break
		}
	}

	// failover.leaderless_confirmation_poll_duration below the 1s floor - clamp and warn
	if c.Failover.LeaderlessConfirmationPollDuration < leaderlessConfirmationPollFloor {
		c.logger.Warnf(
			"failover.leaderless_confirmation_poll_duration %s is below the minimum of %s - clamped to %s; sub-second polling sits inside gossip propagation jitter and increases false-positive failover risk",
			c.Failover.LeaderlessConfirmationPollDuration, leaderlessConfirmationPollFloor, leaderlessConfirmationPollFloor,
		)
		c.Failover.LeaderlessConfirmationPollDuration = leaderlessConfirmationPollFloor
	}

	// failover.dry_run if true print warning
	if c.Failover.DryRun {
		c.logger.Warn("failover.dry_run is true - failovers will dry-run commands only and be no-op")
	}

	// failover.takeover_jitter_duration if below 1s print warning
	if c.Failover.TakeoverJitterDuration > 0 {
		c.logger.Warn("failover.takeover_jitter_duration is deprecated and this value will be ignored - takeover delays are now deterministic based on <zero-indexed gossip state IP rank>*<poll_interval_duration>")
	}

	// failover.delinquent_slot_distance_override enabled but distance is too short - warn that it will be set to a safer value
	if c.Failover.DelinquentSlotDistanceOverride.Enabled && c.Failover.DelinquentSlotDistanceOverride.Value <= 1 {
		c.logger.Warnf("failover.delinquent_slot_distance_override is enabled but distance %d <= 1 slot - setting to 2 slots",
			c.Failover.DelinquentSlotDistanceOverride.Value)
		c.Failover.DelinquentSlotDistanceOverride.Value = 2
	}

	// failover.delinquent_slot_distance_override is below Agave's default of 128 - peers may be declared delinquent before
	// their own /health endpoint agrees; recommend setting --health-check-slot-distance on the validator to match
	if c.Failover.DelinquentSlotDistanceOverride.Enabled && c.Failover.DelinquentSlotDistanceOverride.Value < 128 {
		c.logger.Warnf("failover.delinquent_slot_distance_override value %d is below Agave's default of 128 slots - a peer may be declared delinquent before its /health endpoint agrees; consider adding --health-check-slot-distance %d to your validator startup flags to align the thresholds",
			c.Failover.DelinquentSlotDistanceOverride.Value,
			c.Failover.DelinquentSlotDistanceOverride.Value)
	}

	// failover.deliquent_slot_distance_override is enabled - print warning
	if c.Failover.DelinquentSlotDistanceOverride.Enabled {
		// delinquentSlotDistanceDuration estimated duration behind given 400ms slot time
		delinquentSlotDistanceInt64 := int64(c.Failover.DelinquentSlotDistanceOverride.Value)
		delinquentSlotDistanceDuration := time.Duration(delinquentSlotDistanceInt64) * 400 * time.Millisecond
		c.logger.Warnf("failover.deliquent_slot_distance_override enabled - nodes considered delinquent if behind by more than %d slots (~%s)",
			c.Failover.DelinquentSlotDistanceOverride.Value,
			delinquentSlotDistanceDuration,
		)
	}

	// failover.recording: validate output dir is reachable and writable at startup
	if c.Failover.Recording.Enabled {
		resolvedDir := c.Failover.Recording.ResolvedOutputDir(c.File)
		if err := c.Failover.Recording.ValidateOutputDir(resolvedDir); err != nil {
			return err
		}
		c.logger.Infof("failover recording enabled - writing to %s", resolvedDir)
	}

	return nil
}

// setDefaults sets default values for configuration
func (c *Config) setDefaults() {
	c.Log.SetDefaults()
	c.Validator.SetDefaults()
	c.Cluster.SetDefaults()
	c.Prometheus.SetDefaults()
	c.Failover.SetDefaults()
	c.Update.SetDefaults()
}

// urlHost extracts the host:port from a URL
func urlHost(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return parsed.Host, nil
}
