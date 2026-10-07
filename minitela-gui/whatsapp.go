package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	_ "modernc.org/sqlite"

	"minitela/minitela"
)

// WhatsApp integration (receive-only) via the multi-device protocol.
//
// First run shows a QR code in the app: scan it in WhatsApp > Linked devices
// and the session is persisted in whatsapp.db, so later launches reconnect
// automatically. Incoming 1:1 text messages are shown on the mini screen's
// WhatsApp page (registers 1140/1141) and stay there until the user changes
// page — the same behavior as the Notas reminders.

const (
	waStateDisconnected = "disconnected"
	waStatePairing      = "pairing"
	waStateConnected    = "connected"
	waStateError        = "error"

	// waSenderMax/waTextMax fit the stock theme boxes (sender: 213x21 single
	// line, content: 202x143 wrapped, both font 22, no scrolling).
	waSenderMax = 16
	waTextMax   = 64
)

var (
	waMu        sync.Mutex
	waApp       *App
	waClient    *whatsmeow.Client
	waContainer *sqlstore.Container
	waDB        *sql.DB
	waState     = waStateDisconnected
	waPhone     = ""
	waStarting  = false
	waQRGen     = 0
	waLastShown = ""
)

func waInit(app *App) {
	waMu.Lock()
	waApp = app
	waMu.Unlock()
}

func waCtx() context.Context {
	waMu.Lock()
	defer waMu.Unlock()
	if waApp == nil {
		return context.Background()
	}
	return waApp.ctx
}

func waDBPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "minitela-gui.exe", "whatsapp.db"), nil
}

func waSetStatus(state, phone string) {
	waMu.Lock()
	waState = state
	waPhone = phone
	waMu.Unlock()
	runtimeEmit(waCtx(), "whatsapp-status", map[string]interface{}{
		"state": state,
		"phone": phone,
	})
}

func waEmitQR(png []byte) {
	waMu.Lock()
	waQRGen++
	gen := waQRGen
	waMu.Unlock()
	runtimeEmit(waCtx(), "whatsapp-qr", map[string]interface{}{
		"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		"gen":   gen,
	})
}

// GetWhatsAppStatus returns the connection state for the WhatsApp tab.
func (a *App) GetWhatsAppStatus() map[string]interface{} {
	waMu.Lock()
	defer waMu.Unlock()
	return map[string]interface{}{
		"state":     waState,
		"phone":     waPhone,
		"connected": waClient != nil && waClient.IsConnected(),
	}
}

// ConnectWhatsApp starts the client: it pairs with a QR code on first run and
// reconnects with the saved session afterwards. The QR codes and the final
// state arrive through the whatsapp-qr / whatsapp-status events.
func (a *App) ConnectWhatsApp() error {
	waMu.Lock()
	if waStarting {
		waMu.Unlock()
		return fmt.Errorf("conexão em andamento")
	}
	if waClient != nil && waClient.IsConnected() {
		waMu.Unlock()
		return nil
	}
	waStarting = true
	waMu.Unlock()

	go func() {
		defer func() {
			waMu.Lock()
			waStarting = false
			waMu.Unlock()
		}()
		if err := waConnect(); err != nil {
			waSetStatus(waStateError, "")
		}
	}()
	return nil
}

// waOpenContainer opens (creating when needed) the session database and
// returns the container plus the first device, creating one for first pairing.
func waOpenContainer(ctx context.Context, dbPath string) (*sqlstore.Container, *store.Device, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?cache=shared&_foreign_keys=on")
	if err != nil {
		return nil, nil, err
	}
	waMu.Lock()
	if waDB != nil {
		_ = waDB.Close()
	}
	waDB = db
	waMu.Unlock()
	container := sqlstore.NewWithDB(db, "sqlite", nil)
	if err := container.Upgrade(ctx); err != nil {
		return nil, nil, err
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, nil, err
	}
	if device == nil {
		device = container.NewDevice()
	}
	return container, device, nil
}

func waConnect() error {
	ctx := context.Background()

	dbPath, err := waDBPath()
	if err != nil {
		return err
	}
	container, device, err := waOpenContainer(ctx, dbPath)
	if err != nil {
		return err
	}

	client := whatsmeow.NewClient(device, nil)
	client.AddEventHandler(waEventHandler)

	waMu.Lock()
	waContainer = container
	waClient = client
	waMu.Unlock()

	if client.Store.ID == nil {
		waSetStatus(waStatePairing, "")
		qrChan, err := client.GetQRChannel(ctx)
		if err != nil {
			return err
		}
		if err := client.Connect(); err != nil {
			return err
		}
		for item := range qrChan {
			switch item.Event {
			case "code":
				png, err := qrcode.Encode(item.Code, qrcode.Medium, 256)
				if err == nil {
					waEmitQR(png)
				}
			case "success":
				waMarkConnected()
				waSetStatus(waStateConnected, waPhoneOf(client))
				return nil
			case "error":
				if item.Error != nil {
					return item.Error
				}
				return fmt.Errorf("pareamento falhou")
			}
		}
		return fmt.Errorf("pareamento expirado")
	}

	if err := client.Connect(); err != nil {
		return err
	}
	waMarkConnected()
	waSetStatus(waStateConnected, waPhoneOf(client))
	return nil
}

