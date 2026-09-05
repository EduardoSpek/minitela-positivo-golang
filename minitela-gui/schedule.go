package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"minitela/minitela"
)

// scheduleRule is a daily preset: every day at Time ("HH:MM") the mini screen
// switches to Page with the given Brightness.
type scheduleRule struct {
	Enabled    bool   `json:"enabled"`
	Time       string `json:"time"` // "HH:MM", 24h
	Page       int32  `json:"page"` // 2-5
	Brightness int    `json:"brightness"`
}

// schedulesConfig is the on-disk shape of the daily presets.
type schedulesConfig struct {
	Rules []scheduleRule `json:"rules"`
}

func schedulesConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "minitela-gui.exe", "schedules.json"), nil
}

func loadSchedulesConfig() []scheduleRule {
	var cfg schedulesConfig
	p, err := schedulesConfigPath()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	return cfg.Rules
}

func saveSchedulesConfig(rules []scheduleRule) error {
	p, err := schedulesConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(schedulesConfig{Rules: rules}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// GetSchedules returns the saved daily presets for the Agenda screen.
func (a *App) GetSchedules() []scheduleRule {
	return loadSchedulesConfig()
}

// SetSchedules stores the daily presets, validating each rule's time, page and
// brightness. It also resets the "last fired" tracking so edited rules can run
// again at their next matching minute.
func (a *App) SetSchedules(rules []scheduleRule) error {
	for i := range rules {
		if err := validateScheduleRule(rules[i]); err != nil {
			return fmt.Errorf("regra %d: %w", i+1, err)
		}
	}
	if err := saveSchedulesConfig(rules); err != nil {
		return err
	}
	a.schedMu.Lock()
	a.schedLast = make(map[int]string)
	a.schedMu.Unlock()
	return nil
}

func validateScheduleRule(r scheduleRule) error {
	t, err := time.Parse("15:04", r.Time)
	if err != nil {
		return fmt.Errorf("horário inválido %q (use HH:MM)", r.Time)
	}
	_ = t
	if r.Page < PageNotas || r.Page > PageImagem {
		return fmt.Errorf("página inválida: %d", r.Page)
	}
	if r.Brightness < 0 || r.Brightness > 100 {
		return fmt.Errorf("brilho fora de 0-100: %d", r.Brightness)
	}
	return nil
}

// fireDueSchedule applies any daily preset whose HH:MM matches the current
// minute (once per minute, tracking the last applied minute per rule index).
// It returns an error if a serial write fails and stops right away.
func (a *App) fireDueSchedule(c *minitela.Client) error {
	now := time.Now()
	hhmm := now.Format("15:04")
	rules := loadSchedulesConfig()

	a.schedMu.Lock()
	defer a.schedMu.Unlock()

	for i, r := range rules {
		if !r.Enabled {
			continue
		}
		if r.Time != hhmm {
			continue
		}
		// Already fired this minute: avoid repeating SetPage/SetBacklight
		// on every 1s tick while the clock stays on the same minute.
		if a.schedLast[i] == hhmm {
			continue
		}
		if err := c.SetPage(r.Page); err != nil {
			return err
		}
		if err := c.SetBacklight(r.Brightness); err != nil {
			return err
		}
		a.schedLast[i] = hhmm
		name := pageName(r.Page)
		runtimeEmit(a.ctx, "schedule-run", map[string]interface{}{
			"time":       hhmm,
			"page":       name,
			"brightness": r.Brightness,
		})
	}
	return nil
}

func pageName(page int32) string {
	switch page {
	case PageNotas:
		return "Notas"
	case PageMonitor:
		return "Monitor"
	case PageClima:
		return "Clima"
	case PageImagem:
		return "Imagem"
	}
	return strconv.Itoa(int(page))
}