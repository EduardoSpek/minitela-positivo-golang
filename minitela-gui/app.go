package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"minitela/minitela"
)

// Firmware page identifiers (pageId written to register 2). Confirmed
// empirically: the physical key cycles WhatsApp, Notas, Monitor, Clima,
// Imagem = pageId 1-5. WhatsApp (1) is disabled/excluded from the cycle.
const (
	PageNotas    = int32(2)
	PageMonitor  = int32(3)
	PageClima    = int32(4)
	PageImagem   = int32(5)
)

// App struct
type App struct {
	ctx context.Context

	mu       sync.Mutex
	client   *minitela.Client
	connected bool

	// monitoring state
	monitorRunning bool
	monitorStop    chan struct{}
	monitorMu      sync.Mutex

	// notes state (Reminder 1/2/3) for the Notas screen
	notesMu sync.Mutex
	notes   [3]note

	// weather state for the Clima screen
	weatherMu   sync.Mutex
	weatherLast []DayForecast

	// daily preset (Agenda) tracking: last fired HH:MM per rule index
	schedMu   sync.Mutex
	schedLast map[int]string
}

// note holds a scheduled reminder for the Notas screen. Text is the message;
// At is the day/time the mini screen flips to Notas. Once At passes, Fired
// becomes true and FiredAt records the moment the notice was shown (the screen
// displays that time, not the scheduled one).
type note struct {
	Text    string
	At      time.Time
	Fired   bool
	FiredAt time.Time
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// Restore persisted reminders so rescheduling survives app restarts.
	a.notesMu.Lock()
	a.notes = loadNotesConfig()
	a.notesMu.Unlock()
	// Install the global keyboard hook so the dedicated notebook key advances
	// the mini screen page even when the app does not have focus.
	installPageHook(a)
}

// Connect establishes the connection to the Minitela device.
func (a *App) Connect(port string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.connected {
		return fmt.Errorf("já conectado")
	}

	var (
		c   *minitela.Client
		err error
	)
	if port != "" {
		c, err = minitela.ConnectPort(port)
	} else {
		c, err = minitela.Connect()
	}
	if err != nil {
		return err
	}
	a.client = c
	a.connected = true

	// Reproduce the official app's run-serial startup sequence: handshake
	// then push the current date/time so the firmware clock starts ticking
	// (without this the monitor screen clock stays frozen).
	if _, err := c.Handshake(); err != nil {
		a.connected = false
		a.client = nil
		_ = c.Close()
		return fmt.Errorf("handshake: %w", err)
	}
	if err := c.SetDateTime(time.Now()); err != nil {
		// non-fatal: keep connection
		fmt.Println("set datetime:", err)
	}
	// On app start the mini screen shows the Monitor page first. Non-fatal: the
	// user can still switch pages afterwards.
	if err := c.SetPage(PageMonitor); err != nil {
		fmt.Println("set page monitor:", err)
	}
	return nil
}

// IsConnected returns whether the device is connected.
func (a *App) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connected
}

// Disconnect closes the connection to the device.
func (a *App) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.connected {
		return nil
	}
	err := a.client.Close()
	a.client = nil
	a.connected = false
	return err
}

func (a *App) get() (*minitela.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.connected || a.client == nil {
		return nil, fmt.Errorf("minitela não conectada")
	}
	return a.client, nil
}

// SetBacklight adjusts the display brightness (0-100).
func (a *App) SetBacklight(value int) error {
	c, err := a.get()
	if err != nil {
		return err
	}
	return c.SetBacklight(value)
}

// WriteText displays text on the screen (ASCII only).
func (a *App) WriteText(text string, brightness int) error {
	c, err := a.get()
	if err != nil {
		return err
	}
	text = normalizeText(text)
	if !isASCIIStr(text) {
		return fmt.Errorf("o texto contém caracteres que a mini tela não suporta")
	}
	if len(text) > 100 {
		text = text[:100]
	}
	return c.WriteTextWithBrightness(text, brightness)
}

// SetDateTime sends the current date/time.
func (a *App) SetDateTime() error {
	c, err := a.get()
	if err != nil {
		return err
	}
	return c.SetDateTime(time.Now())
}

