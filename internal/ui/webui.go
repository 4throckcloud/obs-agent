package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/4throck/obs-agent/internal/agent"
	"github.com/4throck/obs-agent/internal/device"
	"github.com/4throck/obs-agent/internal/status"
	"github.com/gorilla/websocket"
)

// tokenPattern validates 64-char hex tokens
var tokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// allowedOrigins for CORS — the remote wizard/status pages on these origins can talk to the local API.
var allowedOrigins = map[string]bool{
	"https://agent.4throck.cloud": true,
	"https://4throck.cloud":       true,
	"https://www.4throck.cloud":   true,
}

// remoteBaseURL is the hosted agent site. Tried first; local fallback if unreachable.
const remoteBaseURL = "https://agent.4throck.cloud"

// WizardConfig holds input configuration for the setup wizard.
type WizardConfig struct {
	RelayURL      string
	Version       string
	DefaultHost   string
	DefaultPort   int
	OBSDetected   bool
	SavePath      string
	ExistingToken string // set when mode is "obs" (re-setup with existing token)
}

// WizardResult holds the values collected by the setup wizard.
// OBSHost is not included — it is hardcoded in the binary.
type WizardResult struct {
	Token   string
	OBSPort int
	OBSPass string
	Saved   bool
}

// WizardRunner can run a full web-based setup wizard.
type WizardRunner interface {
	RunDeviceWizard(cfg WizardConfig) (*WizardResult, error)
	RunManualWizard(cfg WizardConfig) (*WizardResult, error)
	RunOBSWizard(cfg WizardConfig) (*WizardResult, error)
}

// WebUI provides a branded web-based setup wizard.
// The wizard API runs on the status server at :8765 (JSON only, no HTML).
// Browser opens agent.4throck.cloud which hosts the UI and calls localhost via CORS.
type WebUI struct {
	fallback  UI
	statusSrv *status.Server

	mu     sync.Mutex
	mode   string // "device", "manual", "obs"
	wizCfg WizardConfig
	result *WizardResult
	doneCh chan struct{}

	// Device auth state
	deviceFlow *device.Flow
	deviceCode *device.CodeResponse
	authDone   chan struct{}
	authToken  string
	authErr    error
	pollCancel context.CancelFunc
}

// NewWebUI creates a web-based UI with a fallback for non-wizard dialogs.
func NewWebUI(fallback UI) *WebUI {
	return &WebUI{fallback: fallback}
}

// SetStatusServer wires the WebUI to use the given status server for its API.
// Registers wizard endpoints once. Must be called before any RunXxxWizard call.
func (w *WebUI) SetStatusServer(s *status.Server) {
	w.statusSrv = s
	s.HandleFunc("/api/wizard/state", corsWrap(w.handleState))
	s.HandleFunc("/api/wizard/name", corsWrap(w.handleName))
	s.HandleFunc("/api/wizard/poll", corsWrap(w.handlePoll))
	s.HandleFunc("/api/wizard/token", corsWrap(w.handleToken))
	s.HandleFunc("/api/wizard/obs", corsWrap(w.handleOBS))
	s.HandleFunc("/api/wizard/test-obs", corsWrap(w.handleTestOBS))
	s.HandleFunc("/api/wizard/save", corsWrap(w.handleSave))
	s.HandleFunc("/api/wizard/done", corsWrap(w.handleDone))
}

// UI interface delegation — used for non-wizard dialogs (e.g. fatalWait)
func (w *WebUI) Info(title, msg string)                       { w.fallback.Info(title, msg) }
func (w *WebUI) Error(title, msg string)                      { w.fallback.Error(title, msg) }
func (w *WebUI) Entry(title, text, def string) (string, bool) { return w.fallback.Entry(title, text, def) }
func (w *WebUI) Password(title, text string) (string, bool)   { return w.fallback.Password(title, text) }
func (w *WebUI) Confirm(title, msg string) bool               { return w.fallback.Confirm(title, msg) }
func (w *WebUI) Form(title string, fields []FormField) (map[string]string, bool) {
	return w.fallback.Form(title, fields)
}

// --- Wizard runners ---

func (w *WebUI) RunDeviceWizard(cfg WizardConfig) (*WizardResult, error) {
	return w.runWizard("device", cfg)
}

func (w *WebUI) RunManualWizard(cfg WizardConfig) (*WizardResult, error) {
	return w.runWizard("manual", cfg)
}

func (w *WebUI) RunOBSWizard(cfg WizardConfig) (*WizardResult, error) {
	return w.runWizard("obs", cfg)
}

