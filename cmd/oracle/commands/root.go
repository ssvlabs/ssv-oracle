package commands

import (
	"github.com/spf13/cobra"
)

var (
	// Version info (set by main)
	Version   string
	GitCommit string
	BuildTime string
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "ssv-oracle",
	Short: "SSV Oracle Client",
	Long: `SSV Oracle Client - An offchain oracle that periodically publishes
Merkle roots of SSV cluster effective balances to an onchain oracle contract.`,
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Add subcommands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(versionCmd)
}
