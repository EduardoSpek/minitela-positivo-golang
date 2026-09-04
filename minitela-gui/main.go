package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// trayIconPath points to a .ico file (the system tray on Windows requires an
// ICO, not PNG).
var trayIconPath string

// appIconPath refers to the generated PNG icon (for desktop shortcut).
var appIconPath string

func init() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	trayIconPath = filepath.Join(dir, "icon.ico")
	appIconPath = filepath.Join(dir, "appicon.png")
}

// singleInstanceMutex is a named kernel mutex that guarantees only one app
// process runs at a time. If another instance already owns it, this process
// exits immediately instead of showing a second window/tray icon.
const singleInstanceMutexName = `Local\MinitelaGoSingleInstance`

// acquireSingleInstance tries to take the single-instance mutex. It returns the
// handle to keep alive for the process lifetime (a second, concurrent call
// would return already-exists) and whether this process won the mutex.
func acquireSingleInstance() (windows.Handle, bool) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")
	namePtr, _ := windows.UTF16PtrFromString(singleInstanceMutexName)
	h, _, lastErr := createMutex.Call(0, 1, uintptr(unsafe.Pointer(namePtr)))
	// ERROR_ALREADY_EXISTS (183) means another live instance owns the mutex.
	if lastErr == windows.ERROR_ALREADY_EXISTS {
		return 0, false
	}
	return windows.Handle(h), h != 0
}

func main() {
	// Refuse to start a second instance: if one is already running (system
	// tray, hidden window), just exit.
	_, acquired := acquireSingleInstance()
	if !acquired {
		// Bring the already-running instance's window to the front so the user
		// sees the app instead of silently nothing happening.
		user32 := windows.NewLazySystemDLL("user32.dll")
		findWindow := user32.NewProc("FindWindowW")
		titlePtr, _ := windows.UTF16PtrFromString("Minitela Go")
		hwnd, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(titlePtr)))
		if hwnd != 0 {
			user32.NewProc("ShowWindow").Call(hwnd, 5 /*SW_SHOW*/)
			user32.NewProc("SetForegroundWindow").Call(hwnd)
		}
		return
	}

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
			if err := app.StartMonitor(10); err != nil {
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
		StartHidden:       true,
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
