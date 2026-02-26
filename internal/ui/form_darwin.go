//go:build darwin

package ui

import (
	"fmt"
	"os/exec"
	"strings"
)

// showForm displays a multi-field form using osascript (AppleScript).
// macOS doesn't support true multi-field dialogs natively, so we use
// sequential "display dialog" calls within a single osascript invocation.
// Returns pipe-delimited values on stdout.
func showForm(title string, fields []FormField) (map[string]string, bool) {
	var sb strings.Builder
	sb.WriteString("set allValues to {}\n")

	for _, field := range fields {
		label := escapeAS(field.Label)
		def := escapeAS(field.Default)
		if field.Password {
			sb.WriteString(fmt.Sprintf(
				"set dlg to display dialog %q default answer %q with title %q with hidden answer buttons {\"OK\", \"Cancel\"} default button \"OK\"\n",
				label, def, title,
			))
		} else {
			sb.WriteString(fmt.Sprintf(
				"set dlg to display dialog %q default answer %q with title %q buttons {\"OK\", \"Cancel\"} default button \"OK\"\n",
				label, def, title,
			))
		}
		sb.WriteString("set end of allValues to text returned of dlg\n")
	}

	// Join with pipe delimiter
	sb.WriteString("set AppleScript's text item delimiters to \"|\"\n")
	sb.WriteString("return allValues as text\n")

	cmd := exec.Command("osascript", "-e", sb.String())
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}

	return parseFormOutput(strings.TrimSpace(string(out)), fields), true
}

func escapeAS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
