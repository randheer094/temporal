package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

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
	logDir := filepath.Join(home, ".temporal")
	pidFile := filepath.Join(logDir, "daemon.pid")

	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Println("Server is not running.")
		return
	}

	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		fmt.Println("Invalid PID in pid file.")
		return
	}

	// On Unix-like systems, os.FindProcess is a no-op.
	// We need to send a signal to check if the process exists.
	// Sending signal 0 doesn't kill the process but checks for its existence.
	process, _ := os.FindProcess(pid)
	if err := process.Signal(syscall.Signal(0)); err == nil {
		fmt.Printf("Server is running with PID: %d\n", pid)
	} else {
		fmt.Println("Server is not running, but pid file exists.")
	}
}