// SetSystemDateTime sends an explicit date/time (expects RFC3339).
func (a *App) SetSystemDateTime(rfc3339 string) error {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return err
	}
	c, err := a.get()
	if err != nil {
		return err
	}
	return c.SetDateTime(t)
}

// SetPage changes the displayed page.
func (a *App) SetPage(page int) error {
	c, err := a.get()
	if err != nil {
		return err
	}
	return c.SetPage(int32(page))
}

// nextPage advances the mini screen to the next page, skipping the disabled
// WhatsApp page: Notas(2)->Monitor(3)->Clima(4)->Imagem(5)->Notas(2).
// Called by the global keyboard hook when the dedicated notebook key is pressed.
func (a *App) nextPage() {
	c, err := a.get()
	if err != nil {
		return
	}
	cur, err := a.currentPage()
	if err != nil {
		cur = 0
	}
	if cur < PageNotas || cur > PageImagem {
		cur = PageNotas - 1
	}
	next := cur + 1
	if next > PageImagem {
		next = PageNotas
	}
	_ = c.SetPage(next)
}

// GoToPage switches the mini screen to a specific page (2-5). Page 1
// (WhatsApp) is disabled.
func (a *App) GoToPage(page int) error {
	if page < int(PageNotas) || page > int(PageImagem) {
		return fmt.Errorf("página inválida: %d", page)
	}
	return a.SetPage(page)
}

// GoToImageSlot shows one of the three theme image slots on the mini screen.
// The theme's Imagem pages are Gif1/2/3 at data.json pageList indices 4/5/6,
// and the page register follows index = value-1 (5->Gif1), so slot N writes
// 4+N (slot 1 = 5, same as PageImagem; slot 2 = 6; slot 3 = 7).
func (a *App) GoToImageSlot(slot int) error {
	if slot < 1 || slot > 3 {
		return fmt.Errorf("slot de imagem inválido: %d (use 1..3)", slot)
	}
	return a.SetPage(4 + slot)
}

