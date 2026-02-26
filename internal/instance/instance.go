package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const lockFileName = "obs-agent.lock"

// Lock represents a held instance lock.
type Lock struct {
	fd   lockHandle
	path string
}

// Acquire tries to obtain an exclusive instance lock in the given directory.
// Returns an error if another instance is already running.
func Acquire(dir string) (*Lock, error) {
	path := filepath.Join(dir, lockFileName)

	fd, err := tryLock(path)
	if err != nil {
		// Try to read existing PID for a helpful message
		if data, readErr := os.ReadFile(path); readErr == nil {
			pid := strings.TrimSpace(string(data))
			if pid != "" {
				return nil, fmt.Errorf("another instance is running (PID: %s)", pid)
			}
		}
		return nil, fmt.Errorf("another instance is running")
	}

	// Write our PID for diagnostics
	pidStr := strconv.Itoa(os.Getpid())
	// Truncate and write PID (best-effort, lock is already held)
	writePID(fd, path, pidStr)

	return &Lock{fd: fd, path: path}, nil
}

// Release releases the instance lock.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	unlock(l.fd)
	os.Remove(l.path)
}
