package ui

// FormField describes a single field in a multi-field form dialog.
type FormField struct {
	Label    string // Display label shown to the user
	Key      string // Key used in the returned map
	Default  string // Pre-filled default value
	Password bool   // True for masked/password input
}

// UI abstracts user interaction for the setup wizard.
// Implementations: GuiUI (native OS dialogs via zenity), CliUI (stdin fallback),
// and WebUI (branded browser-based wizard).
type UI interface {
	// Info shows an informational message.
	Info(title, message string)
	// Error shows an error message.
	Error(title, message string)
	// Entry prompts for text input. Returns the value and true, or "" and false if cancelled.
	Entry(title, text, defaultValue string) (string, bool)
	// Password prompts for a password (masked input). Returns the value and true, or "" and false if cancelled.
	Password(title, text string) (string, bool)
	// Confirm asks a yes/no question. Returns true for yes.
	Confirm(title, message string) bool
	// Form shows a multi-field dialog in a single window.
	// Returns field values keyed by FormField.Key, and true if submitted (false if cancelled).
	Form(title string, fields []FormField) (map[string]string, bool)
}
