package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/4throck/obs-agent/internal/agent"
	"github.com/4throck/obs-agent/internal/branding"
	"github.com/4throck/obs-agent/internal/device"
	"github.com/4throck/obs-agent/internal/instance"
	"github.com/4throck/obs-agent/internal/integrity"
	"github.com/4throck/obs-agent/internal/service"
	"github.com/4throck/obs-agent/internal/status"
	"github.com/4throck/obs-agent/internal/tunnel"
	"github.com/4throck/obs-agent/internal/ui"
	"github.com/gorilla/websocket"
)

var Version = "dev"

// SECURITY: token format — exactly 64 hex chars (256-bit)
var tokenRegex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// wizard is the UI implementation used for setup and fatal errors
var wizard ui.UI

func main() {
	// Relay URL is hardcoded — not configurable by users
	const relayURL = "wss://4throck.cloud/ws/agent"

	// OBS host defaults to localhost; Docker sets OBS_HOST=host.docker.internal
	obsHost := "localhost"
	if h := os.Getenv("OBS_HOST"); h != "" {
		obsHost = h
	}

	var (
		token   string
		obsPort int
		obsPass string
		configFile     string
		showVersion    bool
		setup          bool
		verify         bool
		queryStatus    bool
		installService bool
		uninstallSvc   bool
	)

	flag.StringVar(&token, "token", "", "Agent authentication token")
	flag.IntVar(&obsPort, "obs-port", 4455, "Local OBS WebSocket port")
	flag.StringVar(&obsPass, "obs-pass", "", "Local OBS WebSocket password")
	flag.StringVar(&configFile, "config", "", "Config file path (optional, overrides flags)")
	flag.BoolVar(&showVersion, "version", false, "Show version")
	flag.BoolVar(&setup, "setup", false, "Run interactive setup wizard")
	flag.BoolVar(&verify, "verify", false, "Verify binary integrity against manifest")
	flag.BoolVar(&queryStatus, "status", false, "Query running agent status")
	flag.BoolVar(&installService, "install", false, "Install as startup service")
	flag.BoolVar(&uninstallSvc, "uninstall", false, "Uninstall startup service")
	flag.Parse()

	// 1. -version → print version, exit
	if showVersion {
		fmt.Printf("obs-agent %s\n", Version)
		os.Exit(0)
	}

	// 2. -verify → verbose integrity check, exit
	if verify {
		runVerify()
		return
	}

	// 3. -status → query running agent, pretty-print, exit
	if queryStatus {
		runStatusQuery()
		return
	}

	// 4. Select UI implementation: WebUI (branded browser wizard) wrapping native OS dialogs > CLI fallback
	if ui.IsGuiAvailable() {
		wizard = ui.NewWebUI(ui.NewGuiUI())
	} else {
		wizard = ui.NewCliUI()
	}

	// 5. Set up file logging (next to the binary)
	setupFileLogging()

	// 6. Print branded banner
	branding.PrintBanner(Version, runtime.GOOS, runtime.GOARCH, os.Stderr)
	// Also log to file (no ANSI)
	log.Printf("[agent] obs-agent %s (%s/%s) starting", Version, runtime.GOOS, runtime.GOARCH)

	// 7. Resolve binary directory (used for lock, service install)
	binaryDir := binaryDirectory()

	// 8. -install → install service, exit
	if installService {
		exe, _ := os.Executable()
		exe, _ = filepath.EvalSymlinks(exe)
		cfgPath := configFile
		if cfgPath == "" {
			cfgPath = defaultConfigFile()
		}
		if err := service.Install(exe, cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "Install failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Startup service installed. The agent will start automatically on login.")
		return
	}

	// 9. -uninstall → uninstall service, exit
	if uninstallSvc {
		if err := service.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Startup service removed.")
		return
	}

	// 10. Acquire instance lock (fatal if another running)
	lock, err := instance.Acquire(binaryDir)
	if err != nil {
		fatalWait(fmt.Sprintf("[agent] %v", err))
	}

	// Determine default config path (next to binary)
	defaultConfigPath := defaultConfigFile()

	cfg := &agent.Config{
		RelayURL: relayURL,
		Token:    token,
		OBSHost:  obsHost,
		OBSPort:  obsPort,
		OBSPass:  obsPass,
		Version:  Version,
	}

	// 11. Try loading config from explicit path or default location
	// Also check for legacy obs-agent.json and migrate if found
	configPath := configFile
	if configPath == "" {
		configPath = defaultConfigPath
		// If new .dat doesn't exist, try legacy .json for migration
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if legacyPath := legacyConfigFile(); legacyPath != "" {
				if _, err := os.Stat(legacyPath); err == nil {
					configPath = legacyPath
				}
			}
		}
	}
	var configLoaded bool
	if configPath != "" {
		loaded, err := agent.LoadConfig(configPath)
		if err != nil {
			if configFile != "" {
				// Explicit config file specified but failed — warn
				log.Printf("[agent] Warning: could not load config file: %v", err)
			}
			// Default config not found is fine — will prompt for setup
		} else {
			configLoaded = true
			// relay_url and obs_host are never loaded from config — hardcoded in binary
			if !isFlagSet("token") && loaded.Token != "" {
				cfg.Token = loaded.Token
			}
			if !isFlagSet("obs-port") && loaded.OBSPort != 0 {
				cfg.OBSPort = loaded.OBSPort
			}
			if !isFlagSet("obs-pass") && loaded.OBSPass != "" {
				cfg.OBSPass = loaded.OBSPass
			}
			// Migrate legacy JSON config to encrypted format
			if configPath != defaultConfigPath && configLoaded {
				if err := agent.SaveConfig(defaultConfigPath, cfg); err == nil {
					log.Printf("[agent] Migrated config to encrypted format: %s", defaultConfigPath)
					os.Remove(configPath) // delete old plaintext JSON
				}
			}
		}
	}

	// Environment variable fallbacks
	if cfg.Token == "" {
		cfg.Token = os.Getenv("OBS_AGENT_TOKEN")
	}
	if cfg.OBSPass == "" {
		cfg.OBSPass = os.Getenv("OBS_PASSWORD")
	}

	// 12. Start status server early — the WebUI wizard runs on it (no separate server)
	statusSrv := status.New(Version, cfg.OBSHost, cfg.OBSPort, cfg.RelayURL)
	statusSrv.Start()

	// Wire WebUI to use the status server for wizard endpoints
	if webUI, ok := wizard.(*ui.WebUI); ok {
		webUI.SetStatusServer(statusSrv)
	}

	// 13. If no token: run wizard (interactive) or fail fast (headless/Docker)
	var wizardRan bool
	if cfg.Token == "" || setup {
		if !ui.IsGuiAvailable() && !isTerminal() && !setup {
			// Headless mode (Docker, service) — no wizard possible
			statusSrv.Stop()
			lock.Release()
			fmt.Fprintln(os.Stderr, "ERROR: TOKEN is required.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  1. Go to your 4thRock dashboard → OBS Control → Add Agent")
			fmt.Fprintln(os.Stderr, "  2. Copy the token")
			fmt.Fprintln(os.Stderr, "  3. Run: docker run -d -e TOKEN=<your-token> ghcr.io/4throckcloud/obs-agent:latest")
			os.Exit(1)
		}
		wizardRan = true
		detected := autoDetectOBS()
		runWizardSetup(wizard, cfg, defaultConfigPath, detected, setup)
	}

	// 14. Validate token
	if cfg.Token == "" {
		statusSrv.Stop()
		lock.Release()
		fatalWait("[agent] Token is required. Use -token flag, config file, or OBS_AGENT_TOKEN env var")
	}
	if !tokenRegex.MatchString(cfg.Token) {
		statusSrv.Stop()
		lock.Release()
		fatalWait("[agent] Invalid token format. Token must be 64 hex characters.")
	}

	// SECURITY: Never log the token or OBS password
	log.Printf("[agent] Relay: %s", cfg.RelayURL)
	log.Printf("[agent] OBS target: %s:%d", cfg.OBSHost, cfg.OBSPort)
	log.Printf("[agent] Token: %s...%s (verified format)", cfg.Token[:4], cfg.Token[60:])

	// 15. Silent integrity check (background goroutine)
	go func() {
		result, err := integrity.Verify("")
		if err != nil {
			log.Printf("[integrity] Skipped: %v", err)
			return
		}
		if result.Match {
			log.Printf("[integrity] Binary verified (SHA256 matches %s manifest)", result.Version)
		} else {
			log.Printf("[integrity] WARNING: SHA256 mismatch — binary may be modified or outdated")
		}
	}()

	// 16. Create agent, update status server with final config (may have changed during setup)
	statusSrv.UpdateConfig(cfg.OBSHost, cfg.OBSPort, cfg.RelayURL)
	a := agent.New(cfg)
	a.StatusServer = statusSrv

	// Wire status server callbacks
	var reconfigureRequested bool
	var reconfigureMu sync.Mutex

	statusSrv.SetQuitHandler(func() {
		log.Println("[status] Quit requested via dashboard")
		a.Stop()
	})

	statusSrv.SetReconfigureHandler(func() {
		log.Println("[status] Reconfigure requested via dashboard")
		reconfigureMu.Lock()
		reconfigureRequested = true
		reconfigureMu.Unlock()
		a.Stop()
	})

	// Desktop notification debouncing (30s per event type)
	var notifyMu sync.Mutex
	notifyLast := map[string]time.Time{}

	statusSrv.SetStateChangeHandler(func(event, message string) {
		notifyMu.Lock()
		last, ok := notifyLast[event]
		now := time.Now()
		if ok && now.Sub(last) < 30*time.Second {
			notifyMu.Unlock()
			return
		}
		notifyLast[event] = now
		notifyMu.Unlock()
		ui.Notify("4thRock OBS Agent", message)
	})

	// Auto-open status dashboard in browser (GUI mode only).
	// Skip if wizard already opened a tab — the merged page transitions
	// from setup to status inline without needing a second tab.
	if !wizardRan && ui.IsGuiAvailable() && statusSrv.Port() > 0 {
		_ = device.OpenBrowser(fmt.Sprintf("https://agent.4throck.cloud/status?port=%d", statusSrv.Port()))
	}

	// 17. Signal handler (release lock, stop status, stop agent)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[agent] Shutting down...")
		a.Stop()
	}()

	// 18. a.Start() (blocking reconnection loop)
	if err := a.Start(); err != nil {
		// Token rejected — auto-trigger device auth to get a new valid token
		if _, ok := err.(*tunnel.ErrTokenRejected); ok {
			log.Println("[agent] Token rejected — starting device authorization...")
			handleTokenRejected(wizard, cfg, defaultConfigPath, statusSrv, lock)
			return
		}

		// Check if this was a reconfigure request
		reconfigureMu.Lock()
		reconfig := reconfigureRequested
		reconfigureMu.Unlock()

		if reconfig {
			log.Println("[agent] Restarting for reconfiguration...")
			handleReconfigure(wizard, cfg, defaultConfigPath, statusSrv, lock, a)
			return
		}

		statusSrv.Stop()
		lock.Release()
		fatalWait(fmt.Sprintf("[agent] Fatal: %v", err))
	}

	// Check if agent stopped due to reconfigure
	reconfigureMu.Lock()
	reconfig := reconfigureRequested
	reconfigureMu.Unlock()

	if reconfig {
		log.Println("[agent] Restarting for reconfiguration...")
		handleReconfigure(wizard, cfg, defaultConfigPath, statusSrv, lock, a)
		return
	}

	statusSrv.Stop()
	lock.Release()
}

