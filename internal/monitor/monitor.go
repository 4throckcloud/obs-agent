package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/4throck/obs-agent/internal/obs"
	"github.com/gorilla/websocket"
)

// Config is the configuration pushed from the server via AgentConfigureMonitor.
type Config struct {
	Source         string `json:"source"`
	PollIntervalMs int   `json:"pollIntervalMs"`
	Enabled        bool  `json:"enabled"`
}

// mediaStateMap maps OBS media states to internal state strings.
// Only 2 states: "normal" (playing) and "buffering" (everything else).
// Mirrors MEDIA_STATE_MAP in ingest-monitor-service/src/monitor.js.
var mediaStateMap = map[string]string{
	"OBS_MEDIA_STATE_PLAYING":   "normal",
	"OBS_MEDIA_STATE_OPENING":   "buffering",
	"OBS_MEDIA_STATE_BUFFERING": "buffering",
	"OBS_MEDIA_STATE_ENDED":     "buffering",
	"OBS_MEDIA_STATE_ERROR":     "buffering",
	"OBS_MEDIA_STATE_STOPPED":   "buffering",
	"OBS_MEDIA_STATE_NONE":      "buffering",
}

const minPollInterval = 500 * time.Millisecond

// Monitor polls a local OBS media source and pushes state events to the relay.
type Monitor struct {
	mu         sync.Mutex
	obsAddr    string
	obsPass    string
	config     *Config
	sendEvent  func([]byte) // callback to push raw event JSON to relaySend channel
	pollCancel context.CancelFunc
	pollDone   chan struct{}
	// Scene map: source name → scene name (which scene contains this source)
	sceneMap   map[string]string
	sceneMapAt time.Time
}

// New creates a new Monitor. It does not start polling until Configure() is called.
func New(obsAddr, obsPass string) *Monitor {
	return &Monitor{
		obsAddr: obsAddr,
		obsPass: obsPass,
	}
}

