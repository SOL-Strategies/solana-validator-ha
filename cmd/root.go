package cmd

import (
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/spf13/cobra"
)

var (
	configFile   string
	logLevel     string
	loadedConfig *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "solana-validator-ha",
	Short: "High availability manager for Solana validators",
	Long: `Solana Validator HA is a high availability manager for Solana validators.
It monitors peers and manages failover decisions to ensure continuous validator operation.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Set the global log level
		level, err := log.ParseLevel(logLevel)
		if err != nil {
			log.Fatal("invalid log level", "error", err)
		}
		log.SetLevel(level)
		log.SetTimeFunction(func() time.Time {
			return time.Now().UTC()
		})
		log.SetTimeFormat("2006-01-02T15:04:05.000Z07:00")

		// Load configuration
		cfg, err := config.NewFromConfigFile(configFile)
		if err != nil {
			log.Fatal("failed to load configuration", "error", err)
		}
		loadedConfig = cfg
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Add global flags here
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "~/solana-validator-ha/config.yaml", "Path to configuration file (default: ~/solana-validator-ha/config.yaml)")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "Log level (debug, info, warn, error, fatal) (default: info)")

	// Add subcommands here
	rootCmd.AddCommand(runCmd)
}