// handleReconfigure runs the OBS wizard to reconfigure, then restarts the agent.
func handleReconfigure(w ui.UI, cfg *agent.Config, savePath string, statusSrv *status.Server, lock *instance.Lock, oldAgent *agent.Agent) {
	detected := autoDetectOBS()

	if runner, ok := w.(ui.WizardRunner); ok {
		wizCfg := ui.WizardConfig{
			RelayURL:      cfg.RelayURL,
			Version:       Version,
			DefaultHost:   cfg.OBSHost,
			DefaultPort:   cfg.OBSPort,
			OBSDetected:   detected != nil,
			SavePath:      savePath,
			ExistingToken: cfg.Token,
		}
		if detected != nil {
			wizCfg.DefaultHost = detected.Host
			wizCfg.DefaultPort = detected.Port
		}

		result, err := runner.RunOBSWizard(wizCfg)
		if err != nil {
			log.Printf("[agent] Reconfiguration wizard failed: %v", err)
			statusSrv.Stop()
			lock.Release()
			fatalWait(fmt.Sprintf("[agent] Reconfiguration failed: %v", err))
			return
		}

		cfg.OBSHost = result.OBSHost
		cfg.OBSPort = result.OBSPort
		cfg.OBSPass = result.OBSPass
		if result.Token != "" {
			cfg.Token = result.Token
		}
	} else {
		// CLI fallback
		collectOBSSettings(w, cfg, detected)
		autoSaveConfig(w, savePath, cfg)
	}

	// Restart agent with new config on the same status server
	log.Printf("[agent] Restarting with new OBS target: %s:%d", cfg.OBSHost, cfg.OBSPort)
	statusSrv.UpdateConfig(cfg.OBSHost, cfg.OBSPort, cfg.RelayURL)

	newAgent := agent.New(cfg)
	newAgent.StatusServer = statusSrv

	// No need to open a new browser tab — the wizard page transitions
	// to status view inline after reconfiguration completes.

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[agent] Shutting down...")
		newAgent.Stop()
	}()

	if err := newAgent.Start(); err != nil {
		statusSrv.Stop()
		lock.Release()
		fatalWait(fmt.Sprintf("[agent] Fatal: %v", err))
	}

	statusSrv.Stop()
	lock.Release()
}

