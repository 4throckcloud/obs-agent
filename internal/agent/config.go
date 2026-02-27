package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/4throck/obs-agent/internal/crypto"
)

// configHeader identifies the encrypted config format on disk.
// Files starting with this header are machine-locked encrypted blobs.
const configHeader = "OBSAGENT2\n"

// Config holds agent configuration (runtime only, never serialized directly)
type Config struct {
	RelayURL string // hardcoded in binary, never stored on disk
	Token    string
	OBSHost  string
	OBSPort  int
	OBSPass  string
	Version  string
}

// configData is the internal structure encrypted on disk.
// Never visible as JSON to users — the file is an opaque binary blob.
// OBSHost and RelayURL are NOT stored — they are hardcoded in the binary.
type configData struct {
	Token   string `json:"token"`
	OBSPort int    `json:"obs_port"`
	OBSPass string `json:"obs_pass,omitempty"`
}

// legacyConfigFile is the old plaintext JSON format (migration only)
type legacyConfigFile struct {
	RelayURL   string `json:"relay_url"`
	Token      string `json:"token"`
	OBSHost    string `json:"obs_host"`
	OBSPort    int    `json:"obs_port"`
	OBSPassEnc string `json:"obs_pass_enc,omitempty"`
}

// LoadConfig reads and decrypts a config file.
// Handles both the new encrypted format and legacy plaintext JSON.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// New encrypted format
	if bytes.HasPrefix(data, []byte(configHeader)) {
		return loadEncrypted(data)
	}

	// Legacy plaintext JSON (auto-migrates on next save)
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return loadLegacy(data)
	}

	return nil, fmt.Errorf("unrecognized config format")
}

func loadEncrypted(data []byte) (*Config, error) {
	payload := data[len(configHeader):]
	encoded := strings.TrimSpace(string(payload))

	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("config decode failed: %w", err)
	}

	key, err := crypto.DeriveStorageKey()
	if err != nil {
		return nil, fmt.Errorf("cannot derive key: %w", err)
	}

	plaintext, err := crypto.DecryptBytes(key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("config decryption failed (wrong machine?): %w", err)
	}

	var cd configData
	if err := json.Unmarshal(plaintext, &cd); err != nil {
		return nil, fmt.Errorf("config parse failed: %w", err)
	}

	return &Config{
		Token:   cd.Token,
		OBSPort: cd.OBSPort,
		OBSPass: cd.OBSPass,
	}, nil
}

func loadLegacy(data []byte) (*Config, error) {
	var lf legacyConfigFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}

	cfg := &Config{
		Token:   lf.Token,
		OBSPort: lf.OBSPort,
	}

	// Decrypt OBS password using old token-based key
	if lf.OBSPassEnc != "" && lf.Token != "" {
		key, err := crypto.DeriveKey(lf.Token)
		if err == nil {
			if pass, err := crypto.Decrypt(key, lf.OBSPassEnc); err == nil {
				cfg.OBSPass = pass
			}
		}
	}

	return cfg, nil
}

// SaveConfig encrypts and saves config as an opaque machine-locked blob.
// The relay URL is never stored — it is hardcoded in the binary.
func SaveConfig(path string, cfg *Config) error {
	cd := configData{
		Token:   cfg.Token,
		OBSPort: cfg.OBSPort,
		OBSPass: cfg.OBSPass,
	}

	plaintext, err := json.Marshal(cd)
	if err != nil {
		return err
	}

	key, err := crypto.DeriveStorageKey()
	if err != nil {
		return fmt.Errorf("cannot derive key: %w", err)
	}

	ciphertext, err := crypto.EncryptBytes(key, plaintext)
	if err != nil {
		return err
	}

	encoded := base64.StdEncoding.EncodeToString(ciphertext)

	var buf bytes.Buffer
	buf.WriteString(configHeader)
	buf.WriteString(encoded)
	buf.WriteByte('\n')

	return os.WriteFile(path, buf.Bytes(), 0600)
}
