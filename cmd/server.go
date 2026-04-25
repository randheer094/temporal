package cmd

import (
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the temporal event-logging server",
	Long:  `Start, stop, and check the status of the temporal event-logging daemon.`,
}

func init() {
	rootCmd.AddCommand(serverCmd)
}
