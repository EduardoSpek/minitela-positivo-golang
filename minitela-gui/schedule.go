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

// scheduleRule is a daily time window: every day while the clock is inside
// [Start, End) ("HH:MM", 24h) the mini screen switches to Page on entry and
// the app enforces Brightness for the whole window.
type scheduleRule struct {
	Enabled    bool   `json:"enabled"`
	Start      string `json:"start"` // "HH:MM", 24h
	End        string `json:"end"`   // "HH:MM", 24h
	Page       int32  `json:"page"`  // 2-5
	Brightness int    `json:"brightness"`
}

// scheduleRuleFile is the on-disk shape of one rule, accepting both the
// current window format and the legacy one-shot {"time"} format (migrated to
// a 1-hour window on load).
type scheduleRuleFile struct {
	Enabled    bool   `json:"enabled"`
	Start      string `json:"start"`
	End        string `json:"end"`
	Time       string `json:"time"` // legacy one-shot format
	Page       int32  `json:"page"`
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
	p, err := schedulesConfigPath()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var file struct {
		Rules []scheduleRuleFile `json:"rules"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil
	}
	rules := make([]scheduleRule, 0, len(file.Rules))
	for _, fr := range file.Rules {
		r := scheduleRule{
			Enabled:    fr.Enabled,
			Start:      fr.Start,
			End:        fr.End,
			Page:       fr.Page,
			Brightness: fr.Brightness,
		}
		if r.Start == "" && fr.Time != "" {
			// Legacy one-shot rule: migrate to a 1-hour window.
			r.Start = fr.Time
			r.End = plusHour(fr.Time)
		}
		rules = append(rules, r)
	}
	return rules
}

// plusHour returns HH:MM one hour after the given HH:MM (wrapping midnight).
// Invalid input yields "" so the rule is skipped by the matcher.
func plusHour(hhmm string) string {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return ""
	}
	return t.Add(time.Hour).Format("15:04")
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

// SetSchedules stores the daily time windows, validating each rule's start,
// end, page and brightness. It also resets the window tracking so edited
// rules take effect immediately.
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
	a.schedActive = -1
	a.schedApplied = 0
	a.schedLastCheck = time.Time{}
	a.schedMu.Unlock()
	return nil
}

func validateScheduleRule(r scheduleRule) error {
	if _, err := time.Parse("15:04", r.Start); err != nil {
		return fmt.Errorf("início inválido %q (use HH:MM)", r.Start)
	}
	if _, err := time.Parse("15:04", r.End); err != nil {
		return fmt.Errorf("fim inválido %q (use HH:MM)", r.End)
	}
	if r.Start == r.End {
		return fmt.Errorf("início e fim devem ser diferentes")
	}
	if r.Page < PageNotas || r.Page > PageImagem {
		return fmt.Errorf("página inválida: %d", r.Page)
	}
	if r.Brightness < 0 || r.Brightness > 100 {
		return fmt.Errorf("brilho fora de 0-100: %d", r.Brightness)
	}
	return nil
}

// ruleActive reports whether the clock is inside the rule's daily window.
// Windows may span midnight (End <= Start means overnight).
func ruleActive(r scheduleRule, now time.Time) bool {
	s, err := time.Parse("15:04", r.Start)
	if err != nil {
		return false
	}
	e, err := time.Parse("15:04", r.End)
	if err != nil {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	sm := s.Hour()*60 + s.Minute()
	em := e.Hour()*60 + e.Minute()
	if sm == em {
		return false
	}
	if sm < em {
		return cur >= sm && cur < em
	}
	return cur >= sm || cur < em // overnight
}

// schedEnforceInterval is how often the brightness is re-checked (and
// corrected) while a window is active.
const schedEnforceInterval = 60 * time.Second

// fireDueSchedule watches the daily time windows. On window entry it switches
// the page and sets the brightness at once; while the window stays active it
// re-reads the device brightness every schedEnforceInterval and corrects any
// drift (e.g. manual changes). Outside every window it leaves the device
// alone. Overlapping windows resolve to the first enabled match in list
// order. It returns an error if a serial write fails and stops right away.
func (a *App) fireDueSchedule(c *minitela.Client) error {
	now := time.Now()
	rules := loadSchedulesConfig()

	a.schedMu.Lock()
	defer a.schedMu.Unlock()

	active := -1
	for i := range rules {
		if !rules[i].Enabled {
			continue
		}
		if ruleActive(rules[i], now) {
			active = i
			break
		}
	}

	// Window entry (or rule list change): apply page + brightness now.
	if active != a.schedActive {
		a.schedActive = active
		if active < 0 {
			return nil
		}
		r := rules[active]
		if err := c.SetPage(r.Page); err != nil {
			return err
		}
		if err := c.SetBacklight(r.Brightness); err != nil {
			return err
		}
		a.schedApplied = r.Brightness
		a.schedLastCheck = now
		name := pageName(r.Page)
		runtimeEmit(a.ctx, "schedule-run", map[string]interface{}{
			"start":      r.Start,
			"end":        r.End,
			"page":       name,
			"brightness": r.Brightness,
		})
		return nil
	}
	if active < 0 {
		return nil
	}
	// Same window: enforce at most every schedEnforceInterval.
	if now.Sub(a.schedLastCheck) < schedEnforceInterval {
		return nil
	}
	a.schedLastCheck = now
	r := rules[active]
	// Read the actual brightness and correct on mismatch. If the read fails
	// (firmware may not report register 7), fall back to the tracked value.
	cur := a.schedApplied
	if vals, err := c.GetNumTags([]uint16{minitela.RegSystemBacklight}); err == nil {
		if v, ok := vals[minitela.RegSystemBacklight]; ok {
			cur = int(v)
		}
	}
	if cur != r.Brightness {
		if err := c.SetBacklight(r.Brightness); err != nil {
			return err
		}
	}
	a.schedApplied = r.Brightness
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