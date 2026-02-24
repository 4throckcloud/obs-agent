package tray

// System tray functionality is optional and requires CGO + platform-specific
// build tags (getlantern/systray). For now, provide a no-op implementation
// that compiles cross-platform without CGO.
//
// When building with CGO_ENABLED=1 on the target platform, the systray
// implementation in tray_cgo.go will be used instead.

// Status represents the agent connection state
type Status int

const (
	StatusDisconnected Status = iota
	StatusConnecting
	StatusConnected
)

// Tray provides system tray integration (no-op without CGO)
type Tray struct {
	status Status
	onQuit func()
}

// New creates a new system tray (no-op in CGO_ENABLED=0 builds)
func New(onQuit func()) *Tray {
	return &Tray{
		status: StatusDisconnected,
		onQuit: onQuit,
	}
}

// SetStatus updates the tray icon (no-op without CGO)
func (t *Tray) SetStatus(s Status) {
	t.status = s
}

// Run starts the tray event loop (no-op without CGO)
func (t *Tray) Run() {
	// No-op: in headless mode, the agent runs without a tray icon
}

// Stop cleans up the tray (no-op without CGO)
func (t *Tray) Stop() {
	// No-op
}
