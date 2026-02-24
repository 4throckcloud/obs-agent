package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeTimeout   = 10 * time.Second
	pongTimeout    = 60 * time.Second
	pingInterval   = 30 * time.Second
	obsReadTimeout = 90 * time.Second
)

// EnvelopeBridge pipes messages bidirectionally between OBS and relay connections,
// wrapping all messages in signed envelopes with OBS protocol validation.
//
// SECURITY:
// - All messages from relay are verified (HMAC + timestamp + nonce + OBS protocol)
// - All messages to relay are sealed in signed envelopes
// - Only whitelisted OBS v5 ops and request types pass through
// - Binary/unparseable messages are DROPPED (not forwarded)
func EnvelopeBridge(ctx context.Context, obsConn, relayConn *websocket.Conn, sessionKey []byte) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	nonceCache := NewNonceCache()
	errCh := make(chan error, 2)

	// Relay → OBS: verify envelope → validate OBS protocol → forward raw OBS message
	go func() {
		defer cancel()
		err := pipeRelayToOBS(ctx, relayConn, obsConn, sessionKey, nonceCache)
		errCh <- fmt.Errorf("relay→OBS pipe closed: %w", err)
	}()

	// OBS → Relay: validate OBS protocol → seal in envelope → send
	go func() {
		defer cancel()
		err := pipeOBSToRelay(ctx, obsConn, relayConn, sessionKey)
		errCh <- fmt.Errorf("OBS→relay pipe closed: %w", err)
	}()

	// Ping relay to keep connection alive
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				relayConn.SetWriteDeadline(time.Now().Add(writeTimeout))
				if err := relayConn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
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

// pipeRelayToOBS reads signed envelopes from relay, verifies them,
// validates OBS protocol, and forwards the raw OBS payload to local OBS.
func pipeRelayToOBS(ctx context.Context, relay, obs *websocket.Conn, sessionKey []byte, cache *NonceCache) error {
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

		// Step 3: Forward raw OBS payload to local OBS
		obs.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := obs.WriteMessage(websocket.TextMessage, result.Payload); err != nil {
			return fmt.Errorf("OBS write error: %w", err)
		}
	}
}

// pipeOBSToRelay reads raw OBS messages, validates the protocol,
// seals them in signed envelopes, and sends to relay.
func pipeOBSToRelay(ctx context.Context, obs, relay *websocket.Conn, sessionKey []byte) error {
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

		// Step 2: Seal in signed envelope
		sealed, err := Seal(sessionKey, data)
		if err != nil {
			log.Printf("[bridge] Failed to seal message: %v", err)
			continue
		}

		// Step 3: Send sealed envelope to relay
		relay.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := relay.WriteMessage(websocket.TextMessage, sealed); err != nil {
			return fmt.Errorf("relay write error: %w", err)
		}
	}
}

// ErrChallenge is returned when the relay requires connection approval.
// The Code field contains the challenge code to display to the user.
type ErrChallenge struct {
	Code string
}

func (e *ErrChallenge) Error() string {
	return fmt.Sprintf("connection approval required (code: %s)", e.Code)
}

// WaitForSession reads the session handshake message from the relay and derives the session key.
// The relay sends {"type":"session","nonce":"<hex>"} followed by {"type":"connected"}.
// If the relay sends {"type":"challenge","code":"..."}, it means the IP is not approved
// and the user must approve it in the dashboard.
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
			return nil, fmt.Errorf("session handshake failed: %w", err)
		}

		var msg struct {
			Type        string `json:"type"`
			Nonce       string `json:"nonce,omitempty"`
			Code        string `json:"code,omitempty"`
			Version     string `json:"version,omitempty"`
			DownloadURL string `json:"download_url,omitempty"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // Skip unparseable messages during handshake
		}

		switch msg.Type {
		case "challenge":
			// Connection approval required — return special error
			return nil, &ErrChallenge{Code: msg.Code}

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
