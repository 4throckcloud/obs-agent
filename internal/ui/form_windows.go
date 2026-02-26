//go:build windows

package ui

import (
	"fmt"
	"os/exec"
	"strings"
)

// showForm displays a multi-field form using PowerShell System.Windows.Forms.
// Returns field values keyed by FormField.Key, and true if OK was pressed.
func showForm(title string, fields []FormField) (map[string]string, bool) {
	// Build PowerShell script that creates a Windows.Forms dialog
	var sb strings.Builder
	sb.WriteString("Add-Type -AssemblyName System.Windows.Forms\n")
	sb.WriteString("[System.Windows.Forms.Application]::EnableVisualStyles()\n")

	sb.WriteString("$f = New-Object System.Windows.Forms.Form\n")
	sb.WriteString(fmt.Sprintf("$f.Text = '%s'\n", escapePSString(title)))
	sb.WriteString("$f.StartPosition = 'CenterScreen'\n")
	sb.WriteString("$f.FormBorderStyle = 'FixedDialog'\n")
	sb.WriteString("$f.MaximizeBox = $false\n")
	sb.WriteString("$f.MinimizeBox = $false\n")
	sb.WriteString("$f.TopMost = $true\n")

	// Layout: labels + textboxes, stacked vertically
	y := 15
	for i, field := range fields {
		// Label
		sb.WriteString(fmt.Sprintf("$lbl%d = New-Object System.Windows.Forms.Label\n", i))
		sb.WriteString(fmt.Sprintf("$lbl%d.Text = '%s'\n", i, escapePSString(field.Label)))
		sb.WriteString(fmt.Sprintf("$lbl%d.Location = New-Object System.Drawing.Point(15,%d)\n", i, y))
		sb.WriteString(fmt.Sprintf("$lbl%d.AutoSize = $true\n", i))
		sb.WriteString(fmt.Sprintf("$f.Controls.Add($lbl%d)\n", i))
		y += 22

		// TextBox
		sb.WriteString(fmt.Sprintf("$txt%d = New-Object System.Windows.Forms.TextBox\n", i))
		sb.WriteString(fmt.Sprintf("$txt%d.Location = New-Object System.Drawing.Point(15,%d)\n", i, y))
		sb.WriteString(fmt.Sprintf("$txt%d.Size = New-Object System.Drawing.Size(330,24)\n", i))
		sb.WriteString(fmt.Sprintf("$txt%d.Text = '%s'\n", i, escapePSString(field.Default)))
		if field.Password {
			sb.WriteString(fmt.Sprintf("$txt%d.UseSystemPasswordChar = $true\n", i))
		}
		sb.WriteString(fmt.Sprintf("$f.Controls.Add($txt%d)\n", i))
		y += 32
	}

	// Buttons
	y += 5
	sb.WriteString(fmt.Sprintf("$ok = New-Object System.Windows.Forms.Button\n"))
	sb.WriteString(fmt.Sprintf("$ok.Text = 'OK'\n"))
	sb.WriteString(fmt.Sprintf("$ok.Location = New-Object System.Drawing.Point(160,%d)\n", y))
	sb.WriteString("$ok.Size = New-Object System.Drawing.Size(85,28)\n")
	sb.WriteString("$ok.DialogResult = [System.Windows.Forms.DialogResult]::OK\n")
	sb.WriteString("$f.AcceptButton = $ok\n")
	sb.WriteString("$f.Controls.Add($ok)\n")

	sb.WriteString(fmt.Sprintf("$cancel = New-Object System.Windows.Forms.Button\n"))
	sb.WriteString(fmt.Sprintf("$cancel.Text = 'Cancel'\n"))
	sb.WriteString(fmt.Sprintf("$cancel.Location = New-Object System.Drawing.Point(260,%d)\n", y))
	sb.WriteString("$cancel.Size = New-Object System.Drawing.Size(85,28)\n")
	sb.WriteString("$cancel.DialogResult = [System.Windows.Forms.DialogResult]::Cancel\n")
	sb.WriteString("$f.CancelButton = $cancel\n")
	sb.WriteString("$f.Controls.Add($cancel)\n")

	y += 40
	sb.WriteString(fmt.Sprintf("$f.ClientSize = New-Object System.Drawing.Size(360,%d)\n", y))

	// Show dialog and output values pipe-delimited
	sb.WriteString("$r = $f.ShowDialog()\n")
	sb.WriteString("if ($r -ne [System.Windows.Forms.DialogResult]::OK) { exit 1 }\n")
	for i := range fields {
		if i > 0 {
			sb.WriteString("[Console]::Out.Write('|')\n")
		}
		sb.WriteString(fmt.Sprintf("[Console]::Out.Write($txt%d.Text)\n", i))
	}
	sb.WriteString("exit 0\n")

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", sb.String())
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}

	return parseFormOutput(string(out), fields), true
}

// escapePSString escapes single quotes for PowerShell string literals.
func escapePSString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
