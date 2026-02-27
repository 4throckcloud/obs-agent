package branding

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

// PrintBanner prints the branded startup banner.
func PrintBanner(version, goos, goarch string, w io.Writer) {
	if HasColor() {
		fmt.Fprintln(w, Orange("  ┌──────────────────────────────────┐"))
		fmt.Fprintln(w, Orange("  │")+"  "+BoldOrange("4 t h R o c k")+"   "+Dim("C l o u d")+"   "+Orange("│"))
		fmt.Fprintf(w, Orange("  │")+"     "+White("OBS Agent")+" %-14s"+Orange("│")+"\n", version)
		fmt.Fprintf(w, Orange("  │")+"     %-24s"+Orange("│")+"\n", Dim(goos+"/"+goarch))
		fmt.Fprintln(w, Orange("  └──────────────────────────────────┘"))
	} else {
		fmt.Fprintln(w, "  ┌──────────────────────────────────┐")
		fmt.Fprintln(w, "  │  4 t h R o c k   C l o u d      │")
		fmt.Fprintf(w, "  │     OBS Agent %-14s      │\n", version)
		fmt.Fprintf(w, "  │     %-24s│\n", goos+"/"+goarch)
		fmt.Fprintln(w, "  └──────────────────────────────────┘")
	}
}

// ANSI color helpers — wrap text in escape codes.
// No-ops on Windows GUI mode or dumb terminals.

// Orange uses the 4thRock brand color (#E94F2D ≈ 38;2;233;79;45).
func Orange(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[38;2;233;79;45m" + s + "\033[0m"
}

// BoldOrange uses bold + 4thRock brand color.
func BoldOrange(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[1;38;2;233;79;45m" + s + "\033[0m"
}

// White uses bright white for emphasis.
func White(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[1;97m" + s + "\033[0m"
}

func Green(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[38;2;233;79;45m" + s + "\033[0m"
}

func Yellow(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[38;2;255;110;74m" + s + "\033[0m"
}

func Red(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[38;2;139;53;37m" + s + "\033[0m"
}

func Cyan(s string) string {
	if !HasColor() {
		return s
	}
	return "\033[38;2;212;114;74m" + s + "\033[0m"
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
