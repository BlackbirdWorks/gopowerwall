package models

// DiscoveredDevice represents an energy gateway discovered during scan.
type DiscoveredDevice struct {
	IP          string `json:"ip"`
	DIN         string `json:"din,omitempty"`
	Version     string `json:"version,omitempty"`
	DeviceType  string `json:"device_type,omitempty"`
	IsPowerwall bool   `json:"is_powerwall"`
}

// ScanOptions configures network scanning parameters.
type ScanOptions struct {
	CIDR        string
	IP          string
	TimeoutSec  float64
	MaxHosts    int
	Color       bool
	Interactive bool
	JSONOutput  bool
}