func (w *WebUI) runWizard(mode string, cfg WizardConfig) (*WizardResult, error) {
	if w.statusSrv == nil {
		return nil, fmt.Errorf("status server not configured — call SetStatusServer first")
	}

	w.mu.Lock()
	w.mode = mode
	w.wizCfg = cfg
	w.result = &WizardResult{
		OBSPort: cfg.DefaultPort,
		Token:   cfg.ExistingToken,
	}
	w.doneCh = make(chan struct{})
	w.authDone = make(chan struct{})
	w.authToken = ""
	w.authErr = nil
	w.mu.Unlock()

	// Open the remote wizard page — it calls our local API via CORS
	port := w.statusSrv.Port()
	wizardURL := fmt.Sprintf("%s/setup?port=%d&mode=%s", remoteBaseURL, port, mode)

	log.Printf("[wizard] Opening setup wizard at %s", wizardURL)

	if err := device.OpenBrowser(wizardURL); err != nil {
		log.Printf("[wizard] Could not open browser: %v — open %s manually", err, wizardURL)
	}

	// Block until wizard completes
	<-w.doneCh

	if w.pollCancel != nil {
		w.pollCancel()
	}

	return w.result, nil
}

// --- CORS wrapper ---

// corsWrap wraps a handler with CORS headers so the remote wizard page can call the local API.
func corsWrap(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedOrigins[origin] {
			rw.Header().Set("Access-Control-Allow-Origin", origin)
			rw.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			rw.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			rw.Header().Set("Access-Control-Max-Age", "3600")
		}
		if r.Method == "OPTIONS" {
			rw.WriteHeader(204)
			return
		}
		next(rw, r)
	}
}

// --- Handlers ---

func (w *WebUI) handleState(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	writeJSON(rw, map[string]interface{}{
		"mode":    w.mode,
		"version": w.wizCfg.Version,
		"defaults": map[string]interface{}{
			"host":         w.wizCfg.DefaultHost,
			"port":         w.wizCfg.DefaultPort,
			"obs_detected": w.wizCfg.OBSDetected,
		},
	})
}

func (w *WebUI) handleName(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(rw, "POST only", 405)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(rw, map[string]interface{}{"error": "invalid request"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(rw, map[string]interface{}{"error": "Name is required"})
		return
	}

	w.mu.Lock()
	baseURL := relayToHTTPS(w.wizCfg.RelayURL)
	version := w.wizCfg.Version
	w.mu.Unlock()

	flow := &device.Flow{BaseURL: baseURL, Version: version}

	log.Printf("[wizard] Requesting device authorization for %q...", name)
	code, err := flow.RequestCode(context.Background(), name)
	if err != nil {
		writeJSON(rw, map[string]interface{}{"error": fmt.Sprintf("Authorization failed: %v", err)})
		return
	}

	w.mu.Lock()
	w.deviceFlow = flow
	w.deviceCode = code
	w.mu.Unlock()

	if code.Status == "already_authorized" && code.Token != "" {
		w.mu.Lock()
		w.result.Token = code.Token
		close(w.authDone)
		w.mu.Unlock()
		log.Printf("[wizard] Machine already authorized as %q", code.AgentName)
		writeJSON(rw, map[string]interface{}{
			"already_authorized": true,
			"agent_name":         code.AgentName,
		})
		return
	}

	// Open verification URL in a new tab
	if err := device.OpenBrowser(code.VerificationURL); err != nil {
		log.Printf("[wizard] Could not open verification URL: %v", err)
	}

	// Start background polling
	pollCtx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.pollCancel = cancel
	w.authDone = make(chan struct{})
	w.mu.Unlock()

	go w.pollDeviceAuth(pollCtx, flow, code)

	writeJSON(rw, map[string]interface{}{
		"already_authorized": false,
		"verification_url":   code.VerificationURL,
		"user_code":          code.UserCode,
		"poll_interval":      code.Interval,
	})
}

func (w *WebUI) pollDeviceAuth(ctx context.Context, flow *device.Flow, code *device.CodeResponse) {
	token, err := flow.PollForToken(ctx, code.DeviceCode, code.Interval)

	w.mu.Lock()
	defer w.mu.Unlock()

	if err != nil {
		w.authErr = err
		log.Printf("[wizard] Device auth failed: %v", err)
	} else {
		w.authToken = token
		w.result.Token = token
		log.Println("[wizard] Device authorized!")
	}

	select {
	case <-w.authDone:
	default:
		close(w.authDone)
	}
}

func (w *WebUI) handlePoll(rw http.ResponseWriter, r *http.Request) {
	select {
	case <-w.authDone:
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.authErr != nil {
			msg := w.authErr.Error()
			if strings.Contains(msg, "denied") {
				writeJSON(rw, map[string]string{"status": "denied"})
			} else if strings.Contains(msg, "expired") || strings.Contains(msg, "timed out") {
				writeJSON(rw, map[string]string{"status": "expired"})
			} else {
				writeJSON(rw, map[string]string{"status": "error", "error": msg})
			}
		} else {
			writeJSON(rw, map[string]string{"status": "complete"})
		}
	default:
		writeJSON(rw, map[string]string{"status": "pending"})
	}
}

func (w *WebUI) handleToken(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(rw, "POST only", 405)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(rw, map[string]interface{}{"valid": false, "error": "invalid request"})
		return
	}

	token := strings.TrimSpace(strings.ToLower(req.Token))
	if !tokenPattern.MatchString(token) {
		writeJSON(rw, map[string]interface{}{"valid": false, "error": "Token must be exactly 64 hex characters"})
		return
	}

	w.mu.Lock()
	w.result.Token = token
	w.mu.Unlock()

	writeJSON(rw, map[string]interface{}{"valid": true})
}

