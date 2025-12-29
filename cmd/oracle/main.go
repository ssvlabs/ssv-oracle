package main

import (
	"fmt"
	"os"

	"ssv-oracle/cmd/oracle/commands"
	"ssv-oracle/logger"
)

var (
	// Version is the build version.
	Version = "dev"
	// GitCommit is the git commit hash.
	GitCommit = "unknown"
	// BuildTime is the build timestamp.
	BuildTime = "unknown"
)

func main() {
	commands.Version = Version
	commands.GitCommit = GitCommit
	commands.BuildTime = BuildTime

	if err := commands.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		logger.Sync()
		os.Exit(1)
	}
	logger.Sync()
}
