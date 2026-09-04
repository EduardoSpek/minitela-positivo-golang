package minitela

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

// Firmware memory addresses for OTA file uploads (from the official app's
// FileTypeAddrs map).
const (
	// FileTypeTextureGif is where a user-provided animated image ("GIF_0.acf")
	// is flashed; shown on the Imagem (image) page.
	FileTypeTextureGif uint32 = 0x08500000
	// FileTypeTexture is the master theme texture blob.
	FileTypeTexture uint32 = 0x08100000
)

// MaxDownloadFileSize is the firmware's accepted upload size cap (6436 KiB).
const MaxDownloadFileSize = 6436 * 1024

// downloadStatus mirrors the firmware's GET_DOWNLOAD_STATUS response.
type downloadStatus struct {
	status byte
	fileID string // 32 hex chars (MD5)
	offset uint32
}

// UploadFile flashes raw file bytes to the firmware at addr using the official
// app's OTA download state machine:
//
//	HANDSHAKE -> GET_DOWNLOAD_STATUS -> (maybe SWITCH_STATE) -> REQUEST_DOWNLOAD
//	  -> DOWNLOAD_DATA (chunks of maxPageSize) -> DOWNLOAD_COMPLETE.
//
// The entire sequence runs inside a single serial-worker closure so it cannot
// interleave with the monitor loop. progress, when non-nil, receives a 0-100
// integer on every chunk boundary.
func (c *Client) UploadFile(file []byte, addr uint32, progress func(int)) error {
	if len(file) > MaxDownloadFileSize {
		return fmt.Errorf("arquivo grande demais: %d bytes (máx %d)", len(file), MaxDownloadFileSize)
	}

	fileID := md5Hex(file)

	return c.enqueue(func() error {
		// 1. Handshake, exactly like the official app before any download.
		if _, err := c.sendDownloadRaw(CommandHandshake, nil, CommandHandshakeResponse, 60*time.Second); err != nil {
			return fmt.Errorf("handshake: %w", err)
		}

		st := downloadStatus{offset: 0}
		// 2. Query current download status.
		if err := c.stepDownloadStatus(&st); err != nil {
			return err
		}

		switch st.status {
		case 0x10, 0x11:
			// preparing or already downloading: reset to a full re-upload when
			// the file differs (same file resumes from the stored offset).
			if st.fileID != fileID {
				st.fileID = fileID
				st.offset = 0
			}
		case 0x20:
			// AHMI is done and already has this exact file -> nothing to do.
			if st.fileID == fileID {
				if progress != nil {
					progress(100)
				}
				return nil
			}
			// Switch the device into download state first.
			code, err := c.sendDownloadCode(CommandSwitchState, []byte{0x10}, CommandSwitchStateResponse)
			if err != nil {
				return fmt.Errorf("switch state: %w", err)
			}
			if code != 0 {
				return fmt.Errorf("switch state falhou (code %#x)", code)
			}
		default:
			return fmt.Errorf("status de download inválido: %#x", st.status)
		}

		// 3. Request the download (address + size + fileId MD5).
		reqContent := make([]byte, 24)
		binary.BigEndian.PutUint32(reqContent[0:], addr)
		binary.BigEndian.PutUint32(reqContent[4:], uint32(len(file)))
		idBytes, _ := hex.DecodeString(fileID)
		copy(reqContent[8:], idBytes)
		maxPageSize, _, err := c.sendDownloadReqData(CommandRequestDownload, reqContent, CommandRequestDownloadResp)
		if err != nil {
			return fmt.Errorf("request download: %w", err)
		}
		if maxPageSize == 0 {
			maxPageSize = 1024
		}

		// 4. Stream DOWNLOAD_DATA chunks, resuming at the stored offset.
		start := int(st.offset)
		if start > len(file) {
			start = 0
		}
		for start < len(file) {
			chunkEnd := start + int(maxPageSize)
			if chunkEnd > len(file) {
				chunkEnd = len(file)
			}
			chunk := file[start:chunkEnd]
			if err := c.sendDownloadChunkLocked(uint32(start), chunk); err != nil {
				return err
			}
			start = chunkEnd
			if progress != nil {
				progress(start * 100 / len(file))
			}
		}

		// 5. Finish the download (errors here are ignored by the official app).
		if _, err := c.sendDownloadRaw(CommandDownloadComplete, nil, CommandDownloadCompResponse, 5*time.Second); err != nil {
			return fmt.Errorf("download complete: %w", err)
		}
		return nil
	})
}

