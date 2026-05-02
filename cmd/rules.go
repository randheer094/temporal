package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"temporal/internal/rules"

	"github.com/spf13/cobra"
)

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Validate rules.yaml and refresh the running daemon",
	Long: `Parse ~/.temporal/rules.yaml and report any errors. If the daemon is
running, signal it to reload the file in place — no restart needed.

Exit code is 1 if the file fails to parse, 0 otherwise. The signal is only
sent when the local parse succeeds, so a broken file never replaces a
working in-memory rule set.`,
	Run: func(cmd *cobra.Command, args []string) {
		refreshRules()
	},
}

func init() {
	serverCmd.AddCommand(rulesCmd)
}

func refreshRules() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("Could not get user's home directory:", err)
	}
	logDir := filepath.Join(home, ".temporal")
	rulesPath := filepath.Join(logDir, "rules.yaml")

	if _, err := os.Stat(rulesPath); os.IsNotExist(err) {
		fmt.Printf("No rules file at %s — every request will return no_match.\n", rulesPath)
		return
	}

	rs, err := rules.Load(rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rules.yaml failed to parse: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("rules.yaml OK — %d rule(s) loaded:\n", len(rs.Rules))
	for _, r := range rs.Rules {
		fmt.Printf("  - %s\n", r.Name)
	}

	pid, ok := readDaemonPID(filepath.Join(logDir, "daemon.pid"))
	if !ok {
		fmt.Println("Daemon is not running — rules will load on next start.")
		return
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to signal daemon (PID %d): %v\n", pid, err)
		os.Exit(1)
	}
	fmt.Printf("Refreshed running daemon (PID %d).\n", pid)
}

// readDaemonPID returns the daemon PID from the file, or (_, false) if the
// file is missing or the process isn't actually alive.
func readDaemonPID(pidFile string) (int, bool) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, false
	}
	if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
		return 0, false
	}
	return pid, true
}
