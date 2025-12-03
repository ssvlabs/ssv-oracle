package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Print version, git commit, and build time information.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("SSV Oracle Client\n")
		fmt.Printf("Version:    %s\n", Version)
		fmt.Printf("Git Commit: %s\n", GitCommit)
		fmt.Printf("Built:      %s\n", BuildTime)
	},
}
