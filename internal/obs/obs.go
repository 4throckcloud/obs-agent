package obs

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// OBSReadTimeout is the read deadline for OBS connections.
// Reset on each successful read in the bridge pipes.
const OBSReadTimeout = 90 * time.Second

// Connect establishes a WebSocket connection to local OBS Studio
func Connect(ctx context.Context, addr, password string) (*websocket.Conn, error) {
	url := fmt.Sprintf("ws://%s", addr)

	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("OBS WS dial failed: %w", err)
	}

	conn.SetReadLimit(1 * 1024 * 1024) // 1MB

	// OBS WebSocket v5 always requires Hello/Identify handshake,
	// even without a password (Identify still must be sent)
	if err := authenticate(conn, password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("OBS auth failed: %w", err)
	}

	// Set initial read deadline — bridge resets on each successful read
	conn.SetReadDeadline(time.Now().Add(OBSReadTimeout))

	return conn, nil
}

// ConnectMonitor establishes a WebSocket connection to local OBS with events suppressed.
// Used for the monitor's dedicated polling connection (EventSubscriptions: 0).
func ConnectMonitor(ctx context.Context, addr, password string) (*websocket.Conn, error) {
	url := fmt.Sprintf("ws://%s", addr)

	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("OBS WS dial failed: %w", err)
	}

	conn.SetReadLimit(1 * 1024 * 1024) // 1MB

	if err := authenticateMonitor(conn, password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("OBS monitor auth failed: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(OBSReadTimeout))

	return conn, nil
}
