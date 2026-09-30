package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// Repetition modes for a note.
const (
	noteModeOnce   = "once"   // fires at a specific date/time
	noteModeDaily  = "daily"  // fires every day at Time
	noteModeWeekly = "weekly" // fires at Time on the selected weekdays
)

// note is one scheduled reminder. The stock theme has a single text field
// (register 1090), so the app keeps as many notes as the user wants but only
// the most recent fired one is shown on the device.
type note struct {
	// ID is stable across saves so the app can tell which note is displayed.
	ID string
	// Text is the message shown on the device.
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

// noteDisplay is the snapshot of what the device is currently showing. It is
// deliberately independent from the editable note list: editing a note does
// not change the screen, and the screen only changes when a note fires (or
// when the displayed note is removed from the list).
type noteDisplay struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// notesConfig is the on-disk shape. Notes is the editable list; Displayed is the
// snapshot currently on the mini screen. The legacy "reminder" field is still
// accepted when reading so older config files are not lost.
type notesConfig struct {
	Notes     []note       `json:"notes"`
	Displayed *noteDisplay `json:"displayed,omitempty"`
	Reminder  *note        `json:"reminder,omitempty"`
}

func notesConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "minitela-gui.exe", "notes.json"), nil
}

// loadNotesConfig reads the persisted notes, accepting the current list, the
// previous single "reminder" and the oldest three-slot array. Notes without a
// Mode are migrated to "once" and every note gets an ID.
func loadNotesConfig() ([]note, *noteDisplay) {
	p, err := notesConfigPath()
	if err != nil {
		return nil, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, nil
	}
	var cfg notesConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, nil
	}
	notes := cfg.Notes
	if len(notes) == 0 && cfg.Reminder != nil && (cfg.Reminder.Text != "" || !cfg.Reminder.At.IsZero()) {
		notes = append(notes, *cfg.Reminder)
	}
	for i := range notes {
		if notes[i].Mode == "" {
			notes[i].Mode = noteModeOnce
		}
		if notes[i].ID == "" {
			notes[i].ID = newNoteID()
		}
	}
	disp := cfg.Displayed
	if disp == nil || disp.Text == "" {
		// Legacy files have no snapshot: rebuild it from the reminder that
		// fired most recently, so the screen does not fall back to
		// "Sem notas" on upgrade.
		disp = displayedFromNotes(notes)
	}
	return notes, disp
}

// displayedFromNotes returns the snapshot of the most recently fired note, or
// nil when nothing has fired yet.
func displayedFromNotes(notes []note) *noteDisplay {
	best := -1
	for i := range notes {
		if notes[i].FiredAt.IsZero() {
			continue
		}
		if best < 0 || notes[i].FiredAt.After(notes[best].FiredAt) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	return &noteDisplay{ID: notes[best].ID, Text: notes[best].Text}
}

func saveNotesConfig(notes []note, disp *noteDisplay) error {
	p, err := notesConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(notesConfig{Notes: notes, Displayed: disp}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// noteIDSeq makes ids unique even when two notes are created in the same
// nanosecond tick.
var noteIDSeq atomic.Uint64

// newNoteID returns a short unique id for a note.
func newNoteID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) +
		"-" + strconv.FormatUint(noteIDSeq.Add(1), 36)
}

// notesScreenText returns what the device should display for the Notes screen:
// the snapshot of the last fired reminder, or the theme placeholder.
func notesScreenText(disp *noteDisplay) string {
	if disp != nil && disp.Text != "" {
		return normalizeText(disp.Text)
	}
	return "Sem notas"
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
	ID         string `json:"id"`
	Text       string `json:"text"`
	Mode       string `json:"mode"`
	OnceAt     string `json:"onceAt"`     // "2006-01-02T15:04" for "once"
	Time       string `json:"time"`       // "HH:MM" for daily/weekly
	WeekdayBit int    `json:"weekdayBit"` // bitmask for weekly, 0 = every day
	Fired      bool   `json:"fired"`
}

// GetNotes returns the saved reminder list so the UI can prefill it.
func (a *App) GetNotes() []noteRule {
	a.notesMu.Lock()
	notes := a.notes
	a.notesMu.Unlock()
	out := make([]noteRule, 0, len(notes))
	for _, n := range notes {
		r := noteRule{ID: n.ID, Text: n.Text, Mode: n.Mode, Time: n.Time, Fired: n.Fired}
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

// SetNotes stores the whole reminder list. It keeps the fired state of notes
// that still exist and leaves the displayed snapshot untouched, so saving
// never clears or changes the mini screen. Removing the displayed note clears
// the screen instead.
func (a *App) SetNotes(rules []noteRule) error {
	incoming := make([]note, 0, len(rules))
	for i, r := range rules {
		n, err := r.toNote()
		if err != nil {
			return fmt.Errorf("lembrete %d: %w", i+1, err)
		}
		n.ID = r.ID
		incoming = append(incoming, n)
	}

	a.notesMu.Lock()
	merged := mergeNotes(a.notes, incoming)
	disp := a.noteDisp
	if disp != nil && !containsNoteID(merged, disp.ID) {
		// The reminder shown on the screen was removed from the list.
		disp = nil
		a.noteDisp = nil
	}
	a.notes = merged
	err := saveNotesConfig(a.notes, disp)
	a.notesMu.Unlock()
	return err
}

func containsNoteID(notes []note, id string) bool {
	if id == "" {
		return false
	}
	for _, n := range notes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// mergeNotes applies the edited list while preserving the fired state of
// reminders that still exist. Notes without an ID are new: one is generated.
func mergeNotes(old, incoming []note) []note {
	byID := make(map[string]note, len(old))
	for _, n := range old {
		if n.ID != "" {
			byID[n.ID] = n
		}
	}
	out := make([]note, 0, len(incoming))
	for _, n := range incoming {
		if n.ID == "" {
			n.ID = newNoteID()
		}
		if prev, ok := byID[n.ID]; ok {
			// Keep the reminder's fire history: the screen must not change
			// because the user edited the configuration.
			n.Fired = prev.Fired
			n.FiredAt = prev.FiredAt
			n.LastFiredDate = prev.LastFiredDate
		}
		out = append(out, n)
	}
	return out
}

func (r noteRule) toNote() (note, error) {
	n := note{ID: r.ID, Text: r.Text}
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

// noteDue reports whether the note should fire at the given moment. It is a
// pure decision function: it does NOT mutate the note; the caller applies the
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
