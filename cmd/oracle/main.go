package main

import (
	"fmt"
	"os"

	"ssv-oracle/cmd/oracle/commands"
	"ssv-oracle/pkg/logger"
)

// Build-time variables set via ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func main() {
	logger.InitFromEnv()
	defer logger.Sync()

	commands.Version = Version
	commands.GitCommit = GitCommit
	commands.BuildTime = BuildTime

	if err := commands.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
