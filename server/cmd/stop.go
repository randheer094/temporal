package cmd

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the temporal daemon",
	Run: func(cmd *cobra.Command, args []string) {
		stopServer()
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func stopServer() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("Could not get user's home directory:", err)
	}
	logDir := filepath.Join(home, ".temporal")
	pidFile := filepath.Join(logDir, "daemon.pid")

	pidData, err := ioutil.ReadFile(pidFile)
	if err != nil {
		fmt.Println("Server is not running.")
		return
	}

	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		log.Fatal("Invalid PID in pid file:", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		// If FindProcess fails, the process likely doesn't exist.
		fmt.Println("Server is not running.")
		os.Remove(pidFile)
		return
	}

	// Send a signal to the process to terminate it
	if err := process.Signal(syscall.SIGTERM); err != nil {
		log.Println("Failed to send signal to process:", err)
		// If signaling fails, maybe the process is already gone
		os.Remove(pidFile)
		return
	}

	// Clean up the pid file
	if err := os.Remove(pidFile); err != nil {
		log.Println("Failed to remove pid file:", err)
	}

	fmt.Println("Server stopped.")
}
