package service

// Install registers the agent as a startup service for the current OS.
// binaryPath is the absolute path to the agent binary.
// configPath is the absolute path to the config file (may be empty).
func Install(binaryPath, configPath string) error {
	return install(binaryPath, configPath)
}

// Uninstall removes the agent startup service.
func Uninstall() error {
	return uninstall()
}

// IsInstalled returns whether the startup service is currently registered.
func IsInstalled() bool {
	return isInstalled()
}
