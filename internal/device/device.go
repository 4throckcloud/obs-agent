package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

// CodeResponse holds the server's response to a device code request.
type CodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	// Set when machine already has an active token
	Status    string `json:"status,omitempty"`
	Token     string `json:"token,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
}

// Flow manages the device authorization flow against the appdev server.
type Flow struct {
	BaseURL string // e.g. "https://4throck.cloud"
	Version string // agent version string
}

// RequestCode asks the server for a new device code.
// If the machine already has an active token, the server returns it directly.
func (f *Flow) RequestCode(ctx context.Context, agentName string) (*CodeResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"agent_name":    agentName,
		"agent_version": f.Version,
		"agent_os":      runtime.GOOS + "/" + runtime.GOARCH,
		"machine_id":    MachineID(),
	})

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, "POST", f.BaseURL+"/api/device/code", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	var cr CodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &cr, nil
}

// PollForToken polls the server until the device code is approved, denied, or expired.
// Returns the raw 64-hex token and agent name on success.
func (f *Flow) PollForToken(ctx context.Context, deviceCode string, interval int) (token string, err error) {
	if interval < 1 {
		interval = 5
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	deadline := time.After(10 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("device authorization timed out")
		case <-ticker.C:
			tok, done, err := f.poll(ctx, deviceCode)
			if err != nil {
				// Transient network errors: silently retry
				continue
			}
			if done {
				if tok == "" {
					return "", fmt.Errorf("device authorization was denied or expired")
				}
				return tok, nil
			}
			// status == "pending", keep polling
		}
	}
}

// poll performs a single poll request. Returns (token, done, error).
// done=true means stop polling (either success or terminal failure).
func (f *Flow) poll(ctx context.Context, deviceCode string) (string, bool, error) {
	body, _ := json.Marshal(map[string]string{
		"device_code": deviceCode,
	})

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, "POST", f.BaseURL+"/api/device/poll", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, err // transient
	}
	defer resp.Body.Close()

	var result struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false, err
	}

	switch result.Status {
	case "pending":
		return "", false, nil
	case "complete":
		return result.Token, true, nil
	case "denied":
		return "", true, nil
	case "expired":
		return "", true, nil
	default:
		return "", true, nil
	}
}
