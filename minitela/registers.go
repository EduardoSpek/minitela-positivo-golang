package minitela

import (
	"encoding/binary"
)

// Register (tag) IDs used by the Minitela firmware and the official app.
const (
	RegCPUUsage       uint16 = 1080
	RegGPUUsage       uint16 = 1081
	RegBatteryPercent uint16 = 1082
	RegWifiSSID       uint16 = 1083
	RegWifiQuality    uint16 = 1084
	RegBTName         uint16 = 1085
	RegBTStatus       uint16 = 1086
	RegWifiStatus     uint16 = 1087
	RegReminder1Text  uint16 = 1090
	RegReminder1Time  uint16 = 1091
	RegReminder2Text  uint16 = 1092
	RegReminder2Time  uint16 = 1093
	RegReminder3Text  uint16 = 1094
	RegReminder3Time  uint16 = 1095
	RegMediaName      uint16 = 1100
	RegMediaDuration  uint16 = 1101
	RegMediaNow       uint16 = 1102
	RegMediaPlay      uint16 = 2003
	RegBatteryType    uint16 = 1150
	// RegDateHour is the monitor page's top-bar clock (string), e.g. "28/10 14:00".
	RegDateHour uint16 = 2006
	// RegWhatsappLogo is the monitor page's WhatsApp logo visibility flag.
	RegWhatsappLogo uint16 = 2005
	// RegThemeAnimation controls the theme/layer animation (0/1).
	RegThemeAnimation uint16 = 65
)

// System tags (per the official app's systemTagNameMap).
const (
	RegSystemDate uint16 = 4
	RegSystemTime uint16 = 5
	// RegSystemBacklight is the display backlight register (0-100).
	RegSystemBacklight   uint16 = 7
	RegSystemCPU0Version uint16 = 12
	RegSystemCPU1Version uint16 = 13
	// RegSystemPage is the current page index (0-based, pageId-1).
	RegSystemPage uint16 = 2
)

// buildNumContent creates the SET_REGISTER content for a batch of numeric
// registers. Header: (0b1000<<4)|(count-1); then count x [regId UInt16BE][value UInt32BE].
func buildNumContent(tags []NumTag) []byte {
	if len(tags) == 0 || len(tags) > 16 {
		tags = tags[:16]
	}
	content := make([]byte, 1+len(tags)*6)
	content[0] = byte(0b1000<<4 | (len(tags)-1)&0x0F)
	for i, t := range tags {
		binary.BigEndian.PutUint16(content[1+i*6:], t.ID)
		binary.BigEndian.PutUint32(content[3+i*6:], uint32(t.Value))
	}
	return content
}

// buildStringContent creates the SET_REGISTER content for a single string
// register. Header: 0b11010000; then regId UInt16BE + len UInt16BE + string bytes.
func buildStringContent(regID uint16, payload []byte) []byte {
	content := make([]byte, 5+len(payload))
	content[0] = 0b11010000
	binary.BigEndian.PutUint16(content[1:], regID)
	binary.BigEndian.PutUint16(content[3:], uint16(len(payload)))
	copy(content[5:], payload)
	return content
}

// buildStringRequestContent builds the "get string register" request:
// header 0b11100000 + regId UInt16BE + requested length UInt16BE.
func buildStringRequestContent(regID uint16, length uint16) []byte {
	content := make([]byte, 5)
	content[0] = 0b11100000
	binary.BigEndian.PutUint16(content[1:], regID)
	binary.BigEndian.PutUint16(content[3:], length)
	return content
}

// buildNumRequestContent builds the "get numeric registers" request:
// header (0b1100<<4)|(count-1) + count x regId UInt16BE.
func buildNumRequestContent(regIDs []uint16) []byte {
	if len(regIDs) == 0 || len(regIDs) > 16 {
		regIDs = regIDs[:16]
	}
	content := make([]byte, 1+len(regIDs)*2)
	content[0] = byte(0b1100<<4 | (len(regIDs)-1)&0x0F)
	for i, r := range regIDs {
		binary.BigEndian.PutUint16(content[1+i*2:], r)
	}
	return content
}
