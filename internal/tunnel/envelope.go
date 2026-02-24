package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"
)

// Signed envelope protocol — must match relay's envelope.js exactly.
//
// Every message between agent and relay is wrapped in a signed envelope:
//   { v, t, n, p, h }
// Where:
//   v = envelope version (1)
//   t = timestamp (unix ms)
//   n = random nonce (16 bytes hex = 32 chars)
//   p = base64-encoded payload
//   h = HMAC-SHA256(session_key, "1|t|n|p")
//
// SECURITY:
// - Integrity: HMAC prevents message tampering
// - Replay protection: nonce + timestamp window (±30s)
// - Protocol enforcement: payload must be valid OBS WebSocket v5
// - Action whitelist: only approved OBS request types pass through

const (
	timestampTolerance = 30 * time.Second // ±30s window
	maxNonceCache      = 2000
	nonceBytes         = 16 // 16 bytes = 32 hex chars
)

// envelope is the wire format for signed messages.
type envelope struct {
	V int    `json:"v"`
	T int64  `json:"t"`
	N string `json:"n"`
	P string `json:"p"`
	H string `json:"h"`
}

// NonceCache tracks recently-seen nonces for replay protection with TTL-based eviction.
type NonceCache struct {
	mu     sync.Mutex
	nonces map[string]int64 // nonce → timestamp (unix ms)
}

const nonceTTL = 60000 // 60s TTL (2x the 30s tolerance window)

// NewNonceCache creates a bounded nonce cache.
func NewNonceCache() *NonceCache {
	return &NonceCache{
		nonces: make(map[string]int64, maxNonceCache),
	}
}

func (nc *NonceCache) has(n string) bool {
	_, ok := nc.nonces[n]
	return ok
}

// evictExpired removes nonces older than TTL.
func (nc *NonceCache) evictExpired() {
	now := time.Now().UnixMilli()
	for nonce, ts := range nc.nonces {
		if now-ts > nonceTTL {
			delete(nc.nonces, nonce)
		}
	}
}

func (nc *NonceCache) add(n string) {
	nc.nonces[n] = time.Now().UnixMilli()
	// Size cap as secondary protection
	if len(nc.nonces) > maxNonceCache {
		// Evict oldest by timestamp
		var oldestKey string
		var oldestTs int64 = math.MaxInt64
		for k, ts := range nc.nonces {
			if ts < oldestTs {
				oldestTs = ts
				oldestKey = k
			}
		}
		if oldestKey != "" {
			delete(nc.nonces, oldestKey)
		}
	}
}

// DeriveSessionKey computes the session key from the agent token + relay-provided nonce.
// Must match relay's deriveSessionKey(token, sessionNonce) exactly.
func DeriveSessionKey(token string, sessionNonce string) []byte {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("obs-agent-v1|" + sessionNonce))
	return mac.Sum(nil)
}

// Seal wraps a payload in a signed envelope.
// Must match relay's seal(sessionKey, payload) exactly.
func Seal(sessionKey []byte, payload []byte) ([]byte, error) {
	t := time.Now().UnixMilli()

	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce generation failed: %w", err)
	}
	n := hex.EncodeToString(nonce)

	p := base64.StdEncoding.EncodeToString(payload)

	sigInput := fmt.Sprintf("1|%d|%s|%s", t, n, p)
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte(sigInput))
	h := hex.EncodeToString(mac.Sum(nil))

	env := envelope{V: 1, T: t, N: n, P: p, H: h}
	return json.Marshal(env)
}

// OpenResult is returned by Open.
type OpenResult struct {
	Valid   bool
	Payload []byte
	Reason  string
}

// Open verifies and unwraps a signed envelope.
// Must match relay's open(sessionKey, raw, recentNonces) exactly.
//
// SECURITY: HMAC is verified BEFORE timestamp to prevent timing side-channel
// that would differentiate expired vs bad-HMAC.
func Open(sessionKey []byte, raw []byte, cache *NonceCache) OpenResult {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return OpenResult{Reason: "not_json"}
	}

	// Version check
	if env.V != 1 {
		return OpenResult{Reason: "bad_version"}
	}

	// Type checks
	if env.N == "" || env.P == "" || env.H == "" {
		return OpenResult{Reason: "bad_fields"}
	}

	// Nonce format (32 hex chars = 16 bytes)
	if len(env.N) != 32 {
		return OpenResult{Reason: "bad_nonce"}
	}
	if _, err := hex.DecodeString(env.N); err != nil {
		return OpenResult{Reason: "bad_nonce"}
	}

	// HMAC verification FIRST (timing-safe) — before timestamp check
	sigInput := fmt.Sprintf("1|%d|%s|%s", env.T, env.N, env.P)
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte(sigInput))
	expected := mac.Sum(nil)

	actual, err := hex.DecodeString(env.H)
	if err != nil || !hmac.Equal(actual, expected) {
		return OpenResult{Reason: "bad_hmac"}
	}

	// Timestamp window (±30s) — checked after HMAC
	now := time.Now().UnixMilli()
	if abs64(now-env.T) > timestampTolerance.Milliseconds() {
		return OpenResult{Reason: "timestamp_expired"}
	}

	// Evict expired nonces (TTL-based) then replay check
	cache.mu.Lock()
	cache.evictExpired()

	if cache.has(env.N) {
		cache.mu.Unlock()
		return OpenResult{Reason: "replay"}
	}

	// Record nonce with timestamp
	cache.add(env.N)
	cache.mu.Unlock()

	// Decode payload
	payload, err := base64.StdEncoding.DecodeString(env.P)
	if err != nil {
		return OpenResult{Reason: "bad_payload"}
	}

	return OpenResult{Valid: true, Payload: payload}
}

