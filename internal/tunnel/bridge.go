package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/4throck/obs-agent/internal/monitor"
	"github.com/gorilla/websocket"
)

const (
	writeTimeout   = 10 * time.Second
	pongTimeout    = 60 * time.Second
	pingInterval   = 30 * time.Second
	obsReadTimeout = 90 * time.Second
	relaySendCap   = 64
)

// EnvelopeBridge pipes messages bidirectionally between OBS and relay connections,
// wrapping all messages in signed envelopes with OBS protocol validation.
//
// SECURITY:
// - All messages from relay are verified (HMAC + timestamp + nonce + OBS protocol)
// - All messages to relay are sealed in signed envelopes
// - Only whitelisted OBS v5 ops and request types pass through
// - Binary/unparseable messages are DROPPED (not forwarded)
//
// A channel-based relay writer serialises all writes to the relay connection
// (OBS events, monitor events, pings) to prevent concurrent write panics.
func EnvelopeBridge(ctx context.Context, obsConn, relayConn *websocket.Conn, sessionKey []byte, obsAddr, obsPass string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	nonceCache := NewNonceCache()
	errCh := make(chan error, 3)

	// Channel-based relay writer: nil = ping, otherwise raw payload to seal.
	relaySend := make(chan []byte, relaySendCap)

	// Create monitor for agent-push source state polling
	mon := monitor.New(obsAddr, obsPass)
	mon.SetSendEvent(func(eventBytes []byte) {
		select {
		case relaySend <- eventBytes:
		default:
			// Drop if channel full — transient back-pressure
		}
	})
	defer mon.Stop()

	// Relay writer goroutine — sole writer to relayConn
	go func() {
		defer cancel()
		err := relayWriter(ctx, relayConn, sessionKey, relaySend)
		errCh <- fmt.Errorf("relay writer closed: %w", err)
	}()

	// Relay → OBS: verify envelope → validate OBS protocol → forward raw OBS message
	// AgentConfigureMonitor requests are intercepted and handled locally.
	go func() {
		defer cancel()
		err := pipeRelayToOBS(ctx, relayConn, obsConn, sessionKey, nonceCache, mon, relaySend)
		errCh <- fmt.Errorf("relay→OBS pipe closed: %w", err)
	}()

	// OBS → Relay: validate OBS protocol → send raw payload via channel (writer seals)
	go func() {
		defer cancel()
		err := pipeOBSToRelay(ctx, obsConn, relaySend)
		errCh <- fmt.Errorf("OBS→relay pipe closed: %w", err)
	}()

	// Ping relay to keep connection alive (sends nil to channel → writer sends WS ping)
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case relaySend <- nil:
				default:
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// relayWriter is the sole goroutine that writes to relayConn.
// nil payloads are sent as WS ping frames; non-nil payloads are sealed in envelopes.
func relayWriter(ctx context.Context, relay *websocket.Conn, sessionKey []byte, ch <-chan []byte) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case payload, ok := <-ch:
			if !ok {
				return fmt.Errorf("relaySend channel closed")
			}

			relay.SetWriteDeadline(time.Now().Add(writeTimeout))

			if payload == nil {
				// Ping frame
				if err := relay.WriteMessage(websocket.PingMessage, nil); err != nil {
					return fmt.Errorf("ping write error: %w", err)
				}
				continue
			}

			// Seal and send
			sealed, err := Seal(sessionKey, payload)
			if err != nil {
				log.Printf("[bridge] Failed to seal message: %v", err)
				continue
			}
			if err := relay.WriteMessage(websocket.TextMessage, sealed); err != nil {
				return fmt.Errorf("relay write error: %w", err)
			}
		}
	}
}

