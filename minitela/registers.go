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

	// Weather (Clima) page registers. Per-register layout (official theme):
	// Weather_N_Type = condition glyph index (number), Weather_N_Temp =
	// temperature as int, Weather_N_Temp_Min/Max = daily min/max ints, and
	// Weather_N_Temp_Desc = a label string (the official app writes the date).
	Weather1Type      uint16 = 1110
	Weather1Temp      uint16 = 1111
	Weather1TempMin   uint16 = 1112
	Weather1TempMax   uint16 = 1113
	Weather1TempDesc  uint16 = 1114
	Weather2Type      uint16 = 1115
	Weather2Temp      uint16 = 1116
	Weather2TempMin   uint16 = 1117
	Weather2TempMax   uint16 = 1118
	Weather2TempDesc  uint16 = 1119
	Weather3Type      uint16 = 1120
	Weather3Temp      uint16 = 1121
	Weather3TempMin   uint16 = 1122
	Weather3TempMax   uint16 = 1123
	Weather3TempDesc  uint16 = 1124
	Weather4Type      uint16 = 1125
	Weather4Temp      uint16 = 1126
	Weather4TempMin   uint16 = 1127
	Weather4TempMax   uint16 = 1128
	Weather4TempDesc  uint16 = 1129
	Weather5Type      uint16 = 1130
	Weather5Temp      uint16 = 1131
	Weather5TempMin   uint16 = 1132
	Weather5TempMax   uint16 = 1133
	Weather5TempDesc  uint16 = 1134

	// The Clima page's extra custom registers (see the theme data.json):
	// city (2027) = location label, currentTemp (2030) = "25°/26°", and
	// forecastTemp1/2 (2031/2032) = the next two days' temperatures.
	RegCity          uint16 = 2027
	RegCurrentTemp   uint16 = 2030
	RegForecastTemp1 uint16 = 2031
	RegForecastTemp2 uint16 = 2032
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
