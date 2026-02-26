//go:build !windows && !darwin

package ui

import (
	"os/exec"
	"strings"
)

// showForm displays a multi-field form using zenity --forms on Linux/BSD.
// Returns pipe-delimited values on stdout.
func showForm(title string, fields []FormField) (map[string]string, bool) {
	args := []string{"--forms", "--title", title, "--separator", "|"}
	for _, f := range fields {
		if f.Password {
			args = append(args, "--add-password", f.Label)
		} else {
			args = append(args, "--add-entry", f.Label)
		}
	}

	cmd := exec.Command("zenity", args...)
	out, err := cmd.Output()
	if err != nil {
		// zenity --forms doesn't support default values, so fall back to
		// the ncruces/zenity library (sequential dialogs) if zenity is not available.
		return showFormFallback(title, fields)
	}

	return parseFormOutput(strings.TrimSpace(string(out)), fields), true
}

// showFormFallback uses individual zenity dialogs as a fallback when
// the system zenity binary is not available (ncruces/zenity works cross-platform).
func showFormFallback(title string, fields []FormField) (map[string]string, bool) {
	g := &GuiUI{}
	result := make(map[string]string, len(fields))
	for _, f := range fields {
		if f.Password {
			val, ok := g.Password(title, f.Label)
			if !ok {
				return nil, false
			}
			result[f.Key] = val
		} else {
			val, ok := g.Entry(title, f.Label, f.Default)
			if !ok {
				return nil, false
			}
			result[f.Key] = val
		}
	}
	return result, true
}