// parseNoteDue parses a datetime-local value ("2006-01-02T15:04") into a
// reminder schedule. Empty/invalid values return the zero time (no schedule).
func parseNoteDue(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// noteFileJSON is the on-disk shape of the three reminders.
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

// SetNotes stores the three reminders (text + schedule) for the Notas screen.
// t1..t3 are datetime-local values ("2006-01-02T15:04"); an empty value keeps
// the reminder text but disables its automatic flip to the Notas page.
func (a *App) SetNotes(n1, t1, n2, t2, n3, t3 string) error {
	a.notesMu.Lock()
	a.notes[0] = note{Text: n1, At: parseNoteDue(t1)}
	a.notes[1] = note{Text: n2, At: parseNoteDue(t2)}
	a.notes[2] = note{Text: n3, At: parseNoteDue(t3)}
	_ = saveNotesConfig(a.notes)
	a.notesMu.Unlock()
	// No immediate serial write here: a burst of SET_REGISTER frames right when
	// the user clicks "Salvar" is what made the firmware stop responding. The
	// not-yet-fired reminders would render "Sem notas" anyway; the registers are
	// written when a notice fires (fireDueNote) or while the monitor loop is on
	// the Notas page.
	return nil
}

// GetNotes returns the three reminders so the UI can prefill the saved text and
// scheduled datetime ("2006-01-02T15:04") after an app restart.
func (a *App) GetNotes() [][2]string {
	a.notesMu.Lock()
	defer a.notesMu.Unlock()
	out := make([][2]string, 0, 3)
	for _, n := range a.notes {
		due := ""
		if !n.At.IsZero() {
			due = n.At.Format("2006-01-02T15:04")
		}
		out = append(out, [2]string{n.Text, due})
	}
	return out
}

// GetSystemStats returns the last gathered CPU/battery/WiFi info.
func (a *App) GetSystemStats() map[string]interface{} {
	g := gatherSystemInfo()
	return map[string]interface{}{
		"cpu":      g.CPU,
		"battery":  g.Battery,
		"wifiSSID": g.WifiSSID,
		"wifiSig":  g.WifiSignal,
		"btName":   g.BTName,
		"btConn":   g.BTConnected,
	}
}

// StartMonitor begins the periodic push of system stats to the device and UI.
func (a *App) StartMonitor(intervalSeconds int) error {
	a.monitorMu.Lock()
	if a.monitorRunning {
		a.monitorMu.Unlock()
		return fmt.Errorf("monitor já em execução")
	}
	if intervalSeconds <= 0 {
		intervalSeconds = 5
	}
	a.monitorRunning = true
	a.monitorStop = make(chan struct{})
	stop := a.monitorStop
	a.monitorMu.Unlock()

	// Scheduled-reminder worker: checks once per second whether a reminder's
	// date/time has arrived and, if so, flips the mini screen to Notas. It
	// shares the serial op queue with the monitor loop, so it never
	// interleaves on the wire.
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				c, err := a.get()
				if err != nil {
					continue
				}
				if _, err := fireDueNote(c, a); err != nil {
					runtimeEmit(a.ctx, "monitor-error", "notas: "+err.Error())
				}
				// Daily presets (Agenda): switch page + brightness at HH:MM.
				if err := a.fireDueSchedule(c); err != nil {
					runtimeEmit(a.ctx, "monitor-error", "agenda: "+err.Error())
				}
			}
		}
	}()

	go func() {
		// feedOnce pushes stats/notes/weather to the device. It returns false if
		// the serial link failed (timeouts), so the loop can back off and resync.
		feedOnce := func() bool {
			g := gatherSystemInfo()
			runtimeEmit(a.ctx, "stats", g.toMap())
			c, err := a.get()
			if err != nil {
				return false
			}
			now := time.Now()
			// The monitor page's top-bar clock is bound to string register 2006
			// (e.g. "28/10 14:00"), updated on every screen.
			dh := fmt.Sprintf("%02d/%02d %02d:%02d", now.Day(), int(now.Month()), now.Hour(), now.Minute())
			// Keep the firmware clock ticking on every screen (RTC registers 4/5).
			if terr := c.SetDateTime(now); terr != nil {
				runtimeEmit(a.ctx, "monitor-error", "relógio: "+terr.Error())
				return false
			}
			// Detect the active page. If detection fails (e.g. right after the
			// device reboots from an OTA) fall back to Monitor so the top bar
			// still updates; the per-page fields below resync once the page is
			// read reliably again.
			pg, _ := a.currentPage()
			if pg < 1 || pg > 5 {
				pg = PageMonitor
			}
			if last := lastPage.Load(); pg != last {
				lastPage.Store(pg)
				runtimeEmit(a.ctx, "page", pg)
			}
			// Monitor data + top-bar clock are unconditional (cheap, needed on
			// every screen). Clima and Notas are only serialized for their own
			// pages, keeping the serial bus load low so the firmware responds.
			if err := pushSystemTags(c, g); err != nil {
				runtimeEmit(a.ctx, "monitor-error", err.Error())
				return false
			}
			switch pg {
			case PageNotas:
				if err := pushNotesTags(c, a); err != nil {
					runtimeEmit(a.ctx, "monitor-error", "notas: "+err.Error())
					return false
				}
			case PageClima:
				if err := pushWeatherTags(c, a); err != nil {
					runtimeEmit(a.ctx, "monitor-error", "clima: "+err.Error())
					return false
				}
			}
			// The monitor page's top-bar clock is bound to string register 2006.
			if serr := c.SetStringTag(minitela.RegDateHour, dh); serr != nil {
				runtimeEmit(a.ctx, "monitor-error", "data/hora: "+serr.Error())
				return false
			}
			return true
		}

		// Start right away, then refresh on a ticker that backs off while the
		// device is unresponsive (so we stop hammering it and let it recover),
		// and triggers a serial resync after a short burst of failures.
		const (
			minInt = 10 * time.Second
			maxInt = 30 * time.Second
		)
		sleep := minInt
		consecFail := 0
		for {
			select {
			case <-stop:
				return
			case <-time.After(sleep):
			}
			select {
			case <-stop:
				return
			default:
			}
			ok := feedOnce()
			if ok {
				consecFail = 0
				sleep = minInt
				continue
			}
			consecFail++
			if consecFail%3 == 0 {
				// A few failures in a row: drop stale bytes and re-handshake so
				// the app and device fall back in sync without a cable pull.
				if c, cerr := a.get(); cerr == nil {
					if rerr := c.ReSync(); rerr == nil {
						runtimeEmit(a.ctx, "monitor-error", "serial re-sincronizado")
					}
				}
			}
			sleep = maxInt
		}
	}()
	return nil
}

// StopMonitor halts the monitoring loop.
func (a *App) StopMonitor() {
	a.monitorMu.Lock()
	defer a.monitorMu.Unlock()
	if a.monitorRunning {
		close(a.monitorStop)
		a.monitorRunning = false
	}
}

// lastPage tracks the last known active page to avoid redundant UI events.
var lastPage atomic.Int32

// currentPage reads the page index the firmware is currently showing.
func (a *App) currentPage() (int32, error) {
	c, err := a.get()
	if err != nil {
		return -1, err
	}
	vals, err := c.GetNumTags([]uint16{minitela.RegSystemPage})
	if err != nil {
		return -1, err
	}
	if v, ok := vals[minitela.RegSystemPage]; ok {
		return v, nil
	}
	return -1, fmt.Errorf("página não lida")
}

// pushSystemTags feeds the firmware's native monitor registers with the
// gathered CPU / battery / WiFi data (1080-1084, 1087, 1150).
func pushSystemTags(c *minitela.Client, g systemInfo) error {
	cpu := parseIntSafe(g.CPU)
	bat := parseIntSafe(g.Battery)
	sig := parseIntSafe(g.WifiSignal)
	quality := wifiQuality(sig)

	nums := []minitela.NumTag{
		{ID: minitela.RegCPUUsage, Value: int32(cpu)},
		{ID: minitela.RegGPUUsage, Value: 0},
		{ID: minitela.RegWifiQuality, Value: int32(quality)},
	}
	if g.Battery != "" && g.Battery != "-" {
		// Battery_Type (1150) drives the icon. Use the official app's 0-5 scale.
		nums = append(nums, minitela.NumTag{ID: minitela.RegBatteryType, Value: int32(batteryType(bat))})
	}
	if g.WifiSSID != "" && g.WifiSSID != "-" {
		nums = append(nums, minitela.NumTag{ID: minitela.RegWifiStatus, Value: 1})
	} else {
		nums = append(nums, minitela.NumTag{ID: minitela.RegWifiStatus, Value: 0})
	}
	btSet := g.BTConnected && g.BTName != ""
	if btSet {
		nums = append(nums, minitela.NumTag{ID: minitela.RegBTStatus, Value: 1})
	} else {
		nums = append(nums, minitela.NumTag{ID: minitela.RegBTStatus, Value: 0})
	}
	if err := c.SetNumTags(nums); err != nil {
		return err
	}
	// Battery_Percent (1082) is rendered by the theme's MyTextInput widget; in
	// the stock theme it must be written as a STRING (with the "%" suffix) for
	// the number to show, even though data.json marks it numeric.
	if g.Battery != "" && g.Battery != "-" {
		if err := c.SetStringTag(minitela.RegBatteryPercent, truncateASCII(g.Battery+"%", 8)); err != nil {
			return err
		}
	}
	if g.WifiSSID != "" && g.WifiSSID != "-" {
		if err := c.SetStringTag(minitela.RegWifiSSID, g.WifiSSID); err != nil {
			return err
		}
	} else {
		// No WiFi: show "Desconectado" instead of the stale SSID, like the BT.
		if err := c.SetStringTag(minitela.RegWifiSSID, "Desconectado"); err != nil {
			return err
		}
	}
	if btSet {
		// The BT name widget is ~90px wide; shorter strings fit without clipping.
		return c.SetStringTag(minitela.RegBTName, truncateASCII(g.BTName, 10))
	}
	// BT disconnected: write the theme's placeholder text so the screen no
	// longer keeps the stale device name.
	return c.SetStringTag(minitela.RegBTName, "Desconectado")
}

// pushNotesTags writes the three reminders to the Notas screen registers
// (1090-1095). Only fired reminders show their text (with the day/time the
// notice was shown); everything else shows the theme placeholder "Sem notas".
// The scheduled day/time is never displayed: it only triggers the page flip.
func pushNotesTags(c *minitela.Client, a *App) error {
	a.notesMu.Lock()
	notes := a.notes
	a.notesMu.Unlock()

	regText := []uint16{minitela.RegReminder1Text, minitela.RegReminder2Text, minitela.RegReminder3Text}
	regTime := []uint16{minitela.RegReminder1Time, minitela.RegReminder2Time, minitela.RegReminder3Time}
	for i := 0; i < 3; i++ {
		text := ""
		var timeLabel string
		if notes[i].Fired && notes[i].Text != "" {
			// Show the reminder with the actual day/time when it fired.
			text = normalizeText(notes[i].Text)
			if !notes[i].FiredAt.IsZero() {
				timeLabel = notes[i].FiredAt.Format("02/01 15:04")
			}
		}
		if text == "" {
			text = "Sem notas"
		}
		if err := c.SetStringTag(regText[i], truncateASCII(text, 96)); err != nil {
			return err
		}
		// Small pause between register writes: the serial link is fragile and
		// several SET_REGISTER frames in a row make the firmware drop into its
		// no-response state. Let each write settle before the next one.
		time.Sleep(150 * time.Millisecond)
		if err := c.SetStringTag(regTime[i], truncateASCII(timeLabel, 32)); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}

// fireDueNote flips the screen to the Notas page when a scheduled reminder's
// day/time arrives (only once). It returns true when a notice was fired so the
// monitor loop can stop treating the flip as an error.
func fireDueNote(c *minitela.Client, a *App) (bool, error) {
	now := time.Now()
	var fired int = -1
	a.notesMu.Lock()
	for i := 0; i < 3; i++ {
		n := a.notes[i]
		if !n.Fired && !n.At.IsZero() && !now.Before(n.At) && n.Text != "" {
			a.notes[i].Fired = true
			a.notes[i].FiredAt = now
			if fired < 0 {
				fired = i
			}
		}
	}
	a.notesMu.Unlock()
	if fired < 0 {
		return false, nil
	}
	// Persist the fired state so the notice survives an app restart.
	a.notesMu.Lock()
	_ = saveNotesConfig(a.notes)
	a.notesMu.Unlock()
	if err := c.SetPage(PageNotas); err != nil {
		return true, err
	}
	return true, pushNotesTags(c, a)
}

// truncateASCII trims s to at most n bytes/characters without splitting UTF-8.
func truncateASCII(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// pushWeatherTags writes the Clima screen registers for all five forecast days
// (Weather_N_Type / Temp / Temp_Min / Temp_Max / Temp_Desc, 1110-1134) plus the
// page's custom city, currentTemp and forecastTemp registers (2027, 2030-2032).
// When no forecast has been fetched yet it tries to (re)load it once.
func pushWeatherTags(c *minitela.Client, a *App) error {
	days, cfg, err := a.ensureWeather()
	if err != nil || len(days) == 0 {
		// No internet or no city configured: render a friendly offline state.
		return nil
	}
	// Condition glyph + numeric temperature registers. Register layout is
	// contiguous: day N starts at WeatherNType + ((N-1) * 5).
	typeRegs := []uint16{
		minitela.Weather1Type, minitela.Weather2Type, minitela.Weather3Type,
		minitela.Weather4Type, minitela.Weather5Type,
	}
	nums := make([]minitela.NumTag, 0, len(days)*3)
	var desc []struct {
		id  uint16
		val string
	}
	for i, d := range days {
		if i >= len(typeRegs) {
			break
		}
		nums = append(nums,
			minitela.NumTag{ID: typeRegs[i], Value: int32(wmoToIcon(d.WMO, d.IsDay))},
			minitela.NumTag{ID: typeRegs[i] + 1, Value: int32(d.Temp)},
			minitela.NumTag{ID: typeRegs[i] + 3, Value: int32(d.TempMax)},
			minitela.NumTag{ID: typeRegs[i] + 2, Value: int32(d.TempMin)},
		)
		// Weather_N_Temp_Desc registers hold the date label "DD/MM".
		desc = append(desc, struct {
			id  uint16
			val string
		}{typeRegs[i] + 4, dateLabel(d.Date)})
	}
	if err := c.SetNumTags(nums); err != nil {
		return err
	}
	display := cfg.PlaceName
	if display == "" {
		display = cfg.City
	}
	if err := c.SetStringTag(minitela.RegCity, truncateASCII(display, 32)); err != nil {
		return err
	}
	// currentTemp shows today's range as "min°/max°".
	if len(days) > 0 {
		d0 := days[0]
		cur := fmt.Sprintf("%d°/%d°", d0.TempMin, d0.TempMax)
		if err := c.SetStringTag(minitela.RegCurrentTemp, cur); err != nil {
			return err
		}
		// forecastTemp1/2 show the following two days as "min°/max°", matching
		// the theme's placeholder text ("25°/25°").
		for i := 1; i <= 2 && i < len(days); i++ {
			id := minitela.RegForecastTemp1
			if i == 2 {
				id = minitela.RegForecastTemp2
			}
			lab := fmt.Sprintf("%d°/%d°", days[i].TempMin, days[i].TempMax)
			if err := c.SetStringTag(id, lab); err != nil {
				return err
			}
		}
	}
	for _, s := range desc {
		if err := c.SetStringTag(s.id, s.val); err != nil {
			return err
		}
	}
	return nil
}

// ensureWeather returns the most recent forecast plus the saved config,
// triggering a refresh when the cache is empty.
func (a *App) ensureWeather() ([]DayForecast, weatherConfig, error) {
	cfg := loadWeatherConfig()
	a.weatherMu.Lock()
	days := a.weatherLast
	a.weatherMu.Unlock()
	if len(days) > 0 {
		return days, cfg, nil
	}
	if cfg.Lat == 0 && cfg.Lon == 0 {
		return nil, cfg, nil
	}
	a.refreshWeather(cfg)
	a.weatherMu.Lock()
	days = a.weatherLast
	a.weatherMu.Unlock()
	return days, cfg, nil
}

// dateLabel renders an ISO date as the theme's day/month label shown under each
// forecast, e.g. "04/09". Falls back to the raw string if it cannot be parsed.
func dateLabel(iso string) string {
	if len(iso) >= 10 {
		return iso[8:10] + "/" + iso[5:7]
	}
	return iso
}

// parseIntSafe converts a numeric string to int, returning 0 on failure.
func parseIntSafe(s string) int {
	if s == "" || s == "-" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// wifiQuality maps a signal percentage to the firmware's 0-3 scale,
// matching the official app: <=30->0, <=50->1, <70->2, else->3.
func wifiQuality(signal int) int {
	switch {
	case signal <= 30:
		return 0
	case signal <= 50:
		return 1
	case signal < 70:
		return 2
	default:
		return 3
	}
}

// batteryType buckets a percentage into the theme's BatteryStatus slide (1150).
// The theme's MySlide only has 4 slices (BAT_0..BAT_3, indices 0-3), so even a
// fully charged battery must map to 3. Writing 5 (the old 0-5 scale copied from
// tagUtils) pointed past the last slice and made the firmware render nothing,
// which is why the battery icon disappeared at high percentages.
func batteryType(pct int) int {
	switch {
	case pct < 25:
		return 0
	case pct < 50:
		return 1
	case pct < 75:
		return 2
	default:
		return 3
	}
}

// AutoStartEnabled reports whether the app launches with Windows.
func (a *App) AutoStartEnabled() bool {
	return IsAutoStartEnabled()
}

// SetAutoStartEnabled toggles registration to start with Windows.
func (a *App) SetAutoStartEnabled(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if enabled {
		// register to start minimized to the tray on login
		return SetAutoStart(exe, "-minimized")
	}
	return ClearAutoStart()
}

// CreateShortcut creates a desktop shortcut on the OneDrive desktop.
func (a *App) CreateShortcut() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	icon := filepath.Join(filepath.Dir(exe), "appicon.png")
	return CreateDesktopShortcut(exe, "Minitela Go", icon)
}

func isASCIIStr(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7F {
			return false
		}
	}
	return true
}

// normalizeText transliterates accented Latin characters to plain ASCII
// (e.g. "á" -> "a", "ç" -> "c") using Unicode NFD decomposition.
func normalizeText(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// combining mark (diacritic): drop it
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r <= 0x7F:
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// systemInfo is the collected host status.
type systemInfo struct {
	CPU         string
	Battery     string
	WifiSSID    string
	WifiSignal  string
	BTName      string
	BTConnected bool
}

func (s *systemInfo) textLine() string {
	var b strings.Builder
	b.WriteString("CPU:")
	b.WriteString(s.CPU)
	b.WriteString("% BAT:")
	b.WriteString(s.Battery)
	b.WriteString("% WIFI:")
	b.WriteString(s.WifiSSID)
	if s.WifiSignal != "" {
		b.WriteString("(")
		b.WriteString(s.WifiSignal)
		b.WriteString(")")
	}
	return b.String()
}

func (s *systemInfo) toMap() map[string]interface{} {
	return map[string]interface{}{
		"cpu":      s.CPU,
		"battery":  s.Battery,
		"wifiSSID": s.WifiSSID,
		"wifiSig":  s.WifiSignal,
		"btName":   s.BTName,
		"btConn":   s.BTConnected,
	}
}

// waitReconnectAfterReboot re-establishes the serial link after a theme/GIF
// flash reboots the device into the WhatsApp page and drops the connection
// (USB re-enumeration). It mirrors the manual Reconectar flow: drop the stale
// client, then retry Connect (port auto-detect + handshake) until it works or
// the timeout expires. The monitor loop keeps running meanwhile (its ticks
// just back off while disconnected) and resumes on its own once back.
func (a *App) waitReconnectAfterReboot(timeout time.Duration) error {
	_ = a.Disconnect()
	deadline := time.Now().Add(timeout)
	for {
		if err := a.Connect(""); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("minitela não voltou após o reboot (use Reconectar)")
		}
		time.Sleep(3 * time.Second)
	}
}

// UploadGifFile flashes a pre-built .acf (from a user-selected image) onto the
// Imagem page (texture_gif). progress, when non-nil, receives a 0-100 integer.
func (a *App) UploadGifFile(fileBytes []byte) error {
	c, err := a.get()
	if err != nil {
		return err
	}
	if len(fileBytes) == 0 {
		return fmt.Errorf("arquivo vazio")
	}
	if err := c.UploadFile(fileBytes, minitela.FileTypeTextureGif, nil); err != nil {
		return err
	}
	// Show the Imagem page. If the flash rebooted the device (link down),
	// reconnect first like the theme upload does.
	if err := c.SetPage(int32(PageImagem)); err != nil {
		if rerr := a.waitReconnectAfterReboot(90 * time.Second); rerr != nil {
			return fmt.Errorf("imagem enviada. %w", rerr)
		}
		c2, err := a.get()
		if err != nil {
			return fmt.Errorf("imagem enviada, mas sem conexão para exibir: %w", err)
		}
		if err := c2.SetPage(int32(PageImagem)); err != nil {
			return fmt.Errorf("imagem enviada, mas falha ao exibir: %w", err)
		}
	}
	return nil
}

// UploadGifFromPath reads an .acf file from disk and flashes it to the Imagem
// page. Used by the frontend after the user picks a compiled image.
func (a *App) UploadGifFromPath(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ler %s: %w", path, err)
	}
	return a.UploadGifFile(data)
}

// RestoreThemeFromPath flashes a full theme blob to the master texture address
// (FileTypeTexture). Used to validate the OTA pipeline / recover the stock
// theme, which is low-risk because it rewrites the very theme already installed.
func (a *App) RestoreThemeFromPath(path string) error {
	c, err := a.get()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ler %s: %w", path, err)
	}
	if len(data) == 0 {
		return fmt.Errorf("arquivo vazio")
	}
	return c.UploadFile(data, minitela.FileTypeTexture, nil)
}

// RestoreTheme locates the installed stock theme blob (Texture.acf inside the
// Positivo MiniTela WindowsApp) and re-flashes it to FileTypeTexture. This
// exercises the full OTA download pipeline with zero semantic risk: it writes
// the very same theme the device already runs.
func (a *App) RestoreTheme() error {
	path, err := findStockThemeAcf()
	if err != nil {
		return err
	}
	if err := a.RestoreThemeFromPath(path); err != nil {
		return err
	}
	// The device is back on the stock theme: drop the customized work base so
	// the next image upload starts from the factory zip again instead of
	// resurrecting old custom GIFs. Best effort: if the install source is
	// gone the base will be recopied on demand; if it can't be, keep the
	// current base rather than failing.
	_ = os.Remove(filepath.Join(workArea(), "Zip", "file.zip"))
	// The flash reboots the device (link down): reconnect; Connect itself
	// lands back on the Monitor page.
	if err := a.waitReconnectAfterReboot(90 * time.Second); err != nil {
		return fmt.Errorf("tema restaurado. %w", err)
	}
	return nil
}

// UploadImageToTheme converts the provided image bytes to a 192x192 GIF,
// embeds it into the theme's GIF for the chosen Imagem page, regenerates the
// stock theme with the official generator exe, and flashes the resulting
// Texture.acf to the master texture address (FileTypeTexture). imagePage is
// 1..3 (the Imagem page slots on the device).
func (a *App) UploadImageToTheme(fileBytes []byte, imagePage int) error {
	gif := gifByPage(imagePage)
	if gif == nil {
		return fmt.Errorf("página de imagem inválida: %d (use 1..3)", imagePage)
	}
	c, err := a.get()
	if err != nil {
		return err
	}
	if len(fileBytes) == 0 {
		return fmt.Errorf("arquivo vazio")
	}

	work, err := prepareWorkArea()
	if err != nil {
		return err
	}
	python, err := findPython()
	if err != nil {
		return err
	}
	gifPath := filepath.Join(work, "imagem_tmp.gif")
	if err := convertImageToGif(fileBytes, gifPath, python); err != nil {
		return err
	}
	defer os.Remove(gifPath)

	zipPath, err := embedGifInZip(work, python, gif.Name, gifPath)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)

	acf, err := generateThemeAcf(work, zipPath)
	if err != nil {
		return err
	}
	if err := c.UploadFile(acf, minitela.FileTypeTexture, nil); err != nil {
		return fmt.Errorf("upload do tema: %w", err)
	}
	// From now on the generated zip (with this slot embedded) becomes the
	// base for the next upload, so the other slots keep their images instead
	// of reverting to the factory theme. Persist before the reboot wait so a
	// killed app never loses the accumulation.
	if err := persistThemeBase(work, zipPath); err != nil {
		return fmt.Errorf("tema enviado, mas falha ao atualizar a base local: %w", err)
	}
	// The flash reboots the device into the WhatsApp page and drops the
	// serial link: reconnect, then show the slot that was just uploaded.
	if err := a.waitReconnectAfterReboot(90 * time.Second); err != nil {
		return fmt.Errorf("tema enviado. %w", err)
	}
	c2, err := a.get()
	if err != nil {
		return fmt.Errorf("tema enviado, mas sem conexão para exibir a imagem: %w", err)
	}
	if err := c2.SetPage(int32(4 + imagePage)); err != nil {
		return fmt.Errorf("tema enviado, mas falha ao exibir a Imagem %d: %w", imagePage, err)
	}
	return nil
}

