package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// CliUI uses stdin/stdout for interaction — fallback for headless environments.
type CliUI struct {
	reader *bufio.Reader
}

// NewCliUI returns a new CLI-based UI.
func NewCliUI() *CliUI {
	return &CliUI{reader: bufio.NewReader(os.Stdin)}
}

func (c *CliUI) Info(title, message string) {
	fmt.Printf("[%s] %s\n", title, message)
}

func (c *CliUI) Error(title, message string) {
	fmt.Fprintf(os.Stderr, "[%s] %s\n", title, message)
}

func (c *CliUI) Entry(title, text, defaultValue string) (string, bool) {
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", text, defaultValue)
	} else {
		fmt.Printf("%s: ", text)
	}
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", false
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, true
	}
	return line, true
}

func (c *CliUI) Password(title, text string) (string, bool) {
	fmt.Printf("%s: ", text)
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(line), true
}

func (c *CliUI) Confirm(title, message string) bool {
	fmt.Printf("%s [Y/n]: ", message)
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "" || line == "y" || line == "yes"
}
