package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"temporal/internal/pidfile"

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
	serverCmd.AddCommand(stopCmd)
}

// stopWaitTimeout caps how long we wait for the daemon to exit after
// SIGTERM. A second `start` issued immediately after `stop` shouldn't
// race with the previous process still listening on :8005.
const stopWaitTimeout = 3 * time.Second

func stopServer() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("Could not get user's home directory:", err)
	}
	logDir := filepath.Join(home, ".temporal")
	pidFile := filepath.Join(logDir, "daemon.pid")

	pid, alive := pidfile.Read(pidFile)
	if !alive {
		fmt.Println("Server is not running.")
		pidfile.Remove(pidFile)
		return
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		log.Println("Failed to send signal to process:", err)
		pidfile.Remove(pidFile)
		return
	}

	// Poll signal-0 until the process is gone. If it lingers past the
	// timeout, leave the PID file in place and tell the user — they may
	// want to investigate before sending SIGKILL.
	deadline := time.Now().Add(stopWaitTimeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
			pidfile.Remove(pidFile)
			fmt.Println("Server stopped.")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "Server (PID %d) did not exit within %s. Send SIGKILL manually if needed.\n", pid, stopWaitTimeout)
	os.Exit(1)
}