// ── OBS WebSocket v5 protocol validation ────────────────────────────────

// obsMessage is the minimal OBS v5 wire format.
type obsMessage struct {
	Op int              `json:"op"`
	D  *json.RawMessage `json:"d,omitempty"`
}

// obsRequestData extracts requestType from op 6 data.
type obsRequestData struct {
	RequestType string `json:"requestType"`
}

// obsRequestBatchData extracts requests array from op 8 data.
type obsRequestBatchData struct {
	Requests []obsRequestData `json:"requests"`
}

// Direction of OBS message validation.
type Direction int

const (
	// FromAgent = agent is forwarding FROM local OBS (server→client ops)
	FromAgent Direction = iota
	// ToAgent = relay is forwarding commands TO local OBS (client→server ops)
	ToAgent
)

// Allowed op codes per direction — must match envelope.js exactly.
var allowedOpsFromAgent = map[int]bool{
	0: true, // Hello
	2: true, // Identified
	5: true, // Event
	7: true, // RequestResponse
	9: true, // RequestBatchResponse
}

var allowedOpsToAgent = map[int]bool{
	1: true, // Identify
	6: true, // Request
	8: true, // RequestBatch
}

// allowedRequestTypes — must match envelope.js ALLOWED_REQUEST_TYPES exactly.
var allowedRequestTypes = map[string]bool{
	// Scenes
	"GetSceneList": true, "SetCurrentProgramScene": true, "GetCurrentProgramScene": true,
	"CreateScene": true, "RemoveScene": true, "SetSceneName": true,
	// Scene items (sources within scenes)
	"GetSceneItemList": true, "GetSceneItemEnabled": true, "SetSceneItemEnabled": true,
	"GetSceneItemTransform": true, "SetSceneItemTransform": true, "RemoveSceneItem": true,
	// Sources / Inputs
	"GetSourcesList": true, "GetSourceActive": true, "SetSourceFilterEnabled": true,
	"CreateInput": true, "GetInputSettings": true, "SetInputSettings": true, "SetInputName": true,
	"GetInputMute": true, "SetInputMute": true, "ToggleInputMute": true, "GetInputVolume": true, "SetInputVolume": true,
	// Stream
	"GetStreamStatus": true, "StartStream": true, "StopStream": true, "ToggleStream": true,
	// Record
	"GetRecordStatus": true, "StartRecord": true, "StopRecord": true, "PauseRecord": true, "ResumeRecord": true,
	// Replay buffer
	"GetReplayBufferStatus": true, "StartReplayBuffer": true, "StopReplayBuffer": true, "SaveReplayBuffer": true,
	// Virtual cam
	"GetVirtualCamStatus": true, "StartVirtualCam": true, "StopVirtualCam": true,
	// Studio mode
	"GetStudioModeEnabled": true, "SetStudioModeEnabled": true,
	// Media
	"TriggerMediaInputAction": true,
	// General
	"GetVideoSettings": true, "GetStats": true, "GetVersion": true,
}

// ProtocolResult is returned by ValidateOBSProtocol.
type ProtocolResult struct {
	Valid  bool
	Parsed *obsMessage
	Reason string
}

// ValidateOBSProtocol checks that a message is valid OBS v5 going in the specified direction.
// Must match envelope.js validateOBSProtocol() exactly.
func ValidateOBSProtocol(payload []byte, dir Direction) ProtocolResult {
	var msg obsMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return ProtocolResult{Reason: "not_json"}
	}

	// Check direction-specific allowed ops
	var allowed map[int]bool
	if dir == FromAgent {
		allowed = allowedOpsFromAgent
	} else {
		allowed = allowedOpsToAgent
	}
	if !allowed[msg.Op] {
		return ProtocolResult{Reason: fmt.Sprintf("forbidden_op_%d", msg.Op)}
	}

	// For Request (op 6) going TO agent: validate request type is whitelisted
	if msg.Op == 6 && msg.D != nil {
		var reqData obsRequestData
		if err := json.Unmarshal(*msg.D, &reqData); err == nil {
			if reqData.RequestType != "" && !allowedRequestTypes[reqData.RequestType] {
				return ProtocolResult{Reason: fmt.Sprintf("forbidden_request_%s", reqData.RequestType)}
			}
		}
	}

	// For RequestBatch (op 8) going TO agent: validate each request
	if msg.Op == 8 && msg.D != nil {
		var batchData obsRequestBatchData
		if err := json.Unmarshal(*msg.D, &batchData); err == nil {
			for _, req := range batchData.Requests {
				if req.RequestType != "" && !allowedRequestTypes[req.RequestType] {
					return ProtocolResult{Reason: fmt.Sprintf("forbidden_batch_request_%s", req.RequestType)}
				}
			}
		}
	}

	return ProtocolResult{Valid: true, Parsed: &msg}
}

func abs64(x int64) int64 {
	if x < 0 {
		return int64(math.Abs(float64(x)))
	}
	return x
}