// pipeRelayToOBS reads signed envelopes from relay, verifies them,
// validates OBS protocol, and forwards the raw OBS payload to local OBS.
// AgentConfigureMonitor requests are intercepted and handled by the monitor.
func pipeRelayToOBS(ctx context.Context, relay, obs *websocket.Conn, sessionKey []byte, cache *NonceCache, mon *monitor.Monitor, relaySend chan<- []byte) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msgType, data, err := relay.ReadMessage()
		if err != nil {
			return err
		}

		// Only process text messages (OBS v5 is JSON)
		if msgType != websocket.TextMessage {
			continue // DROP binary messages
		}

		// Step 1: Verify signed envelope
		result := Open(sessionKey, data, cache)
		if !result.Valid {
			log.Printf("[bridge] Rejected relay message: %s", result.Reason)
			continue // DROP invalid envelopes
		}

		// Step 2: Validate OBS protocol (to_agent direction — these are commands TO local OBS)
		check := ValidateOBSProtocol(result.Payload, ToAgent)
		if !check.Valid {
			log.Printf("[bridge] Rejected OBS message from relay: %s", check.Reason)
			continue // DROP forbidden ops/requests
		}

		// Step 3: Intercept AgentConfigureMonitor — handle locally, do NOT forward to OBS
		if check.Parsed != nil && check.Parsed.Op == 6 && check.Parsed.D != nil {
			var reqData struct {
				RequestType string          `json:"requestType"`
				RequestID   string          `json:"requestId"`
				RequestData json.RawMessage `json:"requestData"`
			}
			if err := json.Unmarshal(*check.Parsed.D, &reqData); err == nil && reqData.RequestType == "AgentConfigureMonitor" {
				// Parse config and configure monitor
				var cfg monitor.Config
				if err := json.Unmarshal(reqData.RequestData, &cfg); err != nil {
					log.Printf("[bridge] Bad AgentConfigureMonitor data: %v", err)
				} else {
					mon.Configure(cfg)
				}

				// Build op 7 success response
				resp := map[string]interface{}{
					"op": 7,
					"d": map[string]interface{}{
						"requestType": "AgentConfigureMonitor",
						"requestId":   reqData.RequestID,
						"requestStatus": map[string]interface{}{
							"result": true,
							"code":   100,
						},
					},
				}
				respBytes, _ := json.Marshal(resp)

				// Send response via relay writer channel
				select {
				case relaySend <- respBytes:
				default:
				}
				continue
			}
		}

		// Step 4: Forward raw OBS payload to local OBS
		obs.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := obs.WriteMessage(websocket.TextMessage, result.Payload); err != nil {
			return fmt.Errorf("OBS write error: %w", err)
		}
	}
}

// pipeOBSToRelay reads raw OBS messages, validates the protocol,
// and sends raw payload via channel (the relay writer handles sealing).
func pipeOBSToRelay(ctx context.Context, obs *websocket.Conn, relaySend chan<- []byte) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msgType, data, err := obs.ReadMessage()
		if err != nil {
			return err
		}

		// Reset read deadline on successful read
		obs.SetReadDeadline(time.Now().Add(obsReadTimeout))

		// Only process text messages (OBS v5 is JSON)
		if msgType != websocket.TextMessage {
			continue // DROP binary messages
		}

		// Step 1: Validate OBS protocol (from_agent direction — these are responses FROM local OBS)
		check := ValidateOBSProtocol(data, FromAgent)
		if !check.Valid {
			// Don't log — local OBS may send various messages during auth handshake
			continue // DROP non-conforming messages
		}

		// Step 2: Send raw payload to relay writer channel (writer handles sealing)
		select {
		case relaySend <- data:
		default:
			log.Println("[bridge] Relay send channel full, dropping OBS message")
		}
	}
}

// ErrTokenRejected is returned when the relay refuses the token (close 4100).
// The agent should stop retrying and trigger re-authentication.
type ErrTokenRejected struct{}

func (e *ErrTokenRejected) Error() string {
	return "token rejected by relay"
}

// WaitForSession reads the session handshake message from the relay and derives the session key.
// The relay sends {"type":"session","nonce":"<hex>"} followed by {"type":"connected"}.
// Returns the derived session key.
//
// SECURITY: The session key is derived from token + nonce via HMAC-SHA256,
// so both sides compute the same key without transmitting it.
func WaitForSession(conn *websocket.Conn, token string) ([]byte, error) {
	var sessionKey []byte

	// Read session message (with timeout)
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			// Close code 4100 = relay rejected our token
			if websocket.IsCloseError(err, 4100) {
				return nil, &ErrTokenRejected{}
			}
			return nil, fmt.Errorf("session handshake failed: %w", err)
		}

		var msg struct {
			Type        string `json:"type"`
			Nonce       string `json:"nonce,omitempty"`
			Version     string `json:"version,omitempty"`
			DownloadURL string `json:"download_url,omitempty"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // Skip unparseable messages during handshake
		}

		switch msg.Type {
		case "session":
			if msg.Nonce == "" {
				return nil, fmt.Errorf("session message missing nonce")
			}
			sessionKey = DeriveSessionKey(token, msg.Nonce)
			log.Println("[agent] Session key derived")

		case "connected":
			if sessionKey == nil {
				return nil, fmt.Errorf("received connected before session")
			}
			// Clear read deadline — bridge will manage its own
			conn.SetReadDeadline(time.Time{})
			log.Println("[agent] Session established")
			return sessionKey, nil

		case "update_available":
			log.Printf("[agent] *** Update available: %s — download: %s ***", msg.Version, msg.DownloadURL)
			// Informational only — continue handshake

		default:
			// Unknown message type during handshake — skip
		}
	}
}
