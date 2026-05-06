package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"temporal/internal/pidfile"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the temporal daemon",
	Run: func(cmd *cobra.Command, args []string) {
		startServer()
	},
}

func init() {
	serverCmd.AddCommand(startCmd)
}

func startServer() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("Could not get user's home directory:", err)
	}
	logDir := filepath.Join(home, ".temporal")
	pidFile := filepath.Join(logDir, "daemon.pid")

	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatal("Failed to create log directory:", err)
	}

	// If a live daemon already owns the PID file, bail out with its PID
	// so the user knows what to stop. If the file exists but the process
	// is gone, treat it as stale and remove it before retrying.
	if pid, alive := pidfile.Read(pidFile); alive {
		fmt.Printf("Server is already running with PID %d. Run `temporal server stop` to stop it.\n", pid)
		return
	}
	pidfile.Remove(pidFile)

	executable, err := os.Executable()
	if err != nil {
		log.Fatal("Could not resolve own executable path:", err)
	}
	procAttr := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	process, err := os.StartProcess(executable, []string{executable, "server", "run"}, procAttr)
	if err != nil {
		log.Fatal("Failed to start server:", err)
	}

	if err := pidfile.Acquire(pidFile, process.Pid); err != nil {
		// Couldn't claim the PID file — kill the orphan we just spawned
		// rather than leaving it running without a way to stop it.
		_ = process.Kill()
		log.Fatal("Failed to write pid file:", err)
	}

	fmt.Printf("Server started with PID: %d\n", process.Pid)
	process.Release()
}
