package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "temporal",
	Short: "Temporal CLI toolkit",
	Long:  `Temporal is a CLI toolkit. It currently includes the temporal event-logging server, accessible under the "server" subcommand.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
