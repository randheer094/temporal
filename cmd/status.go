package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"temporal/internal/pidfile"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the status of the temporal daemon",
	Run: func(cmd *cobra.Command, args []string) {
		serverStatus()
	},
}

func init() {
	serverCmd.AddCommand(statusCmd)
}

func serverStatus() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("Could not get user's home directory:", err)
	}
	pidFile := filepath.Join(home, ".temporal", "daemon.pid")

	pid, alive := pidfile.Read(pidFile)
	if !alive {
		fmt.Fprintln(os.Stderr, "Server is not running.")
		os.Exit(1)
	}
	fmt.Printf("Server is running with PID: %d\n", pid)
}
