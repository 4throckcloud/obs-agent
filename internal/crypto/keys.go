package crypto

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// DeriveKey derives a 32-byte encryption key from the agent token + machine ID
// using HKDF-SHA256. This makes the encrypted config machine-locked.
//
// Returns an error if machine ID is unavailable — no silent fallback.
func DeriveKey(token string) ([]byte, error) {
	machineID, err := getMachineID()
	if err != nil {
		return nil, fmt.Errorf("machine ID required for key derivation: %w", err)
	}

	// IKM = token, salt = machine ID
	hkdfReader := hkdf.New(sha256.New, []byte(token), []byte(machineID), []byte("obs-agent-config-v1"))

	key := make([]byte, 32)
	if _, err := hkdfReader.Read(key); err != nil {
		return nil, fmt.Errorf("HKDF key derivation failed: %w", err)
	}
	return key, nil
}

// getMachineID returns a stable machine identifier.
// Returns error if unavailable — callers must handle explicitly.
func getMachineID() (string, error) {
	switch runtime.GOOS {
	case "linux":
		return getLinuxMachineID()
	case "darwin":
		return getDarwinMachineID()
	case "windows":
		return getWindowsMachineID()
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func getLinuxMachineID() (string, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		data, err = os.ReadFile("/var/lib/dbus/machine-id")
		if err != nil {
			return "", fmt.Errorf("linux machine-id not found: %w", err)
		}
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("linux machine-id is empty")
	}
	return id, nil
}

// getDarwinMachineID extracts IOPlatformUUID from ioreg output.
func getDarwinMachineID() (string, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("ioreg command failed: %w", err)
	}
	// Parse: "IOPlatformUUID" = "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX"
	re := regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)
	matches := re.FindSubmatch(out)
	if len(matches) < 2 {
		return "", fmt.Errorf("IOPlatformUUID not found in ioreg output")
	}
	id := strings.TrimSpace(string(matches[1]))
	if id == "" {
		return "", fmt.Errorf("IOPlatformUUID is empty")
	}
	return id, nil
}

// getWindowsMachineID reads MachineGuid from Windows registry.
func getWindowsMachineID() (string, error) {
	out, err := exec.Command("reg", "query",
		`HKLM\SOFTWARE\Microsoft\Cryptography`,
		"/v", "MachineGuid").Output()
	if err != nil {
		return "", fmt.Errorf("registry query failed: %w", err)
	}
	// Output format: "    MachineGuid    REG_SZ    <guid>"
	re := regexp.MustCompile(`MachineGuid\s+REG_SZ\s+(\S+)`)
	matches := re.FindSubmatch(out)
	if len(matches) < 2 {
		return "", fmt.Errorf("MachineGuid not found in registry output")
	}
	id := strings.TrimSpace(string(matches[1]))
	if id == "" {
		return "", fmt.Errorf("MachineGuid is empty")
	}
	return id, nil
}
