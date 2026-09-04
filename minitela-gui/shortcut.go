package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// OneDriveDesktop returns the synchronized OneDrive "Área de Trabalho"
// folder if it exists, otherwise the local Desktop folder.
func OneDriveDesktop() string {
	if od := os.Getenv("OneDrive"); od != "" {
		candidates := []string{
			filepath.Join(od, "Área de Trabalho"),
			filepath.Join(od, "Desktop"),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				return c
			}
		}
	}
	// Fallback to the local known desktop.
	home, _ := os.UserHomeDir()
	if home != "" {
		if fi, err := os.Stat(filepath.Join(home, "Desktop")); err == nil && fi.IsDir() {
			return filepath.Join(home, "Desktop")
		}
	}
	return ""
}

// CreateDesktopShortcut creates a .lnk shortcut to the executable on the
// OneDrive desktop with the given icon path. Returns the shortcut path.
func CreateDesktopShortcut(exePath, shortcutName, iconPath string) (string, error) {
	desktop := OneDriveDesktop()
	if desktop == "" {
		return "", fmt.Errorf("área de trabalho não localizada")
	}
	lnk := filepath.Join(desktop, shortcutName+".lnk")

	// Use WScript.Shell COM to create the shortcut.
	ps := fmt.Sprintf(`
$ws = New-Object -ComObject WScript.Shell
$sc = $ws.CreateShortcut(%q)
$sc.TargetPath = %q
$sc.WorkingDirectory = %q
$sc.IconLocation = %q
$sc.Description = %q
$sc.Save()
`, lnk, exePath, filepath.Dir(exePath), iconPath+",0", "Controlador da Mini Tela Positivo")

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("criar atalho: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return lnk, nil
}
