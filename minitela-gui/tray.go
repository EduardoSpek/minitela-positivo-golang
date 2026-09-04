package main

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// appCallbacks define the actions the tray can trigger on the App.
type appCallbacks struct {
	ShowWindow      func()
	SetBlight       func(int)
	StartMonitor    func()
	StopMonitor     func()
	ToggleAutoStart func()
	Quit            func()
}

var (
	trayCB  *appCallbacks
	trayPNG string
)

// Win32 shell notifications for a custom tray icon. We roll our own tray so we
// can react to a left-button double-click (open the window) in addition to the
// classic context menu — the stock getlantern/systray only shows a menu.

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	procCreateWindowEx  = user32.NewProc("CreateWindowExW")
	procDefWindowProc   = user32.NewProc("DefWindowProcW")
	procRegisterClassEx = user32.NewProc("RegisterClassExW")
	procUnregisterClass = user32.NewProc("UnregisterClassW")
	procGetMessage      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage = user32.NewProc("DispatchMessageW")
	procShellNotify     = shell32.NewProc("Shell_NotifyIconW")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	procCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	procDestroyMenu     = user32.NewProc("DestroyMenu")
	procAppendMenu      = user32.NewProc("AppendMenuW")
	procSetMenuDefault  = user32.NewProc("SetMenuDefaultItem")
)

const (
	wmSystrayMessage  = 0x0400 + 1 // WM_APP + 1
	wmTaskbarCreated  = 0x8000     // WM_APP + 0x8000, re-register on shell restart
	callBackMessageID = 1

	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_COMMAND       = 0x0111
	WM_DESTROY       = 0x0002
	WM_RBUTTONUP     = 0x0205
	WM_LBUTTONDOWN   = 0x0201

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	MF_STRING   = 0x00000000
	MF_SEPARATOR = 0x00000800
	MF_DEFAULT  = 0x00001000

	WM_USER = 0x0400
)

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	// remaining fields unused, keep struct storable via cbSize
	dwState        uint32
	dwStateMask    uint32
	szInfo         [256]uint16
	uVersion       uint32
	szInfoTitle    [64]uint16
	dwInfoFlags    uint32
	guidItem       windows.GUID
	hBalloonIcon   uintptr
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

const windowClassName = "MinitelaGoTrayWindow"

var (
	trayWindowHWND uintptr
	trayWindowOnce sync.Once
	trayNID        notifyIconData
	trayIconHICON  uintptr
)

// startTray launches the custom Windows tray in its own goroutine.
func startTray(cb *appCallbacks, iconPath string) {
	trayCB = cb
	trayPNG = iconPath
	go trayLoop()
}

func trayLoop() {
	trayWindowOnce.Do(createTrayWindow)
	if trayWindowHWND == 0 {
		return
	}
	var msg struct {
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  uintptr
		time    uint32
		pt      struct{ x, y int32 }
	}
	for {
		r, _, err := procGetMessage.Call(
			uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 { // WM_QUIT
			break
		}
		if int32(r) == -1 {
			if err != nil {
				break
			}
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func createTrayWindow() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	hInst, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	classNamePtr, _ := windows.UTF16PtrFromString(windowClassName)

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		style:         0,
		lpfnWndProc:   windows.NewCallback(trayWndProc),
		hInstance:     hInst,
		lpszClassName: classNamePtr,
	}
	procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(classNamePtr)),
		uintptr(unsafe.Pointer(classNamePtr)), // window name
		0, 0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	trayWindowHWND = hwnd

	trayIconHICON = loadTrayIconHICON(trayPNG)

	trayNID = notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             hwnd,
		uID:              callBackMessageID,
		uFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		uCallbackMessage: wmSystrayMessage,
		hIcon:            trayIconHICON,
	}
	szTip := [128]uint16{}
	tip := windows.StringToUTF16("Minitela Go - Controle a mini tela Positivo")
	copyLen := len(tip)
	if copyLen > len(szTip) {
		copyLen = len(szTip)
	}
	copy(szTip[:], tip[:copyLen])
	trayNID.szTip = szTip

	procShellNotify.Call(NIM_ADD, uintptr(unsafe.Pointer(&trayNID)))
}

