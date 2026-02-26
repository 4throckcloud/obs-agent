package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"
)

const DefaultManifestURL = "https://media.4throck.cloud/agent/manifest.json"

// Result holds the outcome of an integrity verification.
type Result struct {
	Match    bool
	Expected string
	Actual   string
	Version  string
}

type manifest struct {
	Version string  `json:"version"`
	Builds  []build `json:"builds"`
}

type build struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	SHA256 string `json:"sha256"`
}

// SelfHash computes the SHA256 of the running binary.
func SelfHash() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}

	f, err := os.Open(exe)
	if err != nil {
		return "", fmt.Errorf("open executable: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash executable: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// Verify fetches the manifest and compares the SHA256 for this platform.
func Verify(manifestURL string) (*Result, error) {
	if manifestURL == "" {
		manifestURL = DefaultManifestURL
	}

	actual, err := SelfHash()
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}

	var m manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	for _, b := range m.Builds {
		if b.OS == runtime.GOOS && b.Arch == runtime.GOARCH {
			return &Result{
				Match:    b.SHA256 == actual,
				Expected: b.SHA256,
				Actual:   actual,
				Version:  m.Version,
			}, nil
		}
	}

	return nil, fmt.Errorf("no manifest entry for %s/%s", runtime.GOOS, runtime.GOARCH)
}