// handleTokenRejected clears the bad token, runs device auth to get a new one, and restarts.
func handleTokenRejected(w ui.UI, cfg *agent.Config, savePath string, statusSrv *status.Server, lock *instance.Lock) {
	// Clear the rejected token and delete old config
	cfg.Token = ""
	os.Remove(savePath)

	// Run device auth to get a new valid token
	detected := autoDetectOBS()
	runWizardSetup(w, cfg, savePath, detected, false)

	if cfg.Token == "" || !tokenRegex.MatchString(cfg.Token) {
		statusSrv.Stop()
		lock.Release()
		fatalWait("[agent] Re-authentication failed — no valid token obtained")
		return
	}

	log.Printf("[agent] Re-authenticated successfully, restarting...")
	statusSrv.UpdateConfig(cfg.OBSHost, cfg.OBSPort, cfg.RelayURL)

	newAgent := agent.New(cfg)
	newAgent.StatusServer = statusSrv

	// No need to open a new browser tab — the wizard page transitions
	// to status view inline after re-authentication completes.

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[agent] Shutting down...")
		newAgent.Stop()
	}()

	if err := newAgent.Start(); err != nil {
		statusSrv.Stop()
		lock.Release()
		fatalWait(fmt.Sprintf("[agent] Fatal: %v", err))
	}

	statusSrv.Stop()
	lock.Release()
}