func (w *WebUI) handleOBS(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(rw, "POST only", 405)
		return
	}
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(rw, map[string]interface{}{"error": "invalid request"})
		return
	}

	// OBS host is hardcoded — ignore whatever the wizard sends
	port := req.Port
	if port <= 0 || port > 65535 {
		port = 4455
	}

	w.mu.Lock()
	w.result.OBSPort = port
	w.result.OBSPass = req.Password
	w.mu.Unlock()

	writeJSON(rw, map[string]interface{}{"ok": true})
}

func (w *WebUI) handleTestOBS(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(rw, "POST only", 405)
		return
	}
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(rw, map[string]interface{}{"ok": false, "error": "invalid request"})
		return
	}

	// OBS host is hardcoded — use the configured default, ignore client value
	port := req.Port
	if port <= 0 || port > 65535 {
		port = 4455
	}

	addr := fmt.Sprintf("ws://%s:%d", w.wizCfg.DefaultHost, port)
	dialer := &websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	ws, _, err := dialer.Dial(addr, nil)
	if err != nil {
		writeJSON(rw, map[string]interface{}{"ok": false, "error": "Could not connect to OBS"})
		return
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		writeJSON(rw, map[string]interface{}{"ok": false, "error": "Connected but OBS did not respond"})
		return
	}

	var msg struct {
		Op int `json:"op"`
		D  struct {
			ObsWebSocketVersion string `json:"obsWebSocketVersion"`
		} `json:"d"`
	}
	if err := json.Unmarshal(data, &msg); err != nil || msg.Op != 0 {
		writeJSON(rw, map[string]interface{}{"ok": false, "error": "Connected but response was not OBS WebSocket"})
		return
	}

	writeJSON(rw, map[string]interface{}{"ok": true, "version": msg.D.ObsWebSocketVersion})
}

func (w *WebUI) handleSave(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(rw, "POST only", 405)
		return
	}

	w.mu.Lock()
	savePath := w.wizCfg.SavePath
	relayURL := w.wizCfg.RelayURL
	result := *w.result
	w.mu.Unlock()

	if savePath == "" {
		writeJSON(rw, map[string]interface{}{"saved": false, "error": "no config path available"})
		return
	}

	cfg := &agent.Config{
		RelayURL: relayURL,
		Token:    result.Token,
		OBSHost:  w.wizCfg.DefaultHost,
		OBSPort:  result.OBSPort,
		OBSPass:  result.OBSPass,
	}

	if err := agent.SaveConfig(savePath, cfg); err != nil {
		writeJSON(rw, map[string]interface{}{"saved": false, "error": err.Error()})
		return
	}

	w.mu.Lock()
	w.result.Saved = true
	w.mu.Unlock()

	log.Printf("[wizard] Config saved to %s", savePath)
	writeJSON(rw, map[string]interface{}{"saved": true, "path": savePath})
}

func (w *WebUI) handleDone(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(rw, "POST only", 405)
		return
	}

	resp := map[string]interface{}{"ok": true}
	port := w.statusSrv.Port()
	resp["status_url"] = remoteBaseURL + "/status?port=" + fmt.Sprintf("%d", port)
	writeJSON(rw, resp)

	go func() {
		time.Sleep(100 * time.Millisecond)
		select {
		case <-w.doneCh:
		default:
			close(w.doneCh)
		}
	}()
}

// --- Helpers ---

func writeJSON(rw http.ResponseWriter, data interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(data)
}

func relayToHTTPS(relayURL string) string {
	u := relayURL
	u = strings.Replace(u, "wss://", "https://", 1)
	u = strings.Replace(u, "ws://", "http://", 1)
	if idx := strings.Index(u, "://"); idx >= 0 {
		pathStart := strings.Index(u[idx+3:], "/")
		if pathStart >= 0 {
			return u[:idx+3+pathStart]
		}
	}
	return u
}

