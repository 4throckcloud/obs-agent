package branding

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

// PrintBanner prints the branded startup banner.
func PrintBanner(version, goos, goarch string, w io.Writer) {
	fmt.Fprintln(w, "  \u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510")
	fmt.Fprintln(w, "  \u2502    4 t h R o c k   C l o u d    \u2502")
	fmt.Fprintf(w, "  \u2502         OBS Agent %-13s \u2502\n", version)
	fmt.Fprintf(w, "  \u2502         %-22s \u2502\n", goos+"/"+goarch)
	fmt.Fprintln(w, "  \u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518")
}

// ANSI color helpers — wrap text in escape codes.
// No-ops on Windows GUI mode or dumb terminals.

func Green(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

func Yellow(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[33m" + s + "\033[0m"
}

func Red(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

func Cyan(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[36m" + s + "\033[0m"
}

func Dim(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}

// HasColor returns true if the terminal likely supports ANSI colors.
func HasColor() bool {
	if runtime.GOOS == "windows" {
		// Windows GUI mode (no console) — no color
		if os.Getenv("TERM") == "" && os.Getenv("WT_SESSION") == "" {
			return false
		}
	}
	term := os.Getenv("TERM")
	return term != "" && term != "dumb"
}