// runWizardSetup runs the appropriate wizard flow for initial setup.
// If the wizard implements WizardRunner (WebUI), it uses the branded browser wizard.
// Otherwise it falls back to the CLI/GUI dialog flow.
func runWizardSetup(w ui.UI, cfg *agent.Config, savePath string, detected *obsDetectResult, forceSetup bool) {
	if runner, ok := w.(ui.WizardRunner); ok {
		wizCfg := ui.WizardConfig{
			RelayURL:    cfg.RelayURL,
			Version:     Version,
			DefaultHost: cfg.OBSHost,
			DefaultPort: cfg.OBSPort,
			OBSDetected: detected != nil,
			SavePath:    savePath,
		}
		if detected != nil {
			wizCfg.DefaultHost = detected.Host
			wizCfg.DefaultPort = detected.Port
		}

		var result *ui.WizardResult
		var err error

		if cfg.Token == "" && forceSetup {
			// -setup flag: manual token entry
			result, err = runner.RunManualWizard(wizCfg)
		} else if cfg.Token == "" {
			// No token: device auth flow
			result, err = runner.RunDeviceWizard(wizCfg)
		} else {
			// Has token, -setup flag: OBS-only reconfigure
			wizCfg.ExistingToken = cfg.Token
			result, err = runner.RunOBSWizard(wizCfg)
		}

		if err != nil {
			fatalWait(fmt.Sprintf("[agent] Setup wizard failed: %v", err))
		}

		cfg.Token = result.Token
		cfg.OBSHost = result.OBSHost
		cfg.OBSPort = result.OBSPort
		cfg.OBSPass = result.OBSPass
		return
	}

	// CLI fallback — existing dialog-based flow
	if cfg.Token == "" && forceSetup {
		runSetup(w, cfg, savePath, detected)
	} else if cfg.Token == "" {
		if err := runDeviceAuth(w, cfg, savePath, detected); err != nil {
			fatalWait(fmt.Sprintf("[agent] Device authorization failed: %v\nRun with -setup flag for manual token entry.", err))
		}
	} else {
		runSetup(w, cfg, savePath, detected)
	}
}

