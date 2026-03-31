package cmd

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/recording"
	"github.com/spf13/cobra"
)

var replayCmd = &cobra.Command{
	Use:   "replay <recording-file> [recording-file...]",
	Short: "Replay a failover event from one or more recording files",
	Long: `Replay loads one or more failover recording files produced by solana-validator-ha
and prints a merged, chronological timeline across all nodes — useful for correlating
what each node observed during the same failover event.

Recording files are produced when failover.recording.enabled: true in config.yaml.
Files from the same failover event share the same active identity pubkey prefix in
their filename and can be replayed together:

  solana-validator-ha replay \
    svha-<pubkey>-<timestamp>-london-chicago-to-london-recording.json \
    svha-<pubkey>-<timestamp>-chicago-chicago-to-london-recording.json`,
	Args:             cobra.MinimumNArgs(1),
	SilenceUsage:     true,
	SilenceErrors:    true,
	// Override the root PersistentPreRun — replay needs no config file.
	PersistentPreRun: func(cmd *cobra.Command, args []string) {},
	RunE: func(cmd *cobra.Command, args []string) error {
		data, warnings, err := recording.LoadReplayData(args)
		if err != nil {
			return fmt.Errorf("loading recording files: %w", err)
		}

		for _, w := range warnings {
			log.Warn(w)
		}

		out, err := recording.RenderReplay(data)
		if err != nil {
			return fmt.Errorf("rendering replay: %w", err)
		}

		fmt.Print(out)
		return nil
	},
}