// loadTrayIconHICON loads the tray .ico into a Win32 HICON so it can be shown
// next to the menu and the tooltip. It falls back silently when absent.
func loadTrayIconHICON(path string) uintptr {
	if path == "" {
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if len(b) == 0 {
		return 0
	}
	// Write to a temp file and use LoadImage with LR_LOADFROMFILE, since that is
	// the reliable way to turn an on-disk .ico into a HICON.
	tmp := filepath.Join(os.TempDir(), "minitela_tray.ico")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return 0
	}
	hi, _, _ := user32.NewProc("LoadImageW").Call(
		0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(tmp))),
		1, // IMAGE_ICON
		16, 16,
		0x0020, // LR_LOADFROMFILE
		0,
	)
	_ = os.Remove(tmp)
	return hi
}

// trayWndProc is the window procedure for the tray message-only window. It is
// kept alive by being registered as a callback via windows.NewCallback.
func trayWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmTaskbarCreated:
		// explorer.exe restarted; re-register the icon
		procShellNotify.Call(NIM_ADD, uintptr(unsafe.Pointer(&trayNID)))
	case wmSystrayMessage:
		switch lParam {
		case WM_LBUTTONDBLCLK:
			// Double-click on the tray icon opens the window.
			if trayCB != nil && trayCB.ShowWindow != nil {
				trayCB.ShowWindow()
			}
		case WM_LBUTTONUP:
			procSetForeground.Call(trayWindowHWND)
			showTrayMenu()
		case WM_RBUTTONUP:
			procSetForeground.Call(trayWindowHWND)
			showTrayMenu()
		}
	case WM_COMMAND:
		menuItemID := int32(wParam)
		if menuItemID != -1 {
			handleTrayCommand(uint32(wParam))
		}
	case WM_DESTROY:
		procShellNotify.Call(NIM_DELETE, uintptr(unsafe.Pointer(&trayNID)))
	}
	ret, _, _ := procDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

const (
	cmdShow       = 1001
	cmdB100       = 1002
	cmdB60        = 1003
	cmdB30        = 1004
	cmdB0         = 1005
	cmdMonitor    = 1006
	cmdAutostart  = 1007
	cmdQuit       = 1008
)

func handleTrayCommand(id uint32) {
	switch id {
	case cmdShow:
		if trayCB != nil && trayCB.ShowWindow != nil {
			trayCB.ShowWindow()
		}
	case cmdB100:
		if trayCB != nil && trayCB.SetBlight != nil {
			trayCB.SetBlight(100)
		}
	case cmdB60:
		if trayCB != nil && trayCB.SetBlight != nil {
			trayCB.SetBlight(60)
		}
	case cmdB30:
		if trayCB != nil && trayCB.SetBlight != nil {
			trayCB.SetBlight(30)
		}
	case cmdB0:
		if trayCB != nil && trayCB.SetBlight != nil {
			trayCB.SetBlight(0)
		}
	case cmdMonitor:
		if trayCB != nil {
			if trayCB.StopMonitor != nil {
				trayCB.StopMonitor()
			}
			if trayCB.StartMonitor != nil {
				trayCB.StartMonitor()
			}
		}
	case cmdAutostart:
		if trayCB != nil && trayCB.ToggleAutoStart != nil {
			trayCB.ToggleAutoStart()
		}
	case cmdQuit:
		if trayCB != nil && trayCB.Quit != nil {
			trayCB.Quit()
		}
	}
}

func showTrayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	must := func(id uint32, text string, def bool) {
		if def {
			procAppendMenu.Call(menu, MF_STRING|MF_DEFAULT, uintptr(id), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
		} else {
			procAppendMenu.Call(menu, MF_STRING, uintptr(id), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
		}
	}
	must(cmdShow, "Mostrar janela", true)
	procAppendMenu.Call(menu, MF_SEPARATOR, 0, 0)
	must(cmdB100, "Brilho 100%", false)
	must(cmdB60, "Brilho 60%", false)
	must(cmdB30, "Brilho 30%", false)
	must(cmdB0, "Desligar (0%)", false)
	procAppendMenu.Call(menu, MF_SEPARATOR, 0, 0)
	as := "Iniciar com o Windows"
	if IsAutoStartEnabled() {
		as = "[x] Iniciar com o Windows"
	}
	must(cmdAutostart, as, false)
	procAppendMenu.Call(menu, MF_SEPARATOR, 0, 0)
	must(cmdQuit, "Sair", false)

	var pt struct{ x, y int32 }
	procGetCursorPos := user32.NewProc("GetCursorPos")
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	procTrackPopupMenu.Call(
		menu,
		0x00000002, // TPM_RETURNCMD
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		trayWindowHWND,
		0,
	)
	procDestroyMenu.Call(menu)
}
