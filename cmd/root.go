package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "temporal",
	Short: "A simple daemon for logging events.",
	Long:  `A simple daemon for logging events. You can start, stop, and check the status of the server.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
