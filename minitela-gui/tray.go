package main

import (
	"fmt"
	"image"
	_ "image/png"
	"os"

	"github.com/getlantern/systray"
)

// appCallbacks define the actions the tray can trigger on the App.
type appCallbacks struct {
	ShowWindow   func()
	SetBlight    func(int)
	StartMonitor func()
	StopMonitor  func()
	ToggleAutoStart func()
	Quit         func()
}

var trayCB *appCallbacks

// startTray launches the system tray in its own goroutine.
func startTray(cb *appCallbacks, iconPath string) {
	trayCB = cb
	go func() {
		systray.Run(onTrayReady, onTrayExit)
	}()
}

func trayIconBytes(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

func loadIcon(path string) []byte {
	if path == "" {
		return nil
	}
	return trayIconBytes(path)
}

func onTrayReady() {
	systray.SetTitle("Minitela Go")
	systray.SetTooltip("Minitela Go - Controle a mini tela Positivo")
	if ic := trayIconBytes(trayIconPath); len(ic) > 0 {
		systray.SetIcon(ic)
	}

	// Show window
	mShow := systray.AddMenuItem("Mostrar janela", "Abrir a janela principal")
	// Brightness submenu
	mBri := systray.AddMenuItem("Brilho", "Ajustar brilho da mini tela")
	mB100 := mBri.AddSubMenuItem("100%", "Brilho máximo")
	mB60 := mBri.AddSubMenuItem("60%", "Brilho médio")
	mB30 := mBri.AddSubMenuItem("30%", "Brilho baixo")
	mB0 := mBri.AddSubMenuItem("Desligar (0%)", "Apagar a mini tela")
	// Monitor toggle
	mMon := systray.AddMenuItem("Monitorar CPU/Bateria", "Ligar/desligar monitor")
	systray.AddSeparator()
	mAutostart := systray.AddMenuItem("Iniciar com o Windows", "Alternar auto-início")
	if IsAutoStartEnabled() {
		mAutostart.Check()
	}
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Sair", "Encerrar")

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				if trayCB != nil && trayCB.ShowWindow != nil {
					trayCB.ShowWindow()
				}
			case <-mB100.ClickedCh:
				if trayCB != nil && trayCB.SetBlight != nil {
					trayCB.SetBlight(100)
				}
			case <-mB60.ClickedCh:
				if trayCB != nil && trayCB.SetBlight != nil {
					trayCB.SetBlight(60)
				}
			case <-mB30.ClickedCh:
				if trayCB != nil && trayCB.SetBlight != nil {
					trayCB.SetBlight(30)
				}
			case <-mB0.ClickedCh:
				if trayCB != nil && trayCB.SetBlight != nil {
					trayCB.SetBlight(0)
				}
			case <-mMon.ClickedCh:
				if trayCB != nil {
					if mMon.Checked() {
						mMon.Uncheck()
						if trayCB.StopMonitor != nil {
							trayCB.StopMonitor()
						}
					} else {
						mMon.Check()
						if trayCB.StartMonitor != nil {
							trayCB.StartMonitor()
						}
					}
				}
			case <-mAutostart.ClickedCh:
				if trayCB != nil && trayCB.ToggleAutoStart != nil {
					trayCB.ToggleAutoStart()
				}
				if IsAutoStartEnabled() {
					mAutostart.Check()
				} else {
					mAutostart.Uncheck()
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onTrayExit() {
	// Called when systray.Quit triggers.
	if trayCB != nil && trayCB.Quit != nil {
		trayCB.Quit()
	}
}

var _ = fmt.Sprintf
var _ = image.Rect
