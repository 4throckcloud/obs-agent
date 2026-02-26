package ui

import "strings"

// parseFormOutput splits pipe-delimited output from native form dialogs
// and maps values back to the corresponding FormField keys.
// If a value is empty, the field's Default is used.
func parseFormOutput(raw string, fields []FormField) map[string]string {
	parts := strings.Split(raw, "|")
	result := make(map[string]string, len(fields))
	for i, f := range fields {
		val := ""
		if i < len(parts) {
			val = strings.TrimSpace(parts[i])
		}
		if val == "" {
			val = f.Default
		}
		result[f.Key] = val
	}
	return result
}
