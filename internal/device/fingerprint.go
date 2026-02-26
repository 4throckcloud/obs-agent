package device

import (
	"crypto/sha256"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// MachineID returns a stable SHA-256 hex fingerprint for this machine.
// Uses hostname + OS + architecture as base identifiers.
// On Windows, also includes COMPUTERNAME and USERNAME env vars for extra uniqueness.
func MachineID() string {
	var parts []string

	hostname, _ := os.Hostname()
	if hostname != "" {
		parts = append(parts, "host:"+hostname)
	}

	parts = append(parts, "os:"+runtime.GOOS)
	parts = append(parts, "arch:"+runtime.GOARCH)

	// Platform-specific stable identifiers
	switch runtime.GOOS {
	case "windows":
		if cn := os.Getenv("COMPUTERNAME"); cn != "" {
			parts = append(parts, "cn:"+cn)
		}
		if user := os.Getenv("USERNAME"); user != "" {
			parts = append(parts, "user:"+user)
		}
		if vol := os.Getenv("SystemDrive"); vol != "" {
			parts = append(parts, "drive:"+vol)
		}
	case "linux", "darwin":
		// Read machine-id if available (stable across reboots)
		for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
			if data, err := os.ReadFile(path); err == nil {
				id := strings.TrimSpace(string(data))
				if id != "" {
					parts = append(parts, "mid:"+id)
					break
				}
			}
		}
		if user := os.Getenv("USER"); user != "" {
			parts = append(parts, "user:"+user)
		}
	}

	combined := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash[:])
}