func waPhoneOf(client *whatsmeow.Client) string {
	if client == nil || client.Store.ID == nil {
		return ""
	}
	return "+" + client.Store.ID.User
}

// DisconnectWhatsApp drops the WhatsApp connection, keeping the session.
func (a *App) DisconnectWhatsApp() error {
	waMu.Lock()
	client := waClient
	waMu.Unlock()
	if client != nil {
		client.Disconnect()
	}
	waSetStatus(waStateDisconnected, waPhoneOf(client))
	return nil
}

// LogoutWhatsApp unlinks the device (the next connect needs a new QR scan).
func (a *App) LogoutWhatsApp() error {
	waMu.Lock()
	client := waClient
	waMu.Unlock()
	if client != nil && client.IsConnected() {
		if err := client.Logout(context.Background()); err != nil {
			return err
		}
	}
	if client != nil {
		client.Disconnect()
	}
	waMu.Lock()
	waClient = nil
	waContainer = nil
	if waDB != nil {
		_ = waDB.Close()
		waDB = nil
	}
	waLastShown = ""
	waMu.Unlock()
	waSetStatus(waStateDisconnected, "")
	return nil
}

// waMessageText extracts the displayable text of an incoming message.
// It returns "" for non-text content (media, stickers, etc.).
func waMessageText(msg *events.Message) string {
	if msg == nil || msg.Message == nil {
		return ""
	}
	if t := msg.Message.GetConversation(); t != "" {
		return t
	}
	return msg.Message.GetExtendedTextMessage().GetText()
}

// waShouldShow decides whether an incoming message goes to the mini screen:
// live 1:1 text messages only. No groups, no own messages, no history
// replays, and nothing older than a small grace after (re)connect.
func waShouldShow(info types.MessageInfo, text string, connectedAt time.Time) bool {
	if text == "" || info.IsFromMe || info.IsGroup {
		return false
	}
	if info.Chat.Server != types.DefaultUserServer {
		return false
	}
	if info.Timestamp.Before(connectedAt.Add(-2 * time.Minute)) {
		return false
	}
	return true
}

// waConnectedAt records when the current session came online, so history
// replays are not shown as new messages.
var (
	waConnMu      sync.Mutex
	waConnectedAt time.Time
)

func waMarkConnected() {
	waConnMu.Lock()
	waConnectedAt = time.Now()
	waConnMu.Unlock()
}

func waEventHandler(evt interface{}) {
	msg, ok := evt.(*events.Message)
	if !ok {
		return
	}
	waConnMu.Lock()
	since := waConnectedAt
	waConnMu.Unlock()
	text := waMessageText(msg)
	if !waShouldShow(msg.Info, text, since) {
		return
	}
	sender := msg.Info.PushName
	if sender == "" {
		sender = "+" + msg.Info.Sender.User
	}
	waShowMessage(sender, text)
}

// waShowMessage writes sender + text to the WhatsApp page registers (only
// when the content changed), flips to page 1 and holds it like the Notas
// reminders do (rotation pauses until the user moves on).
func waShowMessage(sender, text string) {
	sender = truncateASCII(normalizeText(sender), waSenderMax)
	text = truncateASCII(normalizeText(text), waTextMax)
	combined := sender + "\x00" + text

	waMu.Lock()
	if combined == waLastShown {
		waMu.Unlock()
		return
	}
	waMu.Unlock()

	c, err := waMinitalaClient()
	if err != nil {
		return
	}
	if err := c.SetStringTag(minitela.RegNotificationSender, sender); err != nil {
		return
	}
	if err := c.SetStringTag(minitela.RegNotificationContent, text); err != nil {
		return
	}
	if err := c.SetPage(1); err != nil {
		return
	}
	waMu.Lock()
	waLastShown = combined
	waMu.Unlock()

	pauseRotationForMessage()
	runtimeEmit(waCtx(), "whatsapp-message", map[string]interface{}{
		"sender": sender,
		"text":   text,
	})
}

// waMinitalaClient returns the serial client through the running App.
func waMinitalaClient() (*minitela.Client, error) {
	waMu.Lock()
	app := waApp
	waMu.Unlock()
	if app == nil {
		return nil, fmt.Errorf("app não iniciado")
	}
	return app.get()
}

// pauseRotationForMessage holds the rotation while a WhatsApp message is
// displayed, mirroring the Notas reminders behavior.
func pauseRotationForMessage() {
	waMu.Lock()
	app := waApp
	waMu.Unlock()
	if app != nil {
		app.pauseRotation()
	}
}
