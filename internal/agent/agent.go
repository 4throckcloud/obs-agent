package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/4throck/obs-agent/internal/obs"
	"github.com/4throck/obs-agent/internal/status"
	"github.com/4throck/obs-agent/internal/tunnel"
)

// Agent manages the lifecycle of the OBS agent
type Agent struct {
	cfg          *Config
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	StatusServer *status.Server
}

// New creates a new Agent instance
func New(cfg *Config) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	return &Agent{
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins the agent's main loop with reconnection
func (a *Agent) Start() error {
	attempt := 0

	for {
		select {
		case <-a.ctx.Done():
			log.Println("[agent] Context cancelled, stopping")
			a.setStatus("stopped")
			return nil
		default:
		}

		err := a.run()
		if err == nil {
			// Clean shutdown
			a.setStatus("stopped")
			return nil
		}

		if a.ctx.Err() != nil {
			a.setStatus("stopped")
			return nil
		}

		attempt++
		a.setStatus("reconnecting")
		a.setOBS(false)
		a.setRelay(false)

		// Token rejected by relay — stop retrying, caller must re-authenticate
		if _, ok := err.(*tunnel.ErrTokenRejected); ok {
			log.Println("[agent] Token rejected by relay — re-authentication required")
			a.setStatus("token_rejected")
			a.setError("token rejected — re-authenticating")
			return err
		}

		delay := backoff(attempt)
		log.Printf("[agent] Connection lost: %v — reconnecting in %v (attempt %d)", err, delay, attempt)
		a.setError(err.Error())

		select {
		case <-time.After(delay):
		case <-a.ctx.Done():
			return nil
		}
	}
}

// run executes one connection lifecycle:
// 1. Connect to local OBS (authenticate locally)
// 2. Connect to relay over WSS
// 3. Wait for session handshake (derive session key)
// 4. Bridge with signed envelopes + OBS protocol validation
func (a *Agent) run() error {
	// Connect to local OBS
	a.setStatus("connecting_obs")
	log.Printf("[agent] Connecting to local OBS at %s:%d", a.cfg.OBSHost, a.cfg.OBSPort)
	obsAddr := fmt.Sprintf("%s:%d", a.cfg.OBSHost, a.cfg.OBSPort)
	obsConn, err := obs.Connect(a.ctx, obsAddr, a.cfg.OBSPass)
	if err != nil {
		return fmt.Errorf("OBS connection failed: %w", err)
	}
	defer obsConn.Close()
	log.Println("[agent] Connected to local OBS")
	a.setOBS(true)

	// Connect to relay
	a.setStatus("connecting_relay")
	log.Printf("[agent] Connecting to relay at %s", a.cfg.RelayURL)
	relayConn, err := tunnel.Connect(a.ctx, a.cfg.RelayURL, a.cfg.Token, a.cfg.Version)
	if err != nil {
		return fmt.Errorf("relay connection failed: %w", err)
	}
	defer relayConn.Close()
	log.Println("[agent] Connected to relay")
	a.setRelay(true)

	// Wait for session handshake — relay sends nonce, we derive session key
	sessionKey, err := tunnel.WaitForSession(relayConn, a.cfg.Token)
	if err != nil {
		// Pass through special errors — main loop handles them
		if _, ok := err.(*tunnel.ErrTokenRejected); ok {
			return err
		}
		return fmt.Errorf("session handshake failed: %w", err)
	}

	// Bridge messages with signed envelope protocol
	a.setStatus("connected")
	a.setError("")
	log.Println("[agent] Bridge active — relaying signed messages")
	return tunnel.EnvelopeBridge(a.ctx, obsConn, relayConn, sessionKey, obsAddr, a.cfg.OBSPass)
}

// Stop gracefully shuts down the agent
func (a *Agent) Stop() {
	a.setStatus("stopped")
	a.cancel()
	a.wg.Wait()
}

// Status server helpers — nil-safe

func (a *Agent) setStatus(s string) {
	if a.StatusServer != nil {
		a.StatusServer.SetStatus(s)
	}
}

func (a *Agent) setError(e string) {
	if a.StatusServer != nil {
		a.StatusServer.SetError(e)
	}
}

func (a *Agent) setOBS(connected bool) {
	if a.StatusServer != nil {
		a.StatusServer.SetOBSConnected(connected)
	}
}

func (a *Agent) setRelay(connected bool) {
	if a.StatusServer != nil {
		a.StatusServer.SetRelayConnected(connected)
	}
}
