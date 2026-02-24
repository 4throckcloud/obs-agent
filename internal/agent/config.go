package agent

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/4throck/obs-agent/internal/crypto"
)

// Config holds agent configuration
type Config struct {
	RelayURL string `json:"relay_url"`
	Token    string `json:"token"`
	OBSHost  string `json:"obs_host"`
	OBSPort  int    `json:"obs_port"`
	OBSPass  string `json:"obs_pass"`
	Version  string `json:"-"`
}

// configFile is the on-disk format (password encrypted)
type configFile struct {
	RelayURL     string `json:"relay_url"`
	Token        string `json:"token"`
	OBSHost      string `json:"obs_host"`
	OBSPort      int    `json:"obs_port"`
	OBSPassEnc   string `json:"obs_pass_enc,omitempty"`
}

// LoadConfig reads and decrypts a config file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cf configFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, err
	}

	cfg := &Config{
		RelayURL: cf.RelayURL,
		Token:    cf.Token,
		OBSHost:  cf.OBSHost,
		OBSPort:  cf.OBSPort,
	}

	// Decrypt OBS password if present
	if cf.OBSPassEnc != "" {
		key, err := crypto.DeriveKey(cf.Token)
		if err != nil {
			return nil, fmt.Errorf("cannot decrypt config (machine ID required): %w", err)
		}
		pass, err := crypto.Decrypt(key, cf.OBSPassEnc)
		if err != nil {
			return nil, err
		}
		cfg.OBSPass = pass
	}

	return cfg, nil
}

// SaveConfig encrypts and saves config to disk
func SaveConfig(path string, cfg *Config) error {
	cf := configFile{
		RelayURL: cfg.RelayURL,
		Token:    cfg.Token,
		OBSHost:  cfg.OBSHost,
		OBSPort:  cfg.OBSPort,
	}

	// Encrypt OBS password
	if cfg.OBSPass != "" {
		key, err := crypto.DeriveKey(cfg.Token)
		if err != nil {
			return fmt.Errorf("cannot encrypt config (machine ID required): %w", err)
		}
		enc, err := crypto.Encrypt(key, cfg.OBSPass)
		if err != nil {
			return err
		}
		cf.OBSPassEnc = enc
	}

	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}
