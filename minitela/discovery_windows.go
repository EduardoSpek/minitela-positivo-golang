//go:build windows

package minitela

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// FindMinitelaPort locates the COM port of the Minitela device on Windows.
// It runs PowerShell to query Win32_PnPEntity for the USB\VID_0324&PID_0324
// device and extracts its friendly name (e.g. "Dispositivo Serial USB (COM3)").
func FindMinitelaPort() (string, error) {
	script := `
$dev = Get-CimInstance Win32_PnPEntity | Where-Object { $_.DeviceID -like 'USB*VID_0324*' -and $_.Name -match 'COM\d+' } | Select-Object -First 1
if ($dev) {
  if ($dev.Name -match '\((COM\d+)\)') { $Matches[1] } else { $dev.Name }
}
`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("query port: %w", err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", fmt.Errorf("minitela device not found (USB VID_0324)")
	}
	// Normalize to COMx form
	upper := strings.ToUpper(line)
	if len(upper) >= 4 && strings.HasPrefix(upper, "COM") {
		// e.g. "COM3"
		return upper, nil
	}
	// Some environments return the full friendly name; parse COM from it
	if idx := strings.Index(upper, "COM"); idx >= 0 {
		end := idx + 3
		for end < len(upper) && upper[end] >= '0' && upper[end] <= '9' {
			end++
		}
		return upper[idx:end], nil
	}
	return "", fmt.Errorf("could not determine COM port from: %s", line)
}
