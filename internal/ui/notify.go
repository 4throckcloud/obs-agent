package ui

import "github.com/ncruces/zenity"

// Notify sends a desktop notification. No-op if zenity is unavailable or fails.
func Notify(title, message string) {
	if !IsGuiAvailable() {
		return
	}
	_ = zenity.Notify(message, zenity.Title(title), zenity.InfoIcon)
}
