package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"minitela/minitela"
)

// Firmware page identifiers (pageId written to register 2). Confirmed
// empirically: the physical key cycles WhatsApp, Notas, Monitor, Clima,
// Imagem = pageId 1-5.
const (
	PageWhatsApp = int32(1)
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
}

// note holds the text+time for a single reminder on the Notas screen.
type note struct {
	Text string
	Time string
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
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

// nextPage advances the mini screen to the next page (1->2->3->4->5->1).
// Called by the global keyboard hook when the dedicated notebook key is pressed.
func (a *App) nextPage() {
	c, err := a.get()
	if err != nil {
		return
	}
	cur, err := a.currentPage()
	if err != nil || cur < 1 || cur > 5 {
		cur = 0
	}
	next := cur + 1
	if next > PageImagem {
		next = PageWhatsApp
	}
	_ = c.SetPage(next)
}

// GoToPage switches the mini screen to a specific page (1-5).
func (a *App) GoToPage(page int) error {
	if page < int(PageWhatsApp) || page > int(PageImagem) {
		return fmt.Errorf("página inválida: %d", page)
	}
	return a.SetPage(page)
}

// SetNotes stores the three reminders (text + time) for the Notas screen.
func (a *App) SetNotes(n1, t1, n2, t2, n3, t3 string) error {
	a.notesMu.Lock()
	defer a.notesMu.Unlock()
	a.notes[0] = note{Text: n1, Time: t1}
	a.notes[1] = note{Text: n2, Time: t2}
	a.notes[2] = note{Text: n3, Time: t3}
	return nil
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

	go func() {
		// Feed once right away, then keep refreshing on the ticker.
		feedOnce := func() {
			g := gatherSystemInfo()
			runtimeEmit(a.ctx, "stats", g.toMap())
			c, err := a.get()
			if err != nil {
				return
			}
			now := time.Now()
			// Keep the firmware clock ticking on every screen (RTC registers 4/5).
			if terr := c.SetDateTime(now); terr != nil {
				runtimeEmit(a.ctx, "monitor-error", "relógio: "+terr.Error())
			}
			// Detect the active page and only send the data that screen shows.
			pg, _ := a.currentPage()
			if pg < 1 || pg > 5 {
				pg = PageMonitor
			}
			if last := lastPage.Load(); pg != last {
				lastPage.Store(pg)
				runtimeEmit(a.ctx, "page", pg)
			}
			switch pg {
			case PageMonitor:
				// The monitor page's top-bar clock is bound to the custom
				// string register 2006 (dateHour), not the system 4/5 clock.
				dh := fmt.Sprintf("%02d/%02d %02d:%02d", now.Day(), int(now.Month()), now.Hour(), now.Minute())
				if serr := c.SetStringTag(minitela.RegDateHour, dh); serr != nil {
					runtimeEmit(a.ctx, "monitor-error", "data/hora: "+serr.Error())
				}
				if err := pushSystemTags(c, g); err != nil {
					runtimeEmit(a.ctx, "monitor-error", err.Error())
				}
			case PageNotas:
				if err := pushNotesTags(c, a); err != nil {
					runtimeEmit(a.ctx, "monitor-error", "notas: "+err.Error())
				}
			}
		}
		feedOnce()
		ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				feedOnce()
			}
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
		{ID: minitela.RegBatteryType, Value: int32(batteryType(bat))},
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
	// Battery_Percent (1082) is a STRING register (valueType:1) in this theme,
	// so it must be written via SetStringTag, not SetNumTags. Include the "%"
	// symbol because the firmware does not add it automatically.
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

// pushNotesTags writes the three reminders (text + time) to the Notas screen
// registers (1090-1095). Empty entries write the theme placeholder "Sem notas".
func pushNotesTags(c *minitela.Client, a *App) error {
	a.notesMu.Lock()
	notes := a.notes
	a.notesMu.Unlock()

	regText := []uint16{minitela.RegReminder1Text, minitela.RegReminder2Text, minitela.RegReminder3Text}
	regTime := []uint16{minitela.RegReminder1Time, minitela.RegReminder2Time, minitela.RegReminder3Time}
	for i := 0; i < 3; i++ {
		text := notes[i].Text
		if text == "" {
			text = "Sem notas"
		} else {
			text = normalizeText(text)
		}
		if err := c.SetStringTag(regText[i], truncateASCII(text, 96)); err != nil {
			return err
		}
		if err := c.SetStringTag(regTime[i], truncateASCII(notes[i].Time, 32)); err != nil {
			return err
		}
	}
	return nil
}

// truncateASCII trims s to at most n bytes/characters without splitting UTF-8.
func truncateASCII(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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

// batteryType buckets a percentage into the firmware's scale (0-5) matching
// the official app: <20->0, <40->1, <60->2, <80->3, <100->4, 100->5.
func batteryType(pct int) int {
	switch {
	case pct < 20:
		return 0
	case pct < 40:
		return 1
	case pct < 60:
		return 2
	case pct < 80:
		return 3
	case pct < 100:
		return 4
	default:
		return 5
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
