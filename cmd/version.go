package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"temporal/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the temporal version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("temporal %s\n", version.String())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
