package minitela

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// NumTag is a numeric register write.
type NumTag struct {
	ID    uint16
	Value int32
}

// Client is a high-level handle to the Minitela device.
type Client struct {
	port *Port
}

// Connect opens the Minitela port (auto-detected on Windows) and performs a
// handshake with the firmware.
func Connect() (*Client, error) {
	p, err := OpenMinitelaPort()
	if err != nil {
		return nil, err
	}
	c := &Client{port: p}
	return c, nil
}

// ConnectPort connects to an explicit port name.
func ConnectPort(name string) (*Client, error) {
	p, err := OpenPort(name)
	if err != nil {
		return nil, err
	}
	return &Client{port: p}, nil
}

// Close closes the underlying port.
func (c *Client) Close() error {
	if c.port == nil {
		return nil
	}
	return c.port.Close()
}

// Port returns the underlying port handle.
func (c *Client) Port() *Port { return c.port }

// Handshake sends the handshake command and reads the response.
func (c *Client) Handshake() (uint32, error) {
	cmd := NewCommand(CommandHandshake, nil, false)
	resp, err := c.port.SendAndWait(cmd, CommandHandshakeResponse, 2*time.Second)
	if err != nil {
		return 0, err
	}
	_, _, content, _, ok := parseResponse(resp)
	if !ok || len(content) < 4 {
		return 0, fmt.Errorf("bad handshake response")
	}
	return binary.BigEndian.Uint32(content), nil
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
// length re-wrapping) and waits for the response.
func (c *Client) sendSetRegisterRaw(cmdType CommandType, content []byte) error {
	cmd := NewCommand(cmdType, content, false)
	_, err := c.port.SendAndWait(cmd, CommandSetRegisterResponse, 2*time.Second)
	return err
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
	if !isASCII(value) {
		return fmt.Errorf("string value must be ASCII")
	}
	payload := []byte(value)
	content := buildStringContent(regID, payload)
	return c.sendSetRegisterRaw(CommandSetRegister, content)
}

// GetNumTags reads one or more numeric registers.
func (c *Client) GetNumTags(regIDs []uint16) (map[uint16]int32, error) {
	res := map[uint16]int32{}
	const maxPerPacket = 16
	for len(regIDs) > 0 {
		chunk := regIDs
		if len(chunk) > maxPerPacket {
			chunk = chunk[:maxPerPacket]
		}
		content := buildNumRequestContent(chunk)
		cmd := NewCommand(CommandSetRegister, content, false)
		resp, err := c.port.SendAndWait(cmd, CommandSetRegisterResponse, 2*time.Second)
		if err != nil {
			return nil, err
		}
		nums, err := decodeNumResponse(resp)
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

// GetStringTag reads a string register.
func (c *Client) GetStringTag(regID uint16, length uint16) ([]byte, error) {
	content := buildStringRequestContent(regID, length)
	cmd := NewCommand(CommandSetRegister, content, false)
	resp, err := c.port.SendAndWait(cmd, CommandSetRegisterResponse, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return decodeStringResponse(resp, regID)
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