// runVerify performs a verbose integrity check and exits.
func runVerify() {
	fmt.Println("Computing binary SHA256...")
	hash, err := integrity.SelfHash()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Local SHA256: %s\n\n", hash)

	fmt.Printf("Fetching manifest from %s...\n", integrity.DefaultManifestURL)
	result, err := integrity.Verify("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Verification failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Manifest version: %s\n", result.Version)
	fmt.Printf("Expected SHA256:  %s\n", result.Expected)
	fmt.Printf("Actual SHA256:    %s\n", result.Actual)
	if result.Match {
		fmt.Println("\nResult: PASS — binary matches manifest")
	} else {
		fmt.Println("\nResult: FAIL — binary does NOT match manifest")
		os.Exit(1)
	}
}

// runStatusQuery fetches status from a running agent and pretty-prints it.
func runStatusQuery() {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + status.DefaultAddr + "/api/status")
	if err != nil {
		fmt.Fprintf(os.Stderr, "No agent running (could not connect to %s)\n", status.DefaultAddr)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	out, _ := json.MarshalIndent(data, "", "  ")
	fmt.Println(string(out))
}

func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// isTerminal returns true if stdin is attached to a terminal (not Docker detached / piped).
func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// binaryDirectory returns the directory containing the running binary.
func binaryDirectory() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// defaultConfigFile returns the config file path next to the binary
func defaultConfigFile() string {
	dir := binaryDirectory()
	if dir == "." {
		return ""
	}
	return filepath.Join(dir, "obs-agent.dat")
}

// legacyConfigFile returns the old JSON config path (for migration)
func legacyConfigFile() string {
	dir := binaryDirectory()
	if dir == "." {
		return ""
	}
	return filepath.Join(dir, "obs-agent.json")
}

