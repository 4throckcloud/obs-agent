//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"
)

const taskName = "4thRockOBSAgent"

func install(binaryPath, configPath string) error {
	args := binaryPath
	if configPath != "" {
		args += " -config " + configPath
	}

	cmd := exec.Command("schtasks.exe",
		"/Create",
		"/SC", "ONLOGON",
		"/TN", taskName,
		"/TR", args,
		"/RL", "LIMITED",
		"/F",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks create: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstall() error {
	cmd := exec.Command("schtasks.exe", "/Delete", "/TN", taskName, "/F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks delete: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isInstalled() bool {
	err := exec.Command("schtasks.exe", "/Query", "/TN", taskName).Run()
	return err == nil
}
