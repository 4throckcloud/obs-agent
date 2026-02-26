package status

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// DefaultAddr is the preferred listen address. If the port is busy,
// Start will bind to :0 and let the OS pick a free port.
const DefaultAddr = "127.0.0.1:8765"

// Server provides a local HTTP status endpoint.
type Server struct {
	mu        sync.RWMutex
	version   string
	status    string
	obsConn   bool
	relayConn bool
	obsHost   string
	obsPort   int
	relayURL  string
	lastError string
	startedAt time.Time
	listenAddr string // actual address after binding

	mux    *http.ServeMux
	server *http.Server

	onQuit        func()
	onReconfigure func()
	onStateChange func(event, message string)
}

type statusResponse struct {
	Version        string `json:"version"`
	Status         string `json:"status"`
	OBSConnected   bool   `json:"obs_connected"`
	RelayConnected bool   `json:"relay_connected"`
	OBSHost        string `json:"obs_host"`
	OBSPort        int    `json:"obs_port"`
	RelayURL       string `json:"relay_url"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
	StartedAt      string `json:"started_at"`
	LastError      string `json:"last_error,omitempty"`
	PID            int    `json:"pid"`
}

// New creates a status server with a pre-built mux.
// Call HandleFunc to register additional routes before or after Start.
func New(version, obsHost string, obsPort int, relayURL string) *Server {
	s := &Server{
		version:   version,
		status:    "starting",
		obsHost:   obsHost,
		obsPort:   obsPort,
		relayURL:  relayURL,
		startedAt: time.Now(),
		mux:       http.NewServeMux(),
	}
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/api/status", s.handleAPIStatus)
	s.mux.HandleFunc("/api/quit", s.handleQuit)
	s.mux.HandleFunc("/api/reconfigure", s.handleReconfigure)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	return s
}

// HandleFunc registers an additional handler on the server's mux.
// Safe to call before or after Start.
func (s *Server) HandleFunc(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, handler)
}

// UpdateConfig updates the OBS/relay info returned by the status API.
func (s *Server) UpdateConfig(obsHost string, obsPort int, relayURL string) {
	s.mu.Lock()
	s.obsHost = obsHost
	s.obsPort = obsPort
	s.relayURL = relayURL
	s.mu.Unlock()
}

// SetQuitHandler sets the callback invoked when POST /api/quit is received.
func (s *Server) SetQuitHandler(fn func()) {
	s.mu.Lock()
	s.onQuit = fn
	s.mu.Unlock()
}

// SetReconfigureHandler sets the callback invoked when POST /api/reconfigure is received.
func (s *Server) SetReconfigureHandler(fn func()) {
	s.mu.Lock()
	s.onReconfigure = fn
	s.mu.Unlock()
}

// SetStateChangeHandler sets the callback invoked on connection state transitions.
func (s *Server) SetStateChangeHandler(fn func(event, message string)) {
	s.mu.Lock()
	s.onStateChange = fn
	s.mu.Unlock()
}

// corsHandler wraps the mux to add CORS headers for the remote agent site.
// Only allows the specific remote origin; local same-origin requests pass through unchanged.
func (s *Server) corsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "https://agent.4throck.cloud" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "3600")
			if r.Method == "OPTIONS" {
				w.WriteHeader(204)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Start begins listening. Tries DefaultAddr first; if busy, binds to :0.
func (s *Server) Start() {
	s.server = &http.Server{
		Handler:      s.corsHandler(s.mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	ln, err := net.Listen("tcp", DefaultAddr)
	if err != nil {
		// Default port busy — let OS assign a free port
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Printf("[status] Could not start status server: %v (non-fatal)", err)
			return
		}
	}

	s.mu.Lock()
	s.listenAddr = ln.Addr().String()
	s.mu.Unlock()

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[status] Status server error: %v", err)
		}
	}()

	log.Printf("[status] Status server listening on %s", s.Addr())
}

// Addr returns the actual listen address (e.g. "127.0.0.1:8765" or auto-assigned).
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listenAddr
}

// Port returns the actual port the server bound to, or 0 if not started.
func (s *Server) Port() int {
	addr := s.Addr()
	if addr == "" {
		return 0
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(portStr)
	return port
}

// Stop shuts down the status server.
func (s *Server) Stop() {
	if s.server != nil {
		s.server.Close()
	}
}

// SetStatus updates the current agent status.
func (s *Server) SetStatus(st string) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}

// SetError sets the last error message.
func (s *Server) SetError(err string) {
	s.mu.Lock()
	s.lastError = err
	s.mu.Unlock()
}

// SetOBSConnected updates OBS connection state and fires state change callback on transitions.
func (s *Server) SetOBSConnected(connected bool) {
	s.mu.Lock()
	prev := s.obsConn
	s.obsConn = connected
	cb := s.onStateChange
	s.mu.Unlock()

	if cb != nil && prev != connected {
		if connected {
			cb("obs_connected", fmt.Sprintf("OBS connected (%s:%d)", s.obsHost, s.obsPort))
		} else {
			cb("obs_disconnected", fmt.Sprintf("OBS disconnected (%s:%d)", s.obsHost, s.obsPort))
		}
	}
}

// SetRelayConnected updates relay connection state and fires state change callback on transitions.
func (s *Server) SetRelayConnected(connected bool) {
	s.mu.Lock()
	prev := s.relayConn
	s.relayConn = connected
	cb := s.onStateChange
	s.mu.Unlock()

	if cb != nil && prev != connected {
		if connected {
			cb("relay_connected", "Relay server connected")
		} else {
			cb("relay_disconnected", "Relay server disconnected")
		}
	}
}

func (s *Server) buildResponse() statusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return statusResponse{
		Version:        s.version,
		Status:         s.status,
		OBSConnected:   s.obsConn,
		RelayConnected: s.relayConn,
		OBSHost:        s.obsHost,
		OBSPort:        s.obsPort,
		RelayURL:       s.relayURL,
		UptimeSeconds:  int64(time.Since(s.startedAt).Seconds()),
		StartedAt:      s.startedAt.Format(time.RFC3339),
		LastError:      s.lastError,
		PID:            os.Getpid(),
	}
}

// handleRoot returns JSON status. No HTML — all UI is hosted at agent.4throck.cloud.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.buildResponse())
}

// handleAPIStatus always returns JSON (polled by the dashboard page).
func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.buildResponse())
}

// handleQuit triggers graceful shutdown via callback.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}

	s.mu.RLock()
	cb := s.onQuit
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if cb != nil {
		fmt.Fprint(w, `{"ok":true}`)
		go func() {
			time.Sleep(100 * time.Millisecond)
			cb()
		}()
	} else {
		fmt.Fprint(w, `{"ok":false,"error":"no quit handler"}`)
	}
}

// handleReconfigure triggers a reconfiguration via callback.
func (s *Server) handleReconfigure(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}

	s.mu.RLock()
	cb := s.onReconfigure
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if cb != nil {
		fmt.Fprint(w, `{"ok":true}`)
		go func() {
			time.Sleep(100 * time.Millisecond)
			cb()
		}()
	} else {
		fmt.Fprint(w, `{"ok":false,"error":"no reconfigure handler"}`)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"ok":true}`)
}
