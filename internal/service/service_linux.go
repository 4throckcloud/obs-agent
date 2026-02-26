//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const serviceName = "obs-agent"

func unitDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func unitPath() string {
	return filepath.Join(unitDir(), serviceName+".service")
}

func install(binaryPath, configPath string) error {
	dir := unitDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}

	execStart := binaryPath
	if configPath != "" {
		execStart += " -config " + configPath
	}

	unit := strings.Join([]string{
		"[Unit]",
		"Description=4thRock OBS Agent",
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + execStart,
		"Restart=on-failure",
		"RestartSec=10",
		"",
		"[Install]",
		"WantedBy=default.target",
	}, "\n")

	if err := os.WriteFile(unitPath(), []byte(unit+"\n"), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	if err := exec.Command("systemctl", "--user", "enable", serviceName).Run(); err != nil {
		return fmt.Errorf("enable service: %w", err)
	}

	return nil
}

func uninstall() error {
	_ = exec.Command("systemctl", "--user", "stop", serviceName).Run()
	_ = exec.Command("systemctl", "--user", "disable", serviceName).Run()

	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func isInstalled() bool {
	_, err := os.Stat(unitPath())
	return err == nil
}
