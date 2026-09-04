package minitela

import (
	"encoding/binary"
)

// CommandType holds the protocol command identifiers defined by the
// Minitela HMI firmware.
type CommandType uint16

const (
	CommandHandshake            CommandType = 0x0080
	CommandHandshakeResponse    CommandType = 0x00C0
	CommandGetDownloadStatus    CommandType = 0x0085
	CommandGetDownloadResponse  CommandType = 0x00C5
	CommandGetOffset            CommandType = 0x0086
	CommandGetOffsetResponse    CommandType = 0x00C6
	CommandRequestDownload      CommandType = 0x0081
	CommandRequestDownloadResp  CommandType = 0x00C1
	CommandDownloadData         CommandType = 0x0082
	CommandDownloadDataResponse CommandType = 0x00C2
	CommandDownloadComplete     CommandType = 0x008F
	CommandDownloadCompResponse CommandType = 0x00CF
	CommandUploadData           CommandType = 0x0083
	CommandUploadDataResponse   CommandType = 0x00C3
	CommandSwitchState          CommandType = 0x0071
	CommandSwitchStateResponse  CommandType = 0x00B1
	CommandReboot               CommandType = 0x0070
	CommandRebootResponse       CommandType = 0x00B0
	CommandSetRegister          CommandType = 0x0090
	CommandSetRegisterResponse  CommandType = 0x00D0
)

var (
	startFlag = []byte{0x41, 0x48} // "AH"
	endFlag   = []byte{0x4D, 0x49} // "MI"
)

// Command is a single framed protocol packet:
// [START 2B][CONTROL 2B][CMD TYPE 2B][CONTENT nB][CRC 2B][END 2B]
type Command struct {
	controlFlag [2]byte
	commandType CommandType
	content     []byte
	crc         [2]byte
	// parsed fields kept for response decoding
}

// NewCommand builds a command frame. When enableCRC is true the CRC bit in
// the control flag is set and a CRC-16/ARC is computed over
// controlFlag+commandType+content. When false the CRC field is left zero,
// exactly like the official Windows app does.
func NewCommand(cmdType CommandType, content []byte, enableCRC bool) *Command {
	c := &Command{
		commandType: cmdType,
		content:     content,
	}

	dataLen := len(content) + 2
	if enableCRC {
		c.controlFlag[0] |= 0x80
	}
	binary.BigEndian.PutUint16(c.controlFlag[:], uint16(dataLen)&0x7FFF)

	if enableCRC {
		var crcData []byte
		crcData = append(crcData, c.controlFlag[:]...)
		crcData = append(crcData, uint16Bytes(uint16(cmdType))...)
		crcData = append(crcData, content...)
		crc := crc16ARC(crcData)
		binary.BigEndian.PutUint16(c.crc[:], crc)
	}
	return c
}

func uint16Bytes(v uint16) []byte {
	return []byte{byte(v >> 8), byte(v)}
}

// Bytes returns the full framebuffer ready to be written to the serial port.
func (c *Command) Bytes() []byte {
	out := make([]byte, 0, len(startFlag)+2+2+len(c.content)+2+len(endFlag))
	out = append(out, startFlag...)
	out = append(out, c.controlFlag[:]...)
	out = append(out, uint16Bytes(uint16(c.commandType))...)
	out = append(out, c.content...)
	out = append(out, c.crc[:]...)
	out = append(out, endFlag...)
	return out
}

// parseResponse splits a received frame into its fields.
func parseResponse(buf []byte) (cmdType CommandType, control uint16, content, crc []byte, ok bool) {
	if len(buf) < 10 {
		return 0, 0, nil, nil, false
	}
	if buf[0] != 0x41 || buf[1] != 0x48 {
		return 0, 0, nil, nil, false
	}
	if buf[len(buf)-2] != 0x4D || buf[len(buf)-1] != 0x49 {
		return 0, 0, nil, nil, false
	}
	control = binary.BigEndian.Uint16(buf[2:4])
	dataLen := int(control & 0x7FFF)
	if dataLen < 2 {
		return 0, 0, nil, nil, false
	}
	cmdType = CommandType(binary.BigEndian.Uint16(buf[4:6]))
	content = buf[6 : 6+dataLen-2]
	crc = buf[6+dataLen-2 : 6+dataLen]
	return cmdType, control, content, crc, true
}
