package minitela

import (
	"fmt"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	defaultBaud = 115200
	readTimeout = 500 * time.Millisecond
)

// Port wraps a serial port connected to the Minitela device.
type Port struct {
	port    serial.Port
	mu      sync.Mutex
	readBuf []byte
}

// OpenPort opens the serial port with the given name at 115200 baud.
func OpenPort(name string) (*Port, error) {
	mode := &serial.Mode{
		BaudRate: defaultBaud,
		DataBits: 8,
		StopBits: 1,
		Parity:   serial.NoParity,
	}
	p, err := serial.Open(name, mode)
	if err != nil {
		return nil, fmt.Errorf("open port %s: %w", name, err)
	}
	return &Port{port: p}, nil
}

// OpenMinitelaPort finds and opens the Minitela COM port automatically.
// It inspects the Windows registry for the USB\VID_0324&PID_0324 device.
func OpenMinitelaPort() (*Port, error) {
	name, err := FindMinitelaPort()
	if err != nil {
		return nil, err
	}
	return OpenPort(name)
}

// Send writes a framed command to the port.
func (p *Port) Send(cmd *Command) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.port.Write(cmd.Bytes())
	return err
}

// SendAndWait writes a command and waits for the expected response type.
// It returns the raw response frame. The whole write+read transaction holds
// p.mu so that concurrent callers (the monitor loop and the keyboard hook)
// never interleave on the shared readBuf, which previously caused data races
// that froze the serial link with the device.
func (p *Port) SendAndWait(cmd *Command, expected CommandType, timeout time.Duration) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.port.Write(cmd.Bytes()); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		frame, _, err := p.readFrameLocked()
		if err != nil {
			return nil, err
		}
		if frame == nil {
			// timed out on this read, check overall deadline
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("timeout waiting for response %#04x", expected)
			}
			continue
		}
		cmdType, _, _, _, ok := parseResponse(frame)
		if !ok {
			continue
		}
		if cmdType == expected {
			return frame, nil
		}
	}
	return nil, fmt.Errorf("timeout waiting for response %#04x", expected)
}

// readFrameLocked reads available bytes and tries to extract one complete
// frame. It MUST be called with p.mu held (i.e. only from SendAndWait).
// It returns (nil, bufferedBytesRemaining, nil) if not enough data yet.
func (p *Port) readFrameLocked() ([]byte, []byte, error) {
	p.port.SetReadTimeout(readTimeout)
	buf := make([]byte, 4096)
	n, err := p.port.Read(buf)
	if err != nil {
		return nil, p.readBuf, err
	}
	if n == 0 {
		return nil, p.readBuf, nil
	}
	p.readBuf = append(p.readBuf, buf[:n]...)

	// Try to find a complete frame: starts with AH, ends with MI
	start := indexOf(p.readBuf, startFlag)
	if start < 0 {
		// discard anything before any potential AH
		if len(p.readBuf) > 4 {
			p.readBuf = p.readBuf[len(p.readBuf)-4:]
		}
		return nil, p.readBuf, nil
	}
	if start > 0 {
		p.readBuf = p.readBuf[start:]
	}
	// Need control flag to know the length
	if len(p.readBuf) < 6 {
		return nil, p.readBuf, nil
	}
	control := int(p.readBuf[2]&0x7F)<<8 | int(p.readBuf[3])
	dataLen := control
	frameLen := 2 + 2 + 2 + (dataLen - 2) + 2 + 2
	// frameLen = START(2) + CONTROL(2) + CMDTYPE(2) + content(dataLen-2) + CRC(2) + END(2)
	if len(p.readBuf) < frameLen {
		return nil, p.readBuf, nil
	}
	frame := p.readBuf[:frameLen]
	p.readBuf = p.readBuf[frameLen:]
	return frame, p.readBuf, nil
}

func indexOf(b, sub []byte) int {
	for i := 0; i+len(sub) <= len(b); i++ {
		match := true
		for j := range sub {
			if b[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Close closes the serial port.
func (p *Port) Close() error {
	return p.port.Close()
}

// Flush discards any buffered bytes still waiting to be parsed. Calling this
// before the next transaction re-syncs the frame parser when the device has
// become desynchronized (e.g. after a reboot or a partial response).
func (p *Port) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Drain whatever the OS has buffered without blocking on a full timeout.
	for {
		buf := make([]byte, 4096)
		p.port.SetReadTimeout(25 * time.Millisecond)
		n, _ := p.port.Read(buf)
		if n <= 0 {
			break
		}
	}
	p.readBuf = nil
}