// sendDownloadChunkLocked sends a single DOWNLOAD_DATA frame and retries when
// the device reports failure, mirroring the official app's recursive resend.
// It MUST run inside the serial worker (p.mu may be held).
func (c *Client) sendDownloadChunkLocked(offset uint32, chunk []byte) error {
	content := make([]byte, 4+len(chunk))
	binary.BigEndian.PutUint32(content[0:], offset)
	copy(content[4:], chunk)

	resp, err := c.sendDownloadRaw(CommandDownloadData, content, CommandDownloadDataResponse, 60*time.Second)
	if err != nil {
		return fmt.Errorf("download data @%d: %w", offset, err)
	}
	code := parseSingleUint32(resp)
	if code == 0 {
		return nil
	}
	// device says failed (or was processing and still fails) -> resend.
	if code != 0xFFFFFFFF {
		return fmt.Errorf("download data @%d falhou (code %#x)", offset, code)
	}
	return c.sendDownloadChunkLocked(offset, chunk)
}

// stepDownloadStatus issues GET_DOWNLOAD_STATUS and parses it.
func (c *Client) stepDownloadStatus(st *downloadStatus) error {
	resp, err := c.sendDownloadRaw(CommandGetDownloadStatus, nil, CommandGetDownloadResponse, 60*time.Second)
	if err != nil {
		return fmt.Errorf("get download status: %w", err)
	}
	r, err := parseDownloadStatus(resp)
	if err != nil {
		return fmt.Errorf("get download status parse: %w", err)
	}
	st.status = r.status
	st.fileID = r.fileID
	st.offset = r.offset
	return nil
}

// sendDownloadRaw writes a command and waits for the expected response type via
// the serial worker. It does not acquire p.mu again, so it can be chained
// inside an UploadFile closure.
func (c *Client) sendDownloadRaw(cmdType CommandType, content []byte, expected CommandType, timeout time.Duration) ([]byte, error) {
	cmd := NewCommand(cmdType, content, false)
	return c.port.SendAndWait(cmd, expected, timeout)
}

// sendDownloadCode sends a command whose response payload is a single 32-bit
// code, retrying while the device reports "processing" (0xFFFFFFFF).
func (c *Client) sendDownloadCode(cmdType CommandType, content []byte, expected CommandType) (uint32, error) {
	var code uint32 = uint32(0xFFFFFFFF)
	for code == 0xFFFFFFFF {
		resp, err := c.sendDownloadRaw(cmdType, content, expected, 60*time.Second)
		if err != nil {
			return 0, err
		}
		code = parseSingleUint32(resp)
	}
	return code, nil
}

// sendDownloadReqData sends REQUEST_DOWNLOAD and returns maxPageSize and code.
func (c *Client) sendDownloadReqData(cmdType CommandType, content []byte, expected CommandType) (uint32, uint32, error) {
	var maxPageSize uint32
	var code uint32 = uint32(0xFFFFFFFF)
	for code == 0xFFFFFFFF {
		resp, err := c.sendDownloadRaw(cmdType, content, expected, 60*time.Second)
		if err != nil {
			return 0, 0, err
		}
		mp, cd, err := parseRequestDownloadResponse(resp)
		if err != nil {
			return 0, 0, err
		}
		maxPageSize = mp
		code = cd
	}
	return maxPageSize, code, nil
}

// parseDownloadStatus decodes a GET_DOWNLOAD_STATUS_RESPONSE frame.
func parseDownloadStatus(resp []byte) (downloadStatus, error) {
	st := downloadStatus{}
	_, _, content, _, ok := parseResponse(resp)
	if !ok || len(content) < 21 {
		return st, fmt.Errorf("resposta curta de download status")
	}
	st.status = content[0]
	st.fileID = hex.EncodeToString(content[1:17])
	st.offset = binary.BigEndian.Uint32(content[17:21])
	return st, nil
}

// parseRequestDownloadResponse decodes a REQUEST_DOWNLOAD_RESPONSE frame into
// maxPageSize and the response code.
func parseRequestDownloadResponse(resp []byte) (uint32, uint32, error) {
	_, _, content, _, ok := parseResponse(resp)
	if !ok || len(content) < 8 {
		return 0, 0, fmt.Errorf("resposta curta de request download")
	}
	maxPageSize := binary.BigEndian.Uint32(content[0:4])
	code := binary.BigEndian.Uint32(content[4:8])
	return maxPageSize, code, nil
}

// parseSingleUint32 reads the response code from a 4-byte payload, mirroring
// DOWNLOAD_DATA_RESPONSE / SWITCH_STATE_RESPONSE / DOWNLOAD_COMPLETE_RESPONSE.
func parseSingleUint32(resp []byte) uint32 {
	_, _, content, _, ok := parseResponse(resp)
	if !ok || len(content) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(content[0:4])
}

// md5Hex returns the lowercase hex MD5 of b (Go stdlib crypto/md5).
func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}