package minitela

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
)

// NumTag is a numeric register write.
type NumTag struct {
	ID    uint16
	Value int32
}

// Client is a high-level handle to the Minitela device. All high-level
// operations are serialized through a single worker goroutine so that
// concurrent callers (the hidden monitor loop and the page selectors / keyboard
// hook) never issue overlapping serial I/O. This removes both the data races on
// the shared read buffer and the cross-caller blocking that froze the device.
type Client struct {
	port *Port

	opMu   sync.Mutex
	closed bool
	opCh   chan op
}

// op is a queued serial operation. It runs inside the single worker goroutine.
type op struct {
	run  func() error
	done chan error
}

func (c *Client) initOps() {
	c.opCh = make(chan op, 64)
	go c.opWorker()
}

func (c *Client) opWorker() {
	for o := range c.opCh {
		err := o.run()
		if o.done != nil {
			o.done <- err
		}
	}
}

// enqueue submits a closure to the serial worker and waits for its result.
// It returns an error immediately if the client is closed or if the serial
// queue is full. Crucially, opMu is NOT held while the operation is enqueued
// or while waiting for the result: holding it there would block Close and every
// other caller whenever the worker is busy with a slow serial transaction,
// which is what froze the COM port while rapidly switching pages.
func (c *Client) enqueue(run func() error) error {
	c.opMu.Lock()
	if c.closed {
		c.opMu.Unlock()
		return fmt.Errorf("minitela fechada")
	}
	done := make(chan error, 1)
	o := op{run: run, done: done}
	select {
	case c.opCh <- o:
		c.opMu.Unlock()
		return <-done
	default:
		c.opMu.Unlock()
		return fmt.Errorf("minitela ocupada (fila de comandos cheia)")
	}
}

// Connect opens the Minitela port (auto-detected on Windows) and performs a
// handshake with the firmware.
func Connect() (*Client, error) {
	p, err := OpenMinitelaPort()
	if err != nil {
		return nil, err
	}
	c := &Client{port: p}
	c.initOps()
	return c, nil
}

// ConnectPort connects to an explicit port name.
func ConnectPort(name string) (*Client, error) {
	p, err := OpenPort(name)
	if err != nil {
		return nil, err
	}
	c := &Client{port: p}
	c.initOps()
	return c, nil
}

// Close closes the underlying port and stops the serial worker.
func (c *Client) Close() error {
	if c.port == nil {
		return nil
	}
	c.opMu.Lock()
	if !c.closed {
		c.closed = true
		close(c.opCh)
	}
	c.opMu.Unlock()
	return c.port.Close()
}

// Port returns the underlying port handle.
func (c *Client) Port() *Port { return c.port }

