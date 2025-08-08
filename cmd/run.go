package cmd

import (
	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/ha"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:           "run",
	Short:         "Start the Solana validator HA manager",
	Long:          `Start the high availability manager to monitor peers and manage failover decisions.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Run: func(cmd *cobra.Command, args []string) {
		// Start the HA manager with the loaded config
		manager := ha.NewManager(ha.NewManagerOptions{
			Cfg: loadedConfig,
		})
		err := manager.Run()
		if err != nil {
			log.Fatal("failed to run manager", "error", err)
		}
	},
}
