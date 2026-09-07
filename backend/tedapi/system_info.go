package tedapi

import (
	"encoding/hex"
	"unicode"
)

// SystemInfo represents normalized gateway firmware and device information.
type SystemInfo struct {
	DIN               string       `json:"din"`
	Version           string       `json:"version"`
	GitHash           string       `json:"githash"`
	PartNumber        string       `json:"part_number"`
	SerialNumber      string       `json:"serial_number"`
	DeviceType        string       `json:"device_type"`
	InstalledFirmware string       `json:"installed_firmware_signature"`
	OfflineFirmware   string       `json:"offline_firmware_signature"`
	WirelessRadios    []RadioInfo  `json:"wireless_radios"`
	SystemUpdate      SystemUpdate `json:"system_update"`
}

// SystemUpdate holds firmware update status.
type SystemUpdate struct {
	UpdateStatus            string `json:"update_status"`
	HandshakeResult         string `json:"handshake_result"`
	LastUpdateResult        string `json:"last_update_result"`
	ServerStagedVersion     string `json:"server_staged_version"`
	ServerStagedGitHash     string `json:"server_staged_githash"`
	TotalBytes              uint64 `json:"total_bytes"`
	BytesOffset             uint64 `json:"bytes_offset"`
	EstimatedBytesPerSecond uint64 `json:"estimated_bytes_per_second"`
	LastHandshakeTimestamp  uint64 `json:"last_handshake_timestamp"`
	IsSideloading           bool   `json:"is_sideloading"`
}

// RadioInfo represents a wireless compliance radio record.
type RadioInfo struct {
	Company string `json:"company"`
	Model   string `json:"model"`
	FCCID   string `json:"fcc_id"`
	IC      string `json:"ic"`
}

// DecodeGitHash formats raw githash bytes as printable ASCII string or hex string.
func DecodeGitHash(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	s := string(b)
	isPrintable := true
	for _, r := range s {
		if !unicode.IsPrint(r) {
			isPrintable = false

			break
		}
	}
	if isPrintable {
		return s
	}

	return hex.EncodeToString(b)
}

// ToDetailsDict converts SystemInfo to a map format expected by get_firmware_version(details=true).
func (s *SystemInfo) ToDetailsDict() map[string]any {
	radios := make([]map[string]string, 0, len(s.WirelessRadios))
	for _, r := range s.WirelessRadios {
		radios = append(radios, map[string]string{
			"company": r.Company,
			"model":   r.Model,
			"fcc_id":  r.FCCID,
			"ic":      r.IC,
		})
	}

	return map[string]any{
		"gateway": map[string]string{
			"partNumber":   s.PartNumber,
			"serialNumber": s.SerialNumber,
		},
		"version": map[string]string{
			"text":    s.Version,
			"githash": s.GitHash,
		},
		"din":        s.DIN,
		"deviceType": s.DeviceType,
		"wireless":   radios,
	}
}
