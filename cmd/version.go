package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const Version = "0.1.0"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the temporal version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("temporal %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