// setupFileLogging opens obs-agent.log next to the binary for persistent logging.
// On Windows (GUI mode), log only to file. On other OS, log to both stderr and file.
func setupFileLogging() {
	dir := binaryDirectory()
	if dir == "." {
		return
	}
	logPath := filepath.Join(dir, "obs-agent.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	if runtime.GOOS == "windows" {
		// Windows GUI mode — no console, log to file only
		log.SetOutput(f)
	} else {
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
}

// obsDetectResult holds auto-detected OBS WebSocket connection info
type obsDetectResult struct {
	Host string
	Port int
}

// autoDetectOBS scans common OBS WebSocket ports on localhost.
// Returns detected host/port or nil if OBS is not found.
func autoDetectOBS() *obsDetectResult {
	ports := []int{4455, 4454, 4456}
	for _, port := range ports {
		addr := fmt.Sprintf("localhost:%d", port)
		// Quick TCP check first
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			continue
		}
		conn.Close()

		// Try WebSocket handshake and read Hello (op 0)
		dialer := &websocket.Dialer{HandshakeTimeout: 2 * time.Second}
		ws, _, err := dialer.Dial(fmt.Sprintf("ws://%s", addr), nil)
		if err != nil {
			continue
		}

		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := ws.ReadMessage()
		ws.Close()
		if err != nil {
			continue
		}

		var msg struct {
			Op int `json:"op"`
			D  struct {
				ObsWebSocketVersion string `json:"obsWebSocketVersion"`
			} `json:"d"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Op == 0 && msg.D.ObsWebSocketVersion != "" {
			log.Printf("[agent] Auto-detected OBS WebSocket v%s on port %d", msg.D.ObsWebSocketVersion, port)
			return &obsDetectResult{Host: "localhost", Port: port}
		}
	}
	return nil
}

// fatalWait shows an error via GUI dialog or stderr, then exits.
func fatalWait(msg string) {
	log.Println(msg)
	if wizard != nil && ui.IsGuiAvailable() {
		wizard.Error("OBS Agent Error", msg)
	} else {
		fmt.Fprintln(os.Stderr, msg)
	}
	os.Exit(1)
}

// relayToHTTPS derives the HTTPS base URL from the relay WebSocket URL.
// e.g. "wss://4throck.cloud/ws/agent" → "https://4throck.cloud"
func relayToHTTPS(relayURL string) string {
	u := relayURL
	u = strings.Replace(u, "wss://", "https://", 1)
	u = strings.Replace(u, "ws://", "http://", 1)
	// Strip path — find first '/' after "://"
	if idx := strings.Index(u, "://"); idx >= 0 {
		pathStart := strings.Index(u[idx+3:], "/")
		if pathStart >= 0 {
			return u[:idx+3+pathStart]
		}
	}
	return u
}

// runDeviceAuth performs the browser-based device authorization flow (CLI fallback).
func runDeviceAuth(w ui.UI, cfg *agent.Config, savePath string, detected *obsDetectResult) error {
	ctx := context.Background()
	baseURL := relayToHTTPS(cfg.RelayURL)

	// Prompt for agent name
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "My Agent"
	}
	agentName, ok := w.Entry("Agent Name", "Name for this agent (shown in your dashboard)", hostname)
	if !ok {
		os.Exit(0)
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = hostname
	}

	flow := &device.Flow{
		BaseURL: baseURL,
		Version: Version,
	}

	// Request device code
	log.Println("[agent] Requesting device authorization...")
	code, err := flow.RequestCode(ctx, agentName)
	if err != nil {
		return fmt.Errorf("could not start device authorization: %w", err)
	}

	// Check if machine already has an active token
	if code.Status == "already_authorized" && code.Token != "" {
		log.Printf("[agent] Machine already authorized as %q — reconnecting", code.AgentName)
		cfg.Token = code.Token
		w.Info("Already Authorized", fmt.Sprintf("This machine is already authorized as %q.\nReconnecting...", code.AgentName))
	} else {
		// Open browser
		verifyURL := code.VerificationURL
		if browserErr := device.OpenBrowser(verifyURL); browserErr != nil {
			log.Printf("[agent] Could not open browser: %v", browserErr)
		}

		// Show user the code and URL
		msg := fmt.Sprintf(
			"A browser window should open.\n\nIf not, go to:\n%s\n\nVerification code: %s\n\nWaiting for approval...",
			verifyURL, code.UserCode,
		)
		log.Printf("[agent] Verification URL: %s", verifyURL)
		log.Printf("[agent] User code: %s", code.UserCode)

		// Non-blocking info display (CLI prints to stderr, GUI shows dialog)
		go w.Info("Sign In to Authorize", msg)

		// Poll for approval
		token, err := flow.PollForToken(ctx, code.DeviceCode, code.Interval)
		if err != nil {
			return err
		}

		cfg.Token = token
		log.Println("[agent] Device authorized successfully!")
	}

	// Prompt for OBS connection settings in a single form
	collectOBSSettings(w, cfg, detected)

	// Auto-save config
	autoSaveConfig(w, savePath, cfg)

	return nil
}

// runSetup runs the interactive setup wizard using the provided UI (CLI fallback).
func runSetup(w ui.UI, cfg *agent.Config, savePath string, detected *obsDetectResult) {
	// Token
	if cfg.Token == "" {
		for {
			val, ok := w.Entry(
				"OBS Agent Setup",
				"Paste your 64-character agent token\n(from OBS Control Panel → Agent → Tokens)",
				"",
			)
			if !ok {
				os.Exit(0)
			}
			val = strings.TrimSpace(val)
			if tokenRegex.MatchString(val) {
				cfg.Token = val
				break
			}
			w.Error("Invalid Token", "Token must be exactly 64 hex characters. Try again.")
		}
	}

	// Prompt for OBS connection settings in a single form
	collectOBSSettings(w, cfg, detected)

	// Auto-save config
	autoSaveConfig(w, savePath, cfg)
}

// collectOBSSettings shows a single form dialog for OBS host, port, and password.
func collectOBSSettings(w ui.UI, cfg *agent.Config, detected *obsDetectResult) {
	defaultHost := cfg.OBSHost
	defaultPort := cfg.OBSPort
	if detected != nil {
		defaultHost = detected.Host
		defaultPort = detected.Port
	}

	fields := []ui.FormField{
		{Label: "OBS WebSocket host", Key: "host", Default: defaultHost},
		{Label: "OBS WebSocket port", Key: "port", Default: strconv.Itoa(defaultPort)},
		{Label: "OBS WebSocket password (blank if none)", Key: "password", Password: true},
	}

	values, ok := w.Form("OBS Connection", fields)
	if !ok {
		// User cancelled — keep defaults
		cfg.OBSHost = defaultHost
		cfg.OBSPort = defaultPort
		return
	}

	if h := strings.TrimSpace(values["host"]); h != "" {
		cfg.OBSHost = h
	} else {
		cfg.OBSHost = defaultHost
	}

	if p, err := strconv.Atoi(strings.TrimSpace(values["port"])); err == nil && p > 0 && p < 65536 {
		cfg.OBSPort = p
	} else {
		cfg.OBSPort = defaultPort
	}

	if pw := strings.TrimSpace(values["password"]); pw != "" {
		cfg.OBSPass = pw
	}
}

// autoSaveConfig saves the config file without prompting for confirmation.
func autoSaveConfig(w ui.UI, savePath string, cfg *agent.Config) {
	if savePath == "" {
		return
	}
	if err := agent.SaveConfig(savePath, cfg); err != nil {
		w.Error("Save Failed", fmt.Sprintf("Could not save config: %v", err))
	} else {
		log.Printf("[agent] Config saved to %s", savePath)
	}
}
