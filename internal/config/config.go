package config

import (
	"fmt"

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
	k := koanf.New(".")
	c.File = filePath

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
		ActiveIdentityPubkey:       c.Validator.Identities.ActiveKeyPair.PublicKey().String(),
		PassiveIdentityKeypairFile: c.Validator.Identities.PassiveKeyPairFile,
		PassiveIdentityPubkey:      c.Validator.Identities.PassiveKeyPair.PublicKey().String(),
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

	// failover.dry_run if true print warning
	if c.Failover.DryRun {
		c.logger.Warn("failover.dry_run is true - failovers will dry-run commands only and be no-op")
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
}
