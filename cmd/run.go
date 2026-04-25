package cmd

import (
	"log"
	"os"
	"path/filepath"
	"temporal/internal/api"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:    "run",
	Short:  "Run the temporal daemon in the foreground",
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
	serverCmd.AddCommand(runCmd)
}
