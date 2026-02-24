package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/4throck/obs-agent/internal/agent"
	"github.com/4throck/obs-agent/internal/ui"
	"github.com/gorilla/websocket"
)

var Version = "dev"

// SECURITY: token format — exactly 64 hex chars (256-bit)
var tokenRegex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// wizard is the UI implementation used for setup and fatal errors
var wizard ui.UI

func main() {
	var (
		relayURL    string
		token       string
		obsHost     string
		obsPort     int
		obsPass     string
		configFile  string
		showVersion bool
		setup       bool
	)

	flag.StringVar(&relayURL, "relay", "wss://4throck.cloud/ws/agent", "Relay server URL")
	flag.StringVar(&token, "token", "", "Agent authentication token")
	flag.StringVar(&obsHost, "obs-host", "localhost", "Local OBS WebSocket host")
	flag.IntVar(&obsPort, "obs-port", 4455, "Local OBS WebSocket port")
	flag.StringVar(&obsPass, "obs-pass", "", "Local OBS WebSocket password")
	flag.StringVar(&configFile, "config", "", "Config file path (optional, overrides flags)")
	flag.BoolVar(&showVersion, "version", false, "Show version")
	flag.BoolVar(&setup, "setup", false, "Run interactive setup wizard")
	flag.Parse()

	if showVersion {
		fmt.Printf("obs-agent %s\n", Version)
		os.Exit(0)
	}

	// Select UI implementation: native OS dialogs or CLI fallback
	if ui.IsGuiAvailable() {
		wizard = ui.NewGuiUI()
	} else {
		wizard = ui.NewCliUI()
	}

	// Set up file logging (next to the binary)
	setupFileLogging()

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

	// Try loading config from explicit path or default location
	configPath := configFile
	if configPath == "" {
		configPath = defaultConfigPath
	}
	if configPath != "" {
		loaded, err := agent.LoadConfig(configPath)
		if err != nil {
			if configFile != "" {
				// Explicit config file specified but failed — warn
				log.Printf("[agent] Warning: could not load config file: %v", err)
			}
			// Default config not found is fine — will prompt for setup
		} else {
			if !isFlagSet("relay") && loaded.RelayURL != "" {
				cfg.RelayURL = loaded.RelayURL
			}
			if !isFlagSet("token") && loaded.Token != "" {
				cfg.Token = loaded.Token
			}
			if !isFlagSet("obs-host") && loaded.OBSHost != "" {
				cfg.OBSHost = loaded.OBSHost
			}
			if !isFlagSet("obs-port") && loaded.OBSPort != 0 {
				cfg.OBSPort = loaded.OBSPort
			}
			if !isFlagSet("obs-pass") && loaded.OBSPass != "" {
				cfg.OBSPass = loaded.OBSPass
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

	// If no token and running interactively, start setup wizard
	if cfg.Token == "" || setup {
		detected := autoDetectOBS()
		runSetup(wizard, cfg, defaultConfigPath, detected)
	}

	// Validate token
	if cfg.Token == "" {
		fatalWait("[agent] Token is required. Use -token flag, config file, or OBS_AGENT_TOKEN env var")
	}
	if !tokenRegex.MatchString(cfg.Token) {
		fatalWait("[agent] Invalid token format. Token must be 64 hex characters.")
	}

	// SECURITY: Never log the token or OBS password
	log.Printf("[agent] obs-agent %s starting", Version)
	log.Printf("[agent] Relay: %s", cfg.RelayURL)
	log.Printf("[agent] OBS target: %s:%d", cfg.OBSHost, cfg.OBSPort)
	log.Printf("[agent] Token: %s...%s (verified format)", cfg.Token[:4], cfg.Token[60:])

	a := agent.New(cfg)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[agent] Shutting down...")
		a.Stop()
	}()

	if err := a.Start(); err != nil {
		fatalWait(fmt.Sprintf("[agent] Fatal: %v", err))
	}
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

// defaultConfigFile returns the config file path next to the binary
func defaultConfigFile() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "obs-agent.json")
}

// setupFileLogging opens obs-agent.log next to the binary for persistent logging.
// On Windows (GUI mode), log only to file. On other OS, log to both stderr and file.
func setupFileLogging() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(exe), "obs-agent.log")
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

// runSetup runs the interactive setup wizard using the provided UI.
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

	// Pre-fill from auto-detect
	defaultHost := cfg.OBSHost
	defaultPort := cfg.OBSPort
	if detected != nil {
		defaultHost = detected.Host
		defaultPort = detected.Port
	}

	// OBS host
	val, ok := w.Entry("OBS Connection", "OBS WebSocket host", defaultHost)
	if ok && strings.TrimSpace(val) != "" {
		cfg.OBSHost = strings.TrimSpace(val)
	} else if ok {
		cfg.OBSHost = defaultHost
	}

	// OBS port
	val, ok = w.Entry("OBS Connection", "OBS WebSocket port", strconv.Itoa(defaultPort))
	if ok && strings.TrimSpace(val) != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && p > 0 && p < 65536 {
			cfg.OBSPort = p
		}
	} else if ok {
		cfg.OBSPort = defaultPort
	}

	// OBS password
	pw, ok := w.Password("OBS Connection", "OBS WebSocket password (leave blank if none)")
	if ok && strings.TrimSpace(pw) != "" {
		cfg.OBSPass = strings.TrimSpace(pw)
	}

	// Save config
	if savePath != "" {
		if w.Confirm("Save Config", fmt.Sprintf("Save config to %s?", savePath)) {
			if err := agent.SaveConfig(savePath, cfg); err != nil {
				w.Error("Save Failed", fmt.Sprintf("Could not save config: %v", err))
			} else {
				w.Info("Config Saved", "Config saved. Next time just double-click to run.")
			}
		}
	}
}
