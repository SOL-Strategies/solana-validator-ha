package cmd

import (
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
		// Load configuration
		var err error
		loadedConfig, err = config.NewFromConfigFile(configFile)
		if err != nil {
			log.Fatal("failed to load configuration", "error", err)
		}

		loadedConfig.Log.ConfigureWithLevelString(logLevel)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Add global flags here
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "~/solana-validator-ha/config.yaml", "Path to configuration file (default: ~/solana-validator-ha/config.yaml)")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "", "Log level (debug, info, warn, error, fatal) - overrides config.yaml log.level if specified")

	// Add subcommands here
	rootCmd.AddCommand(runCmd)
}
