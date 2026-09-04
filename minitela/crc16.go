package minitela

// crc16ARC computes the CRC-16/ARC (also known as CRC-16/IBM) checksum,
// which is what the official app's `crc` npm package (crc16) produces.
// It is reflected, poly 0x8005 (represented as 0xA001 for reflected
// right-shift), init 0x0000, no final XOR.
func crc16ARC(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}
