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

type notesConfig struct {
	Notes [3]note `json:"notes"`
}

func notesConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "minitela-gui.exe", "notes.json"), nil
}

// loadNotesConfig reads the persisted reminders, migrating the legacy one-shot
// format (no Mode) to the current representation.
func loadNotesConfig() [3]note {
	var cfg notesConfig
	p, err := notesConfigPath()
	if err != nil {
		return cfg.Notes
	}
	b, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	// Migrate legacy notes (Mode empty) to "once".
	for i := range cfg.Notes {
		if cfg.Notes[i].Mode == "" {
			cfg.Notes[i].Mode = noteModeOnce
		}
	}
	return cfg.Notes
}

func saveNotesConfig(notes [3]note) error {
	p, err := notesConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(notesConfig{Notes: notes}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// GetNoteRules returns the current reminders for the UI to prefill.
func (a *App) GetNoteRules() []noteRule {
	a.notesMu.Lock()
	defer a.notesMu.Unlock()
	out := make([]noteRule, 0, len(a.notes))
	for _, n := range a.notes {
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
		out = append(out, r)
	}
	return out
}

// SetNoteRules validates and stores up to 3 reminders, resetting the fired
// state so a new configuration takes effect on its next due time.
func (a *App) SetNoteRules(rules []noteRule) error {
	var notes [3]note
	for i, r := range rules {
		if i > 2 {
			break
		}
		n, err := r.toNote()
		if err != nil {
			return fmt.Errorf("lembrete %d: %w", i+1, err)
		}
		notes[i] = n
	}
	a.notesMu.Lock()
	a.notes = notes
	a.notesSig = ""
	err := saveNotesConfig(a.notes)
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
