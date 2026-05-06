// Package pidfile manages the daemon PID file. The file lives at
// ~/.temporal/daemon.pid and is the single source of truth for whether a
// daemon is running and which process to signal.
//
// The package centralizes three concerns that the cmd/* entry points used
// to duplicate (and got slightly wrong each time): atomic acquire so two
// concurrent `start` invocations can't race; ownership verification via
// signal-0 so a stale PID never causes us to signal an unrelated process;
// and a 0600 mode so the file isn't world-readable.
package pidfile

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// Acquire creates path with O_EXCL and writes pid to it. Returns an error
// if the file already exists; callers should treat that case as "another
// daemon is running" and surface the existing PID via Read.
func Acquire(path string, pid int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%d", pid); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Read returns the PID stored in path along with whether that process is
// alive. A missing file, an unparseable file, or a dead process all
// resolve to (_, false). Use Read to decide whether a `stop` should
// actually signal anything, or whether `status` should report "running".
func Read(path string) (int, bool) {
	data, err := os.ReadFile(path)
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

// Remove deletes path, ignoring "already gone" errors.
func Remove(path string) {
	_ = os.Remove(path)
}
