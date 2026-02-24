package tunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Connect establishes a WSS connection to the relay server.
//
// SECURITY:
// - TLS 1.3 minimum (prevents downgrade attacks)
// - Token sent in header (not URL) — never appears in server access logs
// - Error messages are generic — do not leak server-side failure reasons
// - Read limit prevents memory exhaustion from malicious frames
func Connect(ctx context.Context, relayURL, token, version string) (*websocket.Conn, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			// CipherSuites: TLS 1.3 suites are not configurable in Go
			// (all TLS 1.3 suites are considered secure). This is correct behavior.
		},
	}

	headers := http.Header{}
	headers.Set("X-Agent-Token", token)
	if version != "" {
		headers.Set("X-Agent-Version", version)
	}

	conn, resp, err := dialer.DialContext(ctx, relayURL, headers)
	if err != nil {
		if resp != nil {
			// SECURITY: generic error — do not differentiate failure modes
			// Close codes from relay are all 4100 "refused" (no enumeration possible)
			return nil, fmt.Errorf("connection refused by relay (HTTP %d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	// 256KB read limit — OBS messages are small, anything larger is suspicious
	conn.SetReadLimit(256 * 1024)

	return conn, nil
}
