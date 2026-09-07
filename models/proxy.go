package models

// FrequencyMetrics contains grid and home AC frequency metrics.
type FrequencyMetrics struct {
	Grid float64 `json:"grid"`
	Home float64 `json:"home"`
}

// PODMetrics represents Powerwall battery block metrics for Prometheus / Telegraf.
type PODMetrics struct {
	Voltage                float64 `json:"voltage"`
	Current                float64 `json:"current"`
	Frequency              float64 `json:"frequency"`
	Power                  float64 `json:"power"`
	EnergyCharged          float64 `json:"energy_charged"`
	EnergyDischarged       float64 `json:"energy_discharged"`
	NominalEnergyRemaining float64 `json:"nominal_energy_remaining"`
	NominalFullPackEnergy  float64 `json:"nominal_full_pack_energy"`
}

// ProxyStats contains proxy server internal performance metrics.
type ProxyStats struct {
	SiteName         string         `json:"site_name"`
	Config           map[string]any `json:"config"`
	ConnectionHealth map[string]any `json:"connection_health,omitempty"`
	Uptime           string         `json:"uptime"`
	TotalGets        int            `json:"total_gets"`
	TotalPosts       int            `json:"total_posts"`
	TotalErrors      int            `json:"total_errors"`
	TotalTimeouts    int            `json:"total_timeouts"`
	ClearTS          int64          `json:"clear"`
	StartTS          int64          `json:"start"`
	MemKB            uint64         `json:"mem"`
	CloudMode        bool           `json:"cloudmode"`
	FleetAPI         bool           `json:"fleetapi"`
	TEDAPI           bool           `json:"tedapi"`
}

// CompositeJSON represents aggregated Powerwall dashboard data payload.
type CompositeJSON struct {
	SiteName     string           `json:"site_name"`
	Firmware     string           `json:"version"`
	DIN          string           `json:"din"`
	SystemStatus SystemStatus     `json:"system_status"`
	Aggregates   MetersAggregates `json:"aggregates"`
	Frequency    FrequencyMetrics `json:"frequency"`
	SOE          SOE              `json:"soe"`
}
