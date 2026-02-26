package ui

import (
	"os"
	"runtime"

	"github.com/ncruces/zenity"
)

// GuiUI uses native OS dialogs via zenity (Win32 on Windows, osascript on macOS, zenity/kdialog on Linux).
type GuiUI struct{}

// NewGuiUI returns a new GUI-based UI.
func NewGuiUI() *GuiUI {
	return &GuiUI{}
}

func (g *GuiUI) Info(title, message string) {
	_ = zenity.Info(message, zenity.Title(title))
}

func (g *GuiUI) Error(title, message string) {
	_ = zenity.Error(message, zenity.Title(title))
}

func (g *GuiUI) Entry(title, text, defaultValue string) (string, bool) {
	val, err := zenity.Entry(text, zenity.Title(title), zenity.EntryText(defaultValue))
	if err != nil {
		return "", false
	}
	return val, true
}

func (g *GuiUI) Password(title, text string) (string, bool) {
	_, pw, err := zenity.Password(zenity.Title(title))
	if err != nil {
		return "", false
	}
	return pw, true
}

func (g *GuiUI) Confirm(title, message string) bool {
	err := zenity.Question(message, zenity.Title(title), zenity.OKLabel("Yes"), zenity.CancelLabel("No"))
	return err == nil
}

func (g *GuiUI) Form(title string, fields []FormField) (map[string]string, bool) {
	return showForm(title, fields)
}

// IsGuiAvailable returns true if native GUI dialogs can be shown.
// Always true on Windows and macOS. On Linux, requires DISPLAY or WAYLAND_DISPLAY.
func IsGuiAvailable() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	default:
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}
