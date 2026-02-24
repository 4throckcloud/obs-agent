package ui

// UI abstracts user interaction for the setup wizard.
// Implementations: GuiUI (native OS dialogs via zenity) and CliUI (stdin fallback).
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
}
