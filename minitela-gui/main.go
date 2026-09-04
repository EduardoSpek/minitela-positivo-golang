package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// trayIconPath is set at init and used by the tray to load its icon.
var trayIconPath string

// appIconPath refers to the generated PNG icon (for shortcut).
var appIconPath string

func init() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	trayIconPath = filepath.Join(dir, "appicon.png")
	appIconPath = filepath.Join(dir, "appicon.png")
}

func main() {
	app := NewApp()

	cb := &appCallbacks{
		ShowWindow: func() {
			runtime.WindowShow(app.ctx)
		},
		SetBlight: func(v int) {
			if err := app.SetBacklight(v); err != nil {
				log.Printf("brilho: %v", err)
			}
		},
		StartMonitor: func() {
			if err := app.StartMonitor(5); err != nil {
				log.Printf("monitor: %v", err)
			}
		},
		StopMonitor: func() {
			app.StopMonitor()
		},
		ToggleAutoStart: func() {
			exe, _ := os.Executable()
			if IsAutoStartEnabled() {
				_ = ClearAutoStart()
			} else {
				_ = SetAutoStart(exe, "-minimized")
			}
		},
		Quit: func() {
			if app.ctx != nil {
				runtime.Quit(app.ctx)
			}
		},
	}

	// Start the system tray.
	startTray(cb, trayIconPath)

	err := wails.Run(&options.App{
		Title:             "Minitela Go",
		Width:             980,
		Height:            720,
		MinWidth:          860,
		MinHeight:         600,
		StartHidden:       startMinimized(),
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 17, G: 20, B: 34, A: 255},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// helpers referenced to keep os/context imports wired
var _ = context.Background

// startMinimized reports whether the app was launched with -minimized
// (used by the "start with Windows" auto-start entry).
func startMinimized() bool {
	for _, a := range os.Args[1:] {
		if a == "-minimized" {
			return true
		}
	}
	return false
}
