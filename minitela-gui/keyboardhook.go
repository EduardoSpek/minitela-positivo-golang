package main

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	kbdUser32       = syscall.NewLazyDLL("user32.dll")
	kbdSetHook      = kbdUser32.NewProc("SetWindowsHookExW")
	kbdUnhook       = kbdUser32.NewProc("UnhookWindowsHookEx")
	kbdCallNext     = kbdUser32.NewProc("CallNextHookEx")
	kbdGetMessage   = kbdUser32.NewProc("GetMessageW")
	kbdSetProcessDP = kbdUser32.NewProc("SetProcessDPIAware")
)

const (
	kbdWH_KEYBOARD_LL = 13
	kbdWM_KEYDOWN     = 0x0100
	kbdWM_SYSKEYDOWN  = 0x0104
	kbdWM_KEYUP       = 0x0101
	kbdWM_SYSKEYUP    = 0x0105
)

// kbdVkPage is the dedicated notebook key that cycles the mini screen pages
// (VK 0x7F, scan 0x67 on the target hardware).
const kbdVkPage = 0x7F

type kbdLLHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

var (
	kbdHookMu     sync.Mutex
	kbdHookHandle uintptr
	kbdApp        *App
)

// kbdCallback is the low-level keyboard hook invoked on every key event.
func kbdCallback(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 {
		k := (*kbdLLHookStruct)(unsafe.Pointer(lParam))
		if k.VkCode == kbdVkPage && (wParam == kbdWM_KEYDOWN || wParam == kbdWM_SYSKEYDOWN) {
			if a := kbdApp; a != nil {
				a.nextPage()
			}
		}
	}
	ret, _, _ := kbdCallNext.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

// installPageHook installs a global low-level keyboard hook that advances the
// mini screen to the next page whenever the dedicated notebook key is pressed.
// It runs its own message pump on a background goroutine and returns immediately.
func installPageHook(a *App) {
	kbdHookMu.Lock()
	defer kbdHookMu.Unlock()
	if kbdHookHandle != 0 {
		return // already installed
	}
	kbdApp = a
	cb := syscall.NewCallback(kbdCallback)
	h, _, _ := kbdSetHook.Call(kbdWH_KEYBOARD_LL, cb, 0, 0)
	if h == 0 {
		return
	}
	kbdHookHandle = h
	go messagePump(h)
}

// messagePump keeps the calling thread alive so the LL hook receives events.
func messagePump(h uintptr) {
	var msg struct {
		HWnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      struct{ X, Y int32 }
	}
	for {
		r, _, _ := kbdGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 {
			break
		}
		kbdCallNext.Call(h, 0, uintptr(msg.Message), msg.WParam)
	}
	kbdHookMu.Lock()
	kbdHookHandle = 0
	kbdHookMu.Unlock()
}