// Handshake sends the handshake command and reads the response.
func (c *Client) Handshake() (uint32, error) {
	var res uint32
	var hErr error
	err := c.enqueue(func() error {
		cmd := NewCommand(CommandHandshake, nil, false)
		resp, err := c.port.SendAndWait(cmd, CommandHandshakeResponse, 2*time.Second)
		if err != nil {
			return err
		}
		_, _, content, _, ok := parseResponse(resp)
		if !ok || len(content) < 4 {
			return fmt.Errorf("bad handshake response")
		}
		res = binary.BigEndian.Uint32(content)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return res, hErr
}

// ReSync flushes stale bytes from the serial buffer and re-runs the handshake.
// This should be called after a burst of timeouts so the device and app fall
// back in sync without having to physically reconnect the USB cable.
func (c *Client) ReSync() error {
	var hErr error
	err := c.enqueue(func() error {
		c.port.Flush()
		cmd := NewCommand(CommandHandshake, nil, false)
		resp, err := c.port.SendAndWait(cmd, CommandHandshakeResponse, 2*time.Second)
		if err != nil {
			return err
		}
		_, _, content, _, ok := parseResponse(resp)
		if !ok || len(content) < 4 {
			return fmt.Errorf("bad handshake response")
		}
		_ = binary.BigEndian.Uint32(content)
		return nil
	})
	if err != nil {
		return err
	}
	return hErr
}

// SetBacklight sets the display backlight (0-100).
func (c *Client) SetBacklight(value int) error {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return c.sendSystemNum(RegSystemBacklight, int32(value))
}

// SetPage switches the displayed page by writing the pageId to the current
// page register (2), matching the official app's showPage().
func (c *Client) SetPage(pageID int32) error {
	return c.sendSystemNum(RegSystemPage, pageID)
}

// SetDateTime writes the current date and time to the Date and Time system
// registers, matching the official app's sendDate().
func (c *Client) SetDateTime(now time.Time) error {
	year := fmt.Sprintf("%04d", now.Year())
	mo := fmt.Sprintf("%02d", int(now.Month()))
	day := fmt.Sprintf("%02d", now.Day())
	hh := fmt.Sprintf("%02d", now.Hour())
	mm := fmt.Sprintf("%02d", now.Minute())
	ss := fmt.Sprintf("%02d", now.Second())

	var dateVal, timeVal int64
	fmt.Sscanf("0x"+year+mo+day, "0x%x", &dateVal)
	fmt.Sscanf("0x"+hh+mm+ss, "0x%x", &timeVal)

	if err := c.sendSystemNum(RegSystemDate, int32(dateVal)); err != nil {
		return err
	}
	return c.sendSystemNum(RegSystemTime, int32(timeVal))
}

// textRegister is the register the open-minitela project uses to render raw
// ASCII text into the text page (identified in the firmware config as 1090).
const textRegister uint16 = 1090

// WriteText displays raw ASCII text on the screen without changing brightness.
// This reproduces the open-minitela sequence: switch to layer 2, clear the
// text buffer, render the text.
func (c *Client) WriteText(text string) error {
	return c.WriteTextOnly(text)
}

// WriteTextOnly renders ASCII text on the screen without touching the
// current backlight level. See WriteTextWithBrightness for the sequence.
func (c *Client) WriteTextOnly(text string) error {
	if !isASCII(text) {
		return fmt.Errorf("text must be ASCII only")
	}
	if len(text) > 100 {
		text = text[:100]
	}
	if text == "" {
		text = " "
	}

	// Step 1: switch to display page 2 (reg 0x0002 = value 2).
	if err := c.SetNumTag(RegSystemPage, 2); err != nil {
		return err
	}
	// Small bus-settling delay, as done by the reference implementation.
	time.Sleep(100 * time.Millisecond)

	// Step 2: clear the text buffer.
	if err := c.SetStringTag(textRegister, " "); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)

	// Step 3: render the actual text.
	return c.SetStringTag(textRegister, text)
}

// WriteTextWithBrightness writes text and applies the brightness value if >= 0.
// It reproduces the exact byte sequence from the open-minitela project, which
// runs identically to the official app. Pass ::-1:: to leave brightness as-is.
func (c *Client) WriteTextWithBrightness(text string, brightness int) error {
	if err := c.WriteTextOnly(text); err != nil {
		return err
	}
	if brightness >= 0 {
		return c.SetBacklight(brightness)
	}
	return nil
}

// sendSetRegisterRaw sends a SET_REGISTER command with raw content (no
// length re-wrapping) and waits for the response, via the serial worker.
func (c *Client) sendSetRegisterRaw(cmdType CommandType, content []byte) error {
	return c.enqueue(func() error {
		cmd := NewCommand(cmdType, content, false)
		_, err := c.port.SendAndWait(cmd, CommandSetRegisterResponse, 2*time.Second)
		return err
	})
}

// sendSystemNum writes a single numeric system tag.
func (c *Client) sendSystemNum(regID uint16, value int32) error {
	return c.SetNumTag(regID, value)
}

// SetNumTag writes a single numeric register.
func (c *Client) SetNumTag(regID uint16, value int32) error {
	return c.SetNumTags([]NumTag{{ID: regID, Value: value}})
}

// SetNumTags writes a batch of up to 16 numeric registers.
func (c *Client) SetNumTags(tags []NumTag) error {
	const maxPerPacket = 16
	if len(tags) <= maxPerPacket {
		return c.setNumTagsChunk(tags)
	}
	for len(tags) > 0 {
		chunk := tags
		if len(chunk) > maxPerPacket {
			chunk = chunk[:maxPerPacket]
		}
		if err := c.setNumTagsChunk(chunk); err != nil {
			return err
		}
		tags = tags[len(chunk):]
	}
	return nil
}

func (c *Client) setNumTagsChunk(tags []NumTag) error {
	if len(tags) == 0 {
		return nil
	}
	content := buildNumContent(tags)
	return c.sendSetRegisterRaw(CommandSetRegister, content)
}

// SetStringTag writes a single string register (e.g. WiFi SSID, media name,
// notifications).
func (c *Client) SetStringTag(regID uint16, value string) error {
	payload := encodeGB2312(value)
	content := buildStringContent(regID, payload)
	return c.sendSetRegisterRaw(CommandSetRegister, content)
}

// encodeGB2312 converts a UTF-8 string to the byte encoding the official app
// sends to the firmware (StringConverter.convertStrToUint8Array(str,'gb2312')).
// ASCII passes through unchanged. The degree sign '°' (U+00B0) is emitted as
// its UTF-8 pair 0xC2 0xB0: testing showed a lone 0xB0 breaks the rest of the
// string on the panel (only the leading digits render), while UTF-8 renders
// correctly. Any other non-ASCII byte becomes '?'.
func encodeGB2312(s string) []byte {
	var out []byte
	b := []byte(s)
	i := 0
	for i < len(b) {
		c := b[i]
		if c <= 0x7F {
			out = append(out, c)
			i++
		} else if c == 0xC2 && i+1 < len(b) && b[i+1] == 0xB0 {
			// '°' (U+00B0) -> UTF-8 bytes 0xC2 0xB0.
			out = append(out, 0xC2, 0xB0)
			i += 2
		} else {
			out = append(out, byte('?'))
			i++
		}
	}
	return out
}

// GetNumTags reads one or more numeric registers via the serial worker.
func (c *Client) GetNumTags(regIDs []uint16) (map[uint16]int32, error) {
	res := map[uint16]int32{}
	const maxPerPacket = 16
	for len(regIDs) > 0 {
		chunk := regIDs
		if len(chunk) > maxPerPacket {
			chunk = chunk[:maxPerPacket]
		}
		content := buildNumRequestContent(chunk)
		var nums map[uint16]int32
		err := c.enqueue(func() error {
			cmd := NewCommand(CommandSetRegister, content, false)
			resp, err := c.port.SendAndWait(cmd, CommandSetRegisterResponse, 2*time.Second)
			if err != nil {
				return err
			}
			n, err := decodeNumResponse(resp)
			if err != nil {
				return err
			}
			nums = n
			return nil
		})
		if err != nil {
			return nil, err
		}
		for k, v := range nums {
			res[k] = v
		}
		regIDs = regIDs[len(chunk):]
	}
	return res, nil
}

// GetStringTag reads a string register via the serial worker.
func (c *Client) GetStringTag(regID uint16, length uint16) ([]byte, error) {
	content := buildStringRequestContent(regID, length)
	var val []byte
	err := c.enqueue(func() error {
		cmd := NewCommand(CommandSetRegister, content, false)
		resp, err := c.port.SendAndWait(cmd, CommandSetRegisterResponse, 2*time.Second)
		if err != nil {
			return err
		}
		v, err := decodeStringResponse(resp, regID)
		if err != nil {
			return err
		}
		val = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return val, nil
}

// decodeNumResponse parses a SET_REGISTER_RESPONSE frame into numeric values.
func decodeNumResponse(resp []byte) (map[uint16]int32, error) {
	_, control, content, _, ok := parseResponse(resp)
	if !ok {
		return nil, fmt.Errorf("bad response frame")
	}
	_ = control
	hdr := content[0]
	functionCode := (hdr >> 4) & 0x07
	regNum := int(hdr & 0x0F)
	if functionCode != 0 {
		return nil, fmt.Errorf("not numeric response (function code %d)", functionCode)
	}
	data := content[1:]
	res := map[uint16]int32{}
	for i := 0; i <= regNum; i++ {
		if i*6+6 > len(data) {
			break
		}
		id := binary.BigEndian.Uint16(data[i*6:])
		val := int32(binary.BigEndian.Uint32(data[i*6+2:]))
		res[id] = val
	}
	return res, nil
}

// decodeStringResponse parses a SET_REGISTER_RESPONSE frame into a string.
func decodeStringResponse(resp []byte, regID uint16) ([]byte, error) {
	_, _, content, _, ok := parseResponse(resp)
	if !ok {
		return nil, fmt.Errorf("bad response frame")
	}
	hdr := content[0]
	functionCode := (hdr >> 4) & 0x07
	if functionCode != 0b11 {
		return nil, fmt.Errorf("not string response (function code %d)", functionCode)
	}
	data := content[1:]
	if len(data) < 4 {
		return nil, fmt.Errorf("short string response")
	}
	id := binary.BigEndian.Uint16(data[0:])
	_ = id
	length := binary.BigEndian.Uint16(data[2:])
	if int(length) > len(data)-4 {
		length = uint16(len(data) - 4)
	}
	val := data[4 : 4+length]
	// The device may pad with spaces; trim trailing NUL/space padding.
	val = trimPadding(val)
	return val, nil
}

func trimPadding(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == 0x00 || b[len(b)-1] == 0x20) {
		b = b[:len(b)-1]
	}
	return b
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7F {
			return false
		}
	}
	return true
}

// AsText returns a printable representation, treating the buffer as ASCII.
func AsText(b []byte) string {
	return strings.TrimRight(string(b), "\x00 ")
}