// gifByPage returns the theme GIF descriptor for an Imagem page (1..3).
func gifByPage(page int) *GifFile {
	for i := range themeGifs {
		if themeGifs[i].PageNum == page {
			return &themeGifs[i]
		}
	}
	return nil
}

// findPython locates a usable python interpreter with Pillow on the system.
func findPython() (string, error) {
	candidates := []string{}
	for _, name := range []string{"python", "py"} {
		if p, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, p)
		}
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		candidates = append(candidates,
			filepath.Join(base, "Programs", "Python", "Python311", "python.exe"),
			filepath.Join(base, "Programs", "Python", "Python310", "python.exe"),
			filepath.Join(base, "Programs", "Python", "Python39", "python.exe"),
		)
	}
	candidates = append(candidates, `C:\Python311\python.exe`, `C:\Python310\python.exe`)
	seen := map[string]bool{}
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, err := executeCheck(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Python com Pillow não encontrado (necessário para converter imagens)")
}

// executeCheck confirms a python binary actually runs.
func executeCheck(python string) (string, error) {
	cmd := exec.Command(python, "-c", "import PIL")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// findStockThemeAcf returns the path to the stock theme blob inside the
// Positivo MiniTela install folder (checked against the known candidates).
// It uses ideUtilsBase so any installed Store version is found, not just the
// version that was current when the path was first hardcoded.
func findStockThemeAcf() (string, error) {
	base := ideUtilsBase()
	if base == "" {
		return "", fmt.Errorf("não foi possível localizar o .acf de tema na instalação")
	}
	candidates := []string{
		base + "\\ACF\\ConfigData&Texture.acf",
		base + "\\ACF\\Texture.acf",
		base + "\\ACF\\acfV1.0.15.acf",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("não foi possível localizar o .acf de tema na instalação")
}
