package cmd

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"

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

	// Check if the process is already running
	if _, err := os.Stat(pidFile); err == nil {
		fmt.Println("Server is already running.")
		return
	}

	// Create a new process
	executable, _ := os.Executable()
	procAttr := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	process, err := os.StartProcess(executable, []string{executable, "server", "run"}, procAttr)
	if err != nil {
		log.Fatal("Failed to start server:", err)
	}

	// Write the PID to the pid file
	err = ioutil.WriteFile(pidFile, []byte(strconv.Itoa(process.Pid)), 0644)
	if err != nil {
		log.Fatal("Failed to write pid file:", err)
	}

	fmt.Printf("Server started with PID: %d\n", process.Pid)
	process.Release()
}