// SetSendEvent sets the callback used to push event bytes to the relay writer.
func (m *Monitor) SetSendEvent(fn func([]byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendEvent = fn
}

// Configure starts, stops, or restarts the poll goroutine based on cfg.
func (m *Monitor) Configure(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing poll if any
	m.stopLocked()

	m.config = &cfg

	if !cfg.Enabled || cfg.Source == "" {
		log.Printf("[monitor] Disabled (source=%q, enabled=%v)", cfg.Source, cfg.Enabled)
		return
	}

	interval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	if interval < minPollInterval {
		interval = minPollInterval
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.pollCancel = cancel
	m.pollDone = make(chan struct{})

	log.Printf("[monitor] Configured: source=%s, interval=%dms", cfg.Source, interval.Milliseconds())

	go m.pollLoop(ctx, cfg.Source, interval)
}

// Stop stops the poll goroutine and closes any monitor OBS connection.
func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *Monitor) stopLocked() {
	if m.pollCancel != nil {
		m.pollCancel()
		m.pollCancel = nil
		// Wait for poll goroutine to finish
		if m.pollDone != nil {
			<-m.pollDone
			m.pollDone = nil
		}
	}
}

// pollLoop runs the ticker-based poll. It manages its own OBS connection.
func (m *Monitor) pollLoop(ctx context.Context, source string, interval time.Duration) {
	defer close(m.pollDone)

	var obsConn *websocket.Conn
	defer func() {
		if obsConn != nil {
			obsConn.Close()
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[monitor] Poll loop stopped")
			return
		case <-ticker.C:
			// Ensure OBS monitor connection exists
			if obsConn == nil {
				var err error
				obsConn, err = obs.ConnectMonitor(ctx, m.obsAddr, m.obsPass)
				if err != nil {
					log.Printf("[monitor] OBS connect failed: %v", err)
					m.sendState(source, "", "offline", "")
					continue
				}
				log.Println("[monitor] OBS monitor connection established")
			}

			// Refresh scene map (cached 30s) to find which scene contains this source
			m.refreshSceneMap(obsConn)
			containingScene := ""
			if m.sceneMap != nil {
				containingScene = m.sceneMap[source]
			}

			mediaState, err := m.pollOBS(obsConn, source)
			if err != nil {
				log.Printf("[monitor] Poll error: %v", err)
				obsConn.Close()
				obsConn = nil
				m.sendState(source, "", "offline", containingScene)
				continue
			}

			state := mediaStateMap[mediaState]
			if state == "" {
				state = "offline"
			}
			m.sendState(source, mediaState, state, containingScene)
		}
	}
}

// pollOBS sends GetMediaInputStatus and reads the response.
func (m *Monitor) pollOBS(conn *websocket.Conn, source string) (string, error) {
	reqID := fmt.Sprintf("mon-%d", time.Now().UnixMilli())

	req := map[string]interface{}{
		"op": 6,
		"d": map[string]interface{}{
			"requestType": "GetMediaInputStatus",
			"requestId":   reqID,
			"requestData": map[string]interface{}{
				"inputName": source,
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}

	// Read response — skip non-op-7 messages (shouldn't happen with events suppressed, but be safe)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 10; i++ {
		_, respData, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}

		var msg struct {
			Op int `json:"op"`
			D  struct {
				RequestID    string                 `json:"requestId"`
				ResponseData map[string]interface{} `json:"responseData"`
			} `json:"d"`
		}
		if err := json.Unmarshal(respData, &msg); err != nil {
			continue
		}

		if msg.Op == 7 && msg.D.RequestID == reqID {
			if msg.D.ResponseData == nil {
				return "OBS_MEDIA_STATE_NONE", nil
			}
			ms, _ := msg.D.ResponseData["mediaState"].(string)
			if ms == "" {
				return "OBS_MEDIA_STATE_NONE", nil
			}
			return ms, nil
		}
	}

	return "", fmt.Errorf("no matching response after 10 messages")
}

// refreshSceneMap walks all OBS scenes to build a sourceName → sceneName map.
// Cached for 30 seconds to avoid excessive OBS calls.
func (m *Monitor) refreshSceneMap(conn *websocket.Conn) {
	if time.Since(m.sceneMapAt) < 30*time.Second && m.sceneMap != nil {
		return
	}

	scenes, err := m.obsRequest(conn, "GetSceneList", nil)
	if err != nil {
		log.Printf("[monitor] refreshSceneMap GetSceneList failed: %v", err)
		return
	}

	sceneList, _ := scenes["scenes"].([]interface{})
	if len(sceneList) == 0 {
		return
	}

	newMap := make(map[string]string)
	for _, s := range sceneList {
		sc, _ := s.(map[string]interface{})
		sceneName, _ := sc["sceneName"].(string)
		if sceneName == "" {
			continue
		}

		items, err := m.obsRequest(conn, "GetSceneItemList", map[string]interface{}{
			"sceneName": sceneName,
		})
		if err != nil {
			continue
		}

		itemList, _ := items["sceneItems"].([]interface{})
		for _, item := range itemList {
			it, _ := item.(map[string]interface{})
			srcName, _ := it["sourceName"].(string)
			if srcName != "" {
				if _, exists := newMap[srcName]; !exists {
					newMap[srcName] = sceneName
				}
			}
		}
	}

	m.sceneMap = newMap
	m.sceneMapAt = time.Now()
	log.Printf("[monitor] Scene map refreshed: %d sources mapped", len(newMap))
}

// obsRequest sends a request to OBS and reads the op 7 response.
func (m *Monitor) obsRequest(conn *websocket.Conn, requestType string, requestData map[string]interface{}) (map[string]interface{}, error) {
	reqID := fmt.Sprintf("mon-%s-%d", requestType, time.Now().UnixMilli())

	d := map[string]interface{}{
		"requestType": requestType,
		"requestId":   reqID,
	}
	if requestData != nil {
		d["requestData"] = requestData
	}

	req := map[string]interface{}{
		"op": 6,
		"d":  d,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 10; i++ {
		_, respData, err := conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}

		var msg struct {
			Op int `json:"op"`
			D  struct {
				RequestID    string                 `json:"requestId"`
				ResponseData map[string]interface{} `json:"responseData"`
			} `json:"d"`
		}
		if err := json.Unmarshal(respData, &msg); err != nil {
			continue
		}

		if msg.Op == 7 && msg.D.RequestID == reqID {
			if msg.D.ResponseData == nil {
				return map[string]interface{}{}, nil
			}
			return msg.D.ResponseData, nil
		}
	}

	return nil, fmt.Errorf("no matching response")
}

// sendState builds an op 5 AgentSourceState event and calls sendEvent.
func (m *Monitor) sendState(inputName, mediaState, state, containingScene string) {
	m.mu.Lock()
	fn := m.sendEvent
	m.mu.Unlock()

	if fn == nil {
		return
	}

	event := map[string]interface{}{
		"op": 5,
		"d": map[string]interface{}{
			"eventType":   "AgentSourceState",
			"eventIntent": 1,
			"eventData": map[string]interface{}{
				"inputName":       inputName,
				"mediaState":      mediaState,
				"state":           state,
				"containingScene": containingScene,
			},
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[monitor] Failed to marshal event: %v", err)
		return
	}

	fn(data)
}
