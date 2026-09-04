//go:build !windows

package minitela

import (
	"fmt"
	"os"
)

// FindMinitelaPort returns the COM port configured via the environment
// variable MINITELA_PORT. Used on non-Windows platforms where autodetection
// through the Windows registry is unavailable.
func FindMinitelaPort() (string, error) {
	if p := os.Getenv("MINITELA_PORT"); p != "" {
		return p, nil
	}
	// Common Linux device for the Positivo minitela CDC-ACM.
	if _, err := os.Stat("/dev/ttyACM0"); err == nil {
		return "/dev/ttyACM0", nil
	}
	return "", fmt.Errorf("set MINITELA_PORT or connect the device")
}
