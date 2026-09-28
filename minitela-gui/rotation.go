package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// rotationPages are the pages the automatic rotation cycles through: Notas,
// Monitor, Clima and the three Imagem slots. The WhatsApp page (1) is not
// listed on purpose — the app does not use it.
var rotationPages = []int{
	int(PageNotas), int(PageMonitor), int(PageClima),
	5, 6, 7, // Gif1, Gif2, Gif3
}

const (
	rotationIntervalDefault = 10
	rotationIntervalMin     = 3
	rotationIntervalMax     = 600
)

// rotationConfig is the on-disk shape of the automatic screen rotation.
type rotationConfig struct {
	Enabled     bool  `json:"enabled"`
	IntervalSec int   `json:"intervalSec"`
	Pages       []int `json:"pages"`
}

func rotationConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "minitela-gui.exe", "rotation.json"), nil
}

// normalizeRotationConfig clamps the interval and keeps only known pages,
// preserving the canonical order and removing duplicates. It always returns at
// least the full set of pages, so a corrupt file cannot leave rotation with an
// empty cycle.
func normalizeRotationConfig(c rotationConfig) rotationConfig {
	if c.IntervalSec < rotationIntervalMin {
		c.IntervalSec = rotationIntervalDefault
	}
	if c.IntervalSec > rotationIntervalMax {
		c.IntervalSec = rotationIntervalMax
	}
	want := make(map[int]bool, len(c.Pages))
	for _, p := range c.Pages {
		want[p] = true
	}
	pages := make([]int, 0, len(rotationPages))
	for _, p := range rotationPages {
		if want[p] {
			pages = append(pages, p)
		}
	}
	if len(pages) == 0 {
		pages = append(pages, rotationPages...)
	}
	c.Pages = pages
	return c
}

func loadRotationConfig() rotationConfig {
	var c rotationConfig
	p, err := rotationConfigPath()
	if err != nil {
		return normalizeRotationConfig(c)
	}
	b, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return normalizeRotationConfig(c)
}

func saveRotationConfig(c rotationConfig) error {
	p, err := rotationConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// GetRotationConfig returns the rotation settings for the UI.
func (a *App) GetRotationConfig() rotationConfig {
	return loadRotationConfig()
}

// SetRotationConfig validates, stores and applies the rotation settings.
func (a *App) SetRotationConfig(c rotationConfig) error {
	c = normalizeRotationConfig(c)
	if err := saveRotationConfig(c); err != nil {
		return err
	}
	a.applyRotation(c)
	return nil
}

// SetRotationEnabled turns the rotation on or off, keeping the other settings.
func (a *App) SetRotationEnabled(on bool) error {
	c := loadRotationConfig()
	if c.Enabled == on && a.rotationRunning() == on {
		return nil
	}
	c.Enabled = on
	if err := saveRotationConfig(c); err != nil {
		return err
	}
	a.applyRotation(c)
	return nil
}

// StopRotation disables the rotation immediately (used by the "Parar agora"
// button).
func (a *App) StopRotation() error {
	return a.SetRotationEnabled(false)
}

func (a *App) applyRotation(c rotationConfig) {
	if !c.Enabled {
		a.stopRotation()
	} else {
		a.startRotation(c.IntervalSec)
	}
	runtimeEmit(a.ctx, "rotation-changed", c)
}

// rotationRunning reports whether the rotation loop is active.
func (a *App) rotationRunning() bool {
	a.rotMu.Lock()
	defer a.rotMu.Unlock()
	return a.rotRunning
}

// rotationPaused reports whether the rotation is holding for a fired reminder.
func (a *App) rotationPaused() bool {
	a.rotMu.Lock()
	defer a.rotMu.Unlock()
	return a.rotPaused
}

// pauseRotation holds the rotation (a reminder is displayed) without turning
// it off, so it can resume when the user moves on.
func (a *App) pauseRotation() {
	a.rotMu.Lock()
	a.rotPaused = true
	a.rotMu.Unlock()
}

// onManualPageChange is called when the user switches page on purpose (screen
// buttons, Exibir Imagem N or the dedicated notebook key). If a reminder is
// being displayed the rotation resumes; otherwise a manual change means the
// user took over and the rotation is turned off.
func (a *App) onManualPageChange() {
	a.rotMu.Lock()
	paused := a.rotPaused
	a.rotPaused = false
	a.rotMu.Unlock()
	if paused {
		return
	}
	if !a.rotationRunning() {
		return
	}
	c := loadRotationConfig()
	if !c.Enabled {
		return
	}
	c.Enabled = false
	_ = saveRotationConfig(c)
	a.stopRotation()
	runtimeEmit(a.ctx, "rotation-changed", c)
	runtimeEmit(a.ctx, "toast", "Rotação automática desligada")
}

// startRotation (re)starts the loop with the given interval.
func (a *App) startRotation(intervalSec int) {
	a.stopRotation()
	if intervalSec < rotationIntervalMin {
		intervalSec = rotationIntervalDefault
	}
	a.rotMu.Lock()
	a.rotRunning = true
	a.rotPaused = false
	a.rotIdx = 0
	stop := make(chan struct{})
	a.rotStop = stop
	a.rotMu.Unlock()

	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				a.rotationTick()
			}
		}
	}()
}

// stopRotation halts the loop if it is running.
func (a *App) stopRotation() {
	a.rotMu.Lock()
	was := a.rotRunning
	a.rotRunning = false
	a.rotPaused = false
	stop := a.rotStop
	a.rotStop = nil
	a.rotMu.Unlock()
	if was && stop != nil {
		close(stop)
	}
}

// rotationTick advances one step of the cycle. It writes a single page
// register, so the serial bus load stays negligible.
func (a *App) rotationTick() {
	a.rotMu.Lock()
	if !a.rotRunning || a.rotPaused {
		a.rotMu.Unlock()
		return
	}
	idx := a.rotIdx
	a.rotMu.Unlock()

	cfg := loadRotationConfig()
	if len(cfg.Pages) == 0 {
		return
	}
	a.rotMu.Lock()
	if a.rotIdx != idx {
		// Configuration changed while we were reading it: resync.
		a.rotIdx = 0
		idx = 0
	}
	a.rotIdx = (a.rotIdx + 1) % len(cfg.Pages)
	page := cfg.Pages[a.rotIdx]
	a.rotMu.Unlock()

	c, err := a.get()
	if err != nil {
		// Not connected: keep the step, write it on the next tick.
		return
	}
	_ = c.SetPage(int32(page))
}

// startRotationOnStartup starts the rotation after launch when it is enabled.
func (a *App) startRotationOnStartup() {
	c := loadRotationConfig()
	if c.Enabled {
		a.startRotation(c.IntervalSec)
	}
}
