package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/4throck/obs-agent/internal/obs"
	"github.com/4throck/obs-agent/internal/tunnel"
)

// Agent manages the lifecycle of the OBS agent
type Agent struct {
	cfg    *Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
			return nil
		default:
		}

		err := a.run()
		if err == nil {
			// Clean shutdown
			return nil
		}

		if a.ctx.Err() != nil {
			return nil
		}

		attempt++

		// Special handling for connection approval challenges
		if challenge, ok := err.(*tunnel.ErrChallenge); ok {
			log.Printf("[agent] *** NEW DEVICE DETECTED ***")
			log.Printf("[agent] Approval code: %s", challenge.Code)
			log.Printf("[agent] Approve this connection in your dashboard, then the agent will reconnect automatically.")
			// Use a fixed 10s delay for challenge retries
			select {
			case <-time.After(10 * time.Second):
			case <-a.ctx.Done():
				return nil
			}
			continue
		}

		delay := backoff(attempt)
		log.Printf("[agent] Connection lost: %v — reconnecting in %v (attempt %d)", err, delay, attempt)

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
	log.Printf("[agent] Connecting to local OBS at %s:%d", a.cfg.OBSHost, a.cfg.OBSPort)
	obsAddr := fmt.Sprintf("%s:%d", a.cfg.OBSHost, a.cfg.OBSPort)
	obsConn, err := obs.Connect(a.ctx, obsAddr, a.cfg.OBSPass)
	if err != nil {
		return fmt.Errorf("OBS connection failed: %w", err)
	}
	defer obsConn.Close()
	log.Println("[agent] Connected to local OBS")

	// Connect to relay
	log.Printf("[agent] Connecting to relay at %s", a.cfg.RelayURL)
	relayConn, err := tunnel.Connect(a.ctx, a.cfg.RelayURL, a.cfg.Token, a.cfg.Version)
	if err != nil {
		return fmt.Errorf("relay connection failed: %w", err)
	}
	defer relayConn.Close()
	log.Println("[agent] Connected to relay")

	// Wait for session handshake — relay sends nonce, we derive session key
	sessionKey, err := tunnel.WaitForSession(relayConn, a.cfg.Token)
	if err != nil {
		// Check if this is a challenge (connection approval required)
		if challenge, ok := err.(*tunnel.ErrChallenge); ok {
			return challenge // Pass through — main loop handles display
		}
		return fmt.Errorf("session handshake failed: %w", err)
	}

	// Bridge messages with signed envelope protocol
	log.Println("[agent] Bridge active — relaying signed messages")
	return tunnel.EnvelopeBridge(a.ctx, obsConn, relayConn, sessionKey)
}

// Stop gracefully shuts down the agent
func (a *Agent) Stop() {
	a.cancel()
	a.wg.Wait()
}
