package cmd

import (
	"log"
	"os"
	"path/filepath"
	"temporal/internal/api"

	"github.com/spf13/cobra"
)

var runServerCmd = &cobra.Command{
	Use:   "run-server",
	Short: "Run the temporal daemon in the foreground",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal("Could not get user's home directory:", err)
		}
		logDir := filepath.Join(home, ".temporal")
		a := api.NewAPI(logDir)
		a.Run()
	},
}

func init() {
	rootCmd.AddCommand(runServerCmd)
}
