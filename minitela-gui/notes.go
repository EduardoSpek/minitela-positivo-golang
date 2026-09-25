package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Repetition modes for a note.
const (
	noteModeOnce   = "once"   // fires at a specific date/time
	noteModeDaily  = "daily"  // fires every day at Time
	noteModeWeekly = "weekly" // fires at Time on the selected weekdays
)

// note holds a scheduled reminder for the Notas screen.
//
// Text is the message shown on the device. Mode selects the repetition:
//   - once:   At carries the exact date/time
//   - daily:  Time ("HH:MM") fires every day
//   - weekly: Time fires on the weekdays enabled in Days
//
// Fired/FiredAt track whether the note is currently displayed (FiredAt is the
// moment it fired) and LastFiredDate prevents a repeating note from firing
// twice on the same date.
type note struct {
	Text string
	// At is used by the "once" mode.
	At time.Time
	// Time is used by the daily/weekly modes ("HH:MM").
	Time string
	// Days enables weekdays for the weekly mode; index 0 = Sunday.
	Days [7]bool
	Mode string

	Fired         bool
	FiredAt       time.Time
	LastFiredDate string // "2006-01-02", for repeating notes
}

// weekdayMaskToBool converts a bitmask (bit 0 = Sunday) into the Days array.
func weekdayMaskToBool(mask int) [7]bool {
	var d [7]bool
	for i := 0; i < 7; i++ {
		d[i] = mask&(1<<i) != 0
	}
	return d
}

// noteRule is the frontend-facing shape of a reminder, used by the bindings.
type noteRule struct {
	Text       string `json:"text"`
	Mode       string `json:"mode"`
	OnceAt     string `json:"onceAt"`     // "2006-01-02T15:04" for "once"
	Time       string `json:"time"`       // "HH:MM" for daily/weekly
	WeekdayBit int    `json:"weekdayBit"` // bitmask for weekly, 0 = every day
	Fired      bool   `json:"fired"`
}

// notesConfig is the on-disk shape. Reminder is the single reminder the stock
// theme can display; the legacy Notes array is still accepted when reading so
// an older config file is not lost.
type notesConfig struct {
	Reminder note   `json:"reminder"`
	Notes    []note `json:"notes"`
}

func notesConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "minitela-gui.exe", "notes.json"), nil
}

// loadNotesConfig reads the persisted reminder, accepting both the current
// single-reminder shape and the legacy three-slot array. Legacy notes without
// a Mode are migrated to "once".
func loadNotesConfig() note {
	var cfg notesConfig
	p, err := notesConfigPath()
	if err != nil {
		return note{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return note{}
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return note{}
	}
	n := cfg.Reminder
	if n.Text == "" && n.At.IsZero() {
		// Legacy file: keep the first reminder that actually has content.
		for _, old := range cfg.Notes {
			if old.Text != "" {
				n = old
				break
			}
		}
	}
	if n.Mode == "" {
		n.Mode = noteModeOnce
	}
	return n
}

func saveNotesConfig(n note) error {
	p, err := notesConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(notesConfig{Reminder: n}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// GetNoteRule returns the current reminder for the UI to prefill.
func (a *App) GetNoteRule() noteRule {
	a.notesMu.Lock()
	n := a.note
	a.notesMu.Unlock()
	r := noteRule{Text: n.Text, Mode: n.Mode, Time: n.Time, Fired: n.Fired}
	if n.Mode == noteModeOnce && !n.At.IsZero() {
		r.OnceAt = n.At.Format("2006-01-02T15:04")
	}
	mask := 0
	for i, on := range n.Days {
		if on {
			mask |= 1 << i
		}
	}
	r.WeekdayBit = mask
	return r
}

// SetNoteRule validates and stores the reminder, clearing the fired state so a
// new configuration takes effect at its next due time.
func (a *App) SetNoteRule(r noteRule) error {
	n, err := r.toNote()
	if err != nil {
		return err
	}
	a.notesMu.Lock()
	a.note = n
	a.notesSig = ""
	err = saveNotesConfig(a.note)
	a.notesMu.Unlock()
	return err
}

func (r noteRule) toNote() (note, error) {
	n := note{Text: r.Text}
	switch r.Mode {
	case noteModeOnce:
		n.Mode = noteModeOnce
		n.At = parseNoteDue(r.OnceAt)
	case noteModeDaily:
		n.Mode = noteModeDaily
		n.Time = r.Time
	case noteModeWeekly:
		n.Mode = noteModeWeekly
		n.Time = r.Time
		mask := r.WeekdayBit
		if mask == 0 {
			// 0 means "no weekday selected" — default to every day so the
			// reminder still fires.
			mask = 0x7F
		}
		n.Days = weekdayMaskToBool(mask)
	default:
		return n, fmt.Errorf("modo de repetição inválido: %q", r.Mode)
	}
	return n, nil
}

// parseNoteDue parses a datetime-local value ("2006-01-02T15:04") into a
// reminder schedule. Empty/invalid values return the zero time.
func parseNoteDue(s string) time.Time {
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local); err == nil {
		return t
	}
	return time.Time{}
}

// noteDue reports whether the note should fire at the given moment, and marks
// it as fired (updating LastFiredDate for repeating notes). It is a pure
// decision function: it does NOT mutate the note; the caller applies the
// result.
func noteDue(n note, now time.Time) bool {
	if n.Text == "" {
		return false
	}
	switch n.Mode {
	case noteModeOnce, "":
		// A one-shot note fires a single time: keep the Fired guard, otherwise
		// its past At would make it fire again on every monitor tick.
		return !n.Fired && !n.At.IsZero() && !now.Before(n.At)
	case noteModeDaily:
		return now.Format("15:04") == n.Time &&
			n.LastFiredDate != now.Format("2006-01-02")
	case noteModeWeekly:
		if now.Format("15:04") != n.Time {
			return false
		}
		if n.LastFiredDate == now.Format("2006-01-02") {
			return false
		}
		return n.Days[int(now.Weekday())]
	}
	return false
}
