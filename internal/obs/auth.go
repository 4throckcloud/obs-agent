package obs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// OBS WebSocket v5 protocol messages for authentication
type obsMessage struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

type helloData struct {
	ObsWebSocketVersion string          `json:"obsWebSocketVersion"`
	Authentication      *authChallenge  `json:"authentication,omitempty"`
}

type authChallenge struct {
	Challenge string `json:"challenge"`
	Salt      string `json:"salt"`
}

type identifyMsg struct {
	RPCVersion     int    `json:"rpcVersion"`
	Authentication string `json:"authentication,omitempty"`
}

type identifiedData struct {
	NegotiatedRPCVersion int `json:"negotiatedRpcVersion"`
}

// authenticate performs OBS WebSocket v5 SHA256 challenge-response auth
func authenticate(conn *websocket.Conn, password string) error {
	// Read Hello (op 0)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read Hello: %w", err)
	}

	var hello obsMessage
	if err := json.Unmarshal(data, &hello); err != nil {
		return fmt.Errorf("failed to parse Hello: %w", err)
	}

	if hello.Op != 0 {
		return fmt.Errorf("expected Hello (op 0), got op %d", hello.Op)
	}

	var hd helloData
	if err := json.Unmarshal(hello.D, &hd); err != nil {
		return fmt.Errorf("failed to parse Hello data: %w", err)
	}

	// Build Identify (op 1)
	identify := identifyMsg{
		RPCVersion: 1,
	}

	if hd.Authentication != nil {
		// Generate auth string: base64(sha256(base64(sha256(password + salt)) + challenge))
		authStr := generateAuthString(password, hd.Authentication.Salt, hd.Authentication.Challenge)
		identify.Authentication = authStr
	}

	identifyData, err := json.Marshal(identify)
	if err != nil {
		return fmt.Errorf("failed to marshal Identify: %w", err)
	}

	msg := obsMessage{
		Op: 1,
		D:  identifyData,
	}

	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("failed to send Identify: %w", err)
	}

	// Read Identified (op 2) or error
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err = conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read Identified: %w", err)
	}

	var response obsMessage
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if response.Op != 2 {
		return fmt.Errorf("authentication failed (op %d)", response.Op)
	}

	// Clear deadlines for normal operation
	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	return nil
}

// generateAuthString implements OBS WS v5 auth: base64(sha256(base64(sha256(password+salt)) + challenge))
func generateAuthString(password, salt, challenge string) string {
	// Step 1: sha256(password + salt)
	h1 := sha256.Sum256([]byte(password + salt))
	b64Secret := base64.StdEncoding.EncodeToString(h1[:])

	// Step 2: sha256(base64_secret + challenge)
	h2 := sha256.Sum256([]byte(b64Secret + challenge))
	return base64.StdEncoding.EncodeToString(h2[:])
}
