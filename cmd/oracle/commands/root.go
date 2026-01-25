package commands

import (
	"github.com/spf13/cobra"
)

var (
	// Version is the build version.
	Version string
	// GitCommit is the git commit hash.
	GitCommit string
	// BuildTime is the build timestamp.
	BuildTime string
)

var rootCmd = &cobra.Command{
	Use:   "ssv-oracle",
	Short: "SSV Oracle Client",
	Long: `SSV Oracle Client - Publishes Merkle roots of SSV cluster effective
balances to the SSV Network contract, with optional cluster balance updates.`,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(versionCmd)
}
