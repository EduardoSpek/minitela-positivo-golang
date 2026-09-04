package main

import (
	"context"
	"os/exec"
	"strings"
	"syscall"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// runtimeEmit forwards an event to the frontend (no-op-safe).
func runtimeEmit(ctx context.Context, event string, data interface{}) {
	if ctx == nil {
		return
	}
	runtime.EventsEmit(ctx, event, data)
}

// gatherSystemInfo collects CPU / battery / WiFi information from Windows.
func gatherSystemInfo() systemInfo {
	info := systemInfo{
		CPU:      runPS(`(Get-CimInstance Win32_Processor).LoadPercentage`),
		Battery:  runPS(`(Get-CimInstance Win32_Battery).EstimatedChargeRemaining`),
	}
	ssid, sig := getWiFi()
	info.WifiSSID = ssid
	info.WifiSignal = sig
	btName, btConnected := getBluetoothName()
	info.BTName = btName
	info.BTConnected = btConnected
	return info
}

// getBluetoothName returns the real connected Bluetooth device name, or an
// empty string when no device is actually connected. It filters out the OS
// adapter/enumerators and AVRCP transport entries so we only report a genuine
// paired device's friendly name (DeviceID under BTHENUM\DEV_).
func getBluetoothName() (name string, connected bool) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance -Namespace root/cimv2 -ClassName Win32_PnPEntity | Where-Object { $_.PNPClass -eq 'Bluetooth' -and $_.Name -and $_.DeviceID -like 'BTHENUM*' -and $_.Name -notmatch 'RFCOMM|Enumerador|Adapter|Transporte|Avrcp|AVCTP|AVRCP' } | Select-Object -ExpandProperty Name -First 1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}

func runPS(expr string) string {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", expr)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "-"
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "-"
	}
	return s
}

func getWiFi() (string, string) {
	cmd := exec.Command("netsh", "wlan", "show", "interfaces")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "-", ""
	}
	var ssid, sig string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		lower := strings.ToLower(l)
		if strings.HasPrefix(lower, "ssid") && !strings.Contains(lower, "bssid") {
			if parts := strings.SplitN(l, ":", 2); len(parts) > 1 {
				ssid = strings.TrimSpace(parts[1])
			}
		}
		if strings.Contains(lower, "sinal") || strings.Contains(lower, "signal") {
			if parts := strings.SplitN(l, ":", 2); len(parts) > 1 {
				sig = strings.TrimSpace(strings.TrimSuffix(parts[1], "%"))
			}
		}
	}
	return ssid, sig
}
