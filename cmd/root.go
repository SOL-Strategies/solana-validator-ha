package cmd

import (
	"fmt"
	"runtime"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/updater"
	"github.com/spf13/cobra"
)

var (
	version     = "dev"
	buildCommit = "unknown"
	buildTime   = "unknown"
)

var (
	configFile   string
	logLevel     string
	loadedConfig *config.Config
)

var rootCmd = &cobra.Command{
	Use:     "solana-validator-ha",
	Short:   "High availability manager for Solana validators",
	Version: version,
	Long: fmt.Sprintf(`Solana Validator HA is a high availability manager for Solana validators.
It monitors peers and manages failover decisions to ensure continuous validator operation.

Version: %s`, version),
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

		// Check for updates on startup
		if loadedConfig.Update.CheckEnabled {
			ch := updater.StartBackgroundCheck(version)
			updater.PrintWarningIfAvailable(ch)
		}
	},
}

var versionCmd = &cobra.Command{
	Use:              "version",
	Short:            "Show detailed binary build information",
	Args:             cobra.NoArgs,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "version: %s\ncommit: %s\nbuilt: %s\ngo: %s\nplatform: %s/%s\n",
			version, buildCommit, buildTime, runtime.Version(), runtime.GOOS, runtime.GOARCH)
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
	rootCmd.AddCommand(replayCmd)
	rootCmd.AddCommand(versionCmd)
}
