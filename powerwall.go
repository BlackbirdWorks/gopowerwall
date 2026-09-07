package gopowerwall

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/cloud"
	"github.com/blackbirdworks/gopowerwall/backend/fleetapi"
	"github.com/blackbirdworks/gopowerwall/backend/local"
	"github.com/blackbirdworks/gopowerwall/backend/tedapi"
	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/calc"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

// Lookup safely traverses nested maps and slices using variadic path keys.
func Lookup(data any, keys ...string) any {
	return lookup.Lookup(data, keys...)
}

const (
	maxConnectRetries  = 3
	connectRetryWait   = 30 * time.Second
	defaultBackupDur   = 3600
	reserveThreshold80 = 80.0
)

// Powerwall represents a Tesla Energy Gateway Powerwall device facade.
type Powerwall struct {
	config       *Config
	local        *local.PyPowerwallLocal
	tedapi       *tedapi.PyPowerwallTEDAPI
	cloud        *cloud.PyPowerwallCloud
	fleetapi     *fleetapi.PyPowerwallFleetAPI
	mode         ConnectionMode
	tedapiMode   TEDAPIMode
	mu           sync.RWMutex
	cloudmode    bool
	fleetapiFlag bool
	tedapiFlag   bool
}

// New creates and connects a new Powerwall instance.
func New(ctx context.Context, opts ...Option) (*Powerwall, error) {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	pw := &Powerwall{
		config:     cfg,
		mode:       ModeUnknown,
		tedapiMode: TEDAPIOff,
	}

	if cfg.Host == "" {
		cfg.CloudMode = true
	}
	switch {
	case cfg.CloudMode && !cfg.FleetAPI:
		pw.mode = ModeCloud
		pw.cloudmode = true
	case cfg.CloudMode && cfg.FleetAPI:
		pw.mode = ModeFleetAPI
		pw.cloudmode = true
		pw.fleetapiFlag = true
	case !cfg.CloudMode && !cfg.FleetAPI:
		pw.mode = ModeLocal
	}

	if cfg.AutoSelect {
		pw.autoSelectMode(ctx, cfg)
	}

	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	if !pw.Connect(ctx, cfg.RetryModes) {
		logger.Load(ctx).
			ErrorContext(ctx, "unable to connect to Powerwall, verify host, credentials and network connectivity")
	}

	return pw, nil
}

func (p *Powerwall) autoSelectMode(ctx context.Context, cfg *Config) {
	if cfg.Host != "" && !cfg.CloudMode && !cfg.FleetAPI {
		logger.Load(ctx).DebugContext(ctx, "auto selecting local mode")
		p.mode = ModeLocal
		p.cloudmode = false
		p.fleetapiFlag = false
	} else if _, statErr := os.Stat(filepath.Join(cfg.AuthPath, fleetapi.ConfigFile)); statErr == nil {
		logger.Load(ctx).DebugContext(ctx, "auto selecting FleetAPI mode")
		p.mode = ModeFleetAPI
		p.cloudmode = true
		p.fleetapiFlag = true
	} else if _, cloudStatErr := os.Stat(filepath.Join(cfg.AuthPath, cloud.AuthFile)); cloudStatErr == nil {
		p.mode = ModeCloud
		p.cloudmode = true
		p.fleetapiFlag = false
		logger.Load(ctx).DebugContext(ctx, "auto selecting cloud mode")
	} else {
		logger.Load(ctx).DebugContext(ctx, "auto select failed, no usable local, cloud or fleetapi mode")
	}
}

// Mode returns the active connection mode.
func (p *Powerwall) Mode() ConnectionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode
}

// TEDAPIMode returns the active TEDAPI sub-mode.
func (p *Powerwall) TEDAPIMode() TEDAPIMode {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.tedapiMode
}

func (p *Powerwall) connectLocal(ctx context.Context) bool {
	cfg := p.config

	if cfg.RSAKeyPath != "" {
		pwd := cfg.Password
		if pwd == "" && cfg.GwPwd != "" && len(cfg.GwPwd) >= 5 {
			pwd = cfg.GwPwd[len(cfg.GwPwd)-5:]
		}
		if pwd == "" {
			return false
		}
		v1r, err := tedapi.NewTEDAPIv1r(cfg.Host, pwd, cfg.RSAKeyPath, cfg.Timeout, cfg.PoolMaxSize)
		if err != nil {
			return false
		}
		tedClient := tedapi.NewClient(
			cfg.Host,
			cfg.GwPwd,
			cfg.Timeout,
			cfg.PWCacheExpire,
			cfg.PoolMaxSize,
			cfg.TEDAPIApiVersion,
			cfg.TEDAPIAuthMode,
		)
		tedClient.SetV1rTransport(v1r)
		p.tedapi = tedapi.NewBackend(tedClient, v1r)
		p.tedapiMode = TEDAPIV1r
		p.tedapiFlag = true

		return true
	}

	if cfg.Password == "" && cfg.GwPwd != "" {
		tedClient := tedapi.NewClient(
			cfg.Host,
			cfg.GwPwd,
			cfg.Timeout,
			cfg.PWCacheExpire,
			cfg.PoolMaxSize,
			cfg.TEDAPIApiVersion,
			cfg.TEDAPIAuthMode,
		)
		p.tedapi = tedapi.NewBackend(tedClient, nil)
		p.tedapiMode = TEDAPIFull
		p.tedapiFlag = true

		return true
	}

	localBackend := local.New(
		cfg.Host,
		cfg.Password,
		cfg.Email,
		cfg.Timezone,
		cfg.Timeout,
		cfg.PWCacheExpire,
		cfg.PoolMaxSize,
		cfg.AuthMode,
		cfg.CacheFile,
		cfg.GwPwd,
	)
	if cfg.GwPwd != "" && (cfg.Host == tedapi.DefaultGWIP || cfg.Host == tedapi.DefaultGWIP+":443") {
		tedClient := tedapi.NewClient(
			cfg.Host,
			cfg.GwPwd,
			cfg.Timeout,
			cfg.PWCacheExpire,
			cfg.PoolMaxSize,
			cfg.TEDAPIApiVersion,
			cfg.TEDAPIAuthMode,
		)
		if tedClient.Connect(ctx) {
			tedBackend := tedapi.NewBackend(tedClient, nil)
			localBackend.SetTEDAPIClient(tedBackend, false)
			p.tedapiMode = TEDAPIHybrid
			p.tedapiFlag = true
		}
	}

	if err := localBackend.Authenticate(ctx); err == nil {
		p.local = localBackend
		p.cloudmode = false
		p.fleetapiFlag = false

		return true
	}

	return false
}

func (p *Powerwall) connectFleetAPI(ctx context.Context) bool {
	fb := fleetapi.New(p.config.Email, p.config.PWCacheExpire, p.config.Timeout, p.config.SiteID, p.config.AuthPath)
	if err := fb.Authenticate(ctx); err == nil {
		p.fleetapi = fb
		p.cloudmode = true
		p.fleetapiFlag = true
		p.tedapiFlag = false
		p.tedapiMode = TEDAPIOff

		return true
	}

	return false
}

func (p *Powerwall) connectCloud(ctx context.Context) bool {
	cb := cloud.New(p.config.Email, p.config.PWCacheExpire, p.config.Timeout, p.config.SiteID, p.config.AuthPath)
	if err := cb.Authenticate(ctx); err == nil {
		p.cloud = cb
		p.cloudmode = true
		p.fleetapiFlag = false
		p.tedapiFlag = false
		p.tedapiMode = TEDAPIOff

		return true
	}

	return false
}

// Connect attempts connection with circular fallback (Local -> FleetAPI -> Cloud -> Local).
func (p *Powerwall) Connect(ctx context.Context, retry bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mode == ModeUnknown {
		logger.Load(ctx).ErrorContext(ctx, "unable to determine mode to connect")

		return false
	}

	for attempt := range maxConnectRetries {
		if retry && attempt == maxConnectRetries-1 {
			logger.Load(ctx).
				WarnContext(ctx, "failed to connect with all modes, waiting to retry", "wait", connectRetryWait)
			time.Sleep(connectRetryWait)
		}

		switch p.mode {
		case ModeLocal:
			logger.Load(ctx).DebugContext(ctx, "trying local mode")
			if p.connectLocal(ctx) {
				return true
			}
			logger.Load(ctx).WarnContext(ctx, "local mode failed, trying fleetapi mode")
			p.mode = ModeFleetAPI

		case ModeFleetAPI:
			logger.Load(ctx).DebugContext(ctx, "trying FleetAPI mode")
			if p.connectFleetAPI(ctx) {
				return true
			}
			logger.Load(ctx).WarnContext(ctx, "FleetAPI mode failed, trying cloud mode")
			p.mode = ModeCloud

		case ModeCloud:
			logger.Load(ctx).DebugContext(ctx, "trying cloud mode")
			if p.connectCloud(ctx) {
				return true
			}
			logger.Load(ctx).WarnContext(ctx, "cloud mode failed, trying local mode")
			p.mode = ModeLocal
		default:
			// Remaining modes do not support this operation.
		}
	}

	return false
}

// IsConnected returns whether the active backend is connected.
func (p *Powerwall) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.local != nil || p.tedapi != nil || p.cloud != nil || p.fleetapi != nil
}

// IsLocal returns true if connected locally.
func (p *Powerwall) IsLocal() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode == ModeLocal
}

// IsCloud returns true if connected via Tesla Cloud.
func (p *Powerwall) IsCloud() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode == ModeCloud
}

// IsFleetAPI returns true if connected via FleetAPI.
func (p *Powerwall) IsFleetAPI() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.mode == ModeFleetAPI
}

// IsTEDAPI returns true if TEDAPI mode is active.
func (p *Powerwall) IsTEDAPI() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.tedapiMode != TEDAPIOff
}

// Close disconnects and releases active backend resources.
func (p *Powerwall) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.local != nil {
		_ = p.local.Close(ctx)
		p.local = nil
	}
	if p.tedapi != nil {
		_ = p.tedapi.Close(ctx)
		p.tedapi = nil
	}
	if p.cloud != nil {
		p.cloud = nil
	}
	if p.fleetapi != nil {
		p.fleetapi = nil
	}

	return nil
}

// PollOption allows customizing Poll behavior.
type PollOption func(*pollConfig)

type pollConfig struct {
	force     bool
	recursive bool
	raw       bool
}

// WithForce forces cache bypass on Poll.
func WithForce(force bool) PollOption {
	return func(c *pollConfig) { c.force = force }
}

// WithRaw requests raw byte stream on Poll.
func WithRaw(raw bool) PollOption {
	return func(c *pollConfig) { c.raw = raw }
}

func (p *Powerwall) pollInternal(ctx context.Context, api string, force, recursive, raw bool) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			return p.local.Poll(ctx, api, force, recursive, raw)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			return p.tedapi.Poll(ctx, api, force, recursive, raw)
		}
	case ModeCloud:
		if p.cloud != nil {
			return p.cloud.Poll(ctx, api, force, recursive, raw)
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			return p.fleetapi.Poll(ctx, api, force, recursive, raw)
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil, ErrNoClient
}

// Poll queries the Powerwall Gateway API endpoint.
func (p *Powerwall) Poll(ctx context.Context, api string, opts ...PollOption) any {
	cfg := &pollConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	val, err := p.pollInternal(ctx, api, cfg.force, cfg.recursive, cfg.raw)
	if err != nil {
		return nil
	}

	return val
}

// PollRaw queries the endpoint and returns raw bytes.
func (p *Powerwall) PollRaw(ctx context.Context, api string, opts ...PollOption) []byte {
	cfg := &pollConfig{raw: true}
	for _, opt := range opts {
		opt(cfg)
	}
	cfg.raw = true

	val, err := p.pollInternal(ctx, api, cfg.force, cfg.recursive, true)
	if err != nil {
		return nil
	}
	if b, ok := val.([]byte); ok {
		return b
	}

	return nil
}

// PollJSON queries the endpoint and returns a JSON string.
func (p *Powerwall) PollJSON(ctx context.Context, api string, opts ...PollOption) string {
	res := p.Poll(ctx, api, opts...)
	if res == nil {
		return ""
	}
	if str, ok := res.(string); ok {
		return str
	}
	if b, ok := res.([]byte); ok {
		return string(b)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return ""
	}

	return string(b)
}

// Post sends a command payload to the Powerwall API endpoint.
func (p *Powerwall) Post(ctx context.Context, api string, payload any, din ...string) any {
	dinStr := ""
	if len(din) > 0 {
		dinStr = din[0]
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		res any
		err error
	)

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			res, err = p.local.Post(ctx, api, payload, dinStr, false, false)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			res, err = p.tedapi.Post(ctx, api, payload, dinStr, false, false)
		}
	case ModeCloud:
		if p.cloud != nil {
			res, err = p.cloud.Post(ctx, api, payload, dinStr, false, false)
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			res, err = p.fleetapi.Post(ctx, api, payload, dinStr, false, false)
		}
	default:
		// Remaining modes do not support this operation.
	}

	if err != nil {
		return nil
	}

	return res
}

// Level returns battery state of charge percentage.
func (p *Powerwall) Level(ctx context.Context, scale ...bool) *float64 {
	doScale := false
	if len(scale) > 0 {
		doScale = scale[0]
	}

	data := p.Poll(ctx, "/api/system_status/soe")
	if data == nil {
		return nil
	}

	pct := Lookup(data, "percentage")
	if pct == nil {
		return nil
	}

	var val float64
	switch v := pct.(type) {
	case float64:
		val = v
	case int:
		val = float64(v)
	}

	if doScale {
		val = calc.ScaleBatteryLevel(val)
	}

	return &val
}

// Power returns instant power metrics across all sensors in a strongly-typed PowerSummary.
func (p *Powerwall) Power(ctx context.Context) models.PowerSummary {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		res map[string]float64
		err error
	)

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			res, err = p.local.Power(ctx)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			res, err = p.tedapi.Power(ctx)
		}
	case ModeCloud:
		if p.cloud != nil {
			res, err = p.cloud.Power(ctx)
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			res, err = p.fleetapi.Power(ctx)
		}
	default:
		// Remaining modes do not support this operation.
	}

	if err != nil || res == nil {
		return models.PowerSummary{}
	}

	return models.PowerSummary{
		Site:    res["site"],
		Solar:   res["solar"],
		Battery: res["battery"],
		Load:    res["load"],
		Grid:    res["site"],
		Home:    res["load"],
	}
}

// Site returns site power in Watts or meter reading.
func (p *Powerwall) Site(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "site", verbose...)
}

// Solar returns solar power in Watts or meter reading.
func (p *Powerwall) Solar(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "solar", verbose...)
}

// Battery returns battery power in Watts or meter reading.
func (p *Powerwall) Battery(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "battery", verbose...)
}

// Load returns load power in Watts or meter reading.
func (p *Powerwall) Load(ctx context.Context, verbose ...bool) any {
	return p.fetchSensor(ctx, "load", verbose...)
}

// Grid returns grid power in Watts.
func (p *Powerwall) Grid(ctx context.Context, verbose ...bool) any { return p.Site(ctx, verbose...) }

// Home returns home load power in Watts.
func (p *Powerwall) Home(ctx context.Context, verbose ...bool) any { return p.Load(ctx, verbose...) }

func (p *Powerwall) fetchSensor(ctx context.Context, sensor string, verbose ...bool) any {
	isVerbose := false
	if len(verbose) > 0 {
		isVerbose = verbose[0]
	}

	if isVerbose {
		data := p.Poll(ctx, "/api/meters/aggregates")
		if data != nil {
			return Lookup(data, sensor)
		}

		return nil
	}

	summary := p.Power(ctx)
	switch sensor {
	case "site", "grid":
		return summary.Site
	case "solar":
		return summary.Solar
	case "battery":
		return summary.Battery
	case "load", "home":
		return summary.Load
	}

	return 0.0
}

// SiteName returns the site name.
func (p *Powerwall) SiteName(ctx context.Context) *string {
	data := p.Poll(ctx, "/api/site_info/site_name")
	if data == nil {
		return nil
	}
	name := Lookup(data, "site_name")
	if name == nil {
		return nil
	}
	s := fmt.Sprintf("%v", name)

	return &s
}

// Status returns gateway status.
func (p *Powerwall) Status(ctx context.Context, param ...string) any {
	data := p.Poll(ctx, "/api/status")
	if data == nil {
		return nil
	}
	if len(param) > 0 && param[0] != "" {
		return Lookup(data, param[0])
	}

	return data
}

// Version returns firmware version.
func (p *Powerwall) Version(ctx context.Context, intValue ...bool) any {
	s := p.Status(ctx, "version")
	if s == nil {
		return nil
	}
	strVal := fmt.Sprintf("%v", s)
	if len(intValue) > 0 && intValue[0] {
		return ParseVersion(strVal)
	}

	return strVal
}

// Uptime returns gateway uptime string.
func (p *Powerwall) Uptime(ctx context.Context) *string {
	s := p.Status(ctx, "up_time_seconds")
	if s == nil {
		return nil
	}
	strVal := fmt.Sprintf("%v", s)

	return &strVal
}

// Din returns gateway DIN.
func (p *Powerwall) Din(ctx context.Context) *string {
	s := p.Status(ctx, "din")
	if s == nil {
		return nil
	}
	strVal := fmt.Sprintf("%v", s)

	return &strVal
}

// Vitals returns full device vitals.
func (p *Powerwall) Vitals(ctx context.Context) (models.VitalsData, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		res map[string]any
		err error
	)

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			res, err = p.local.Vitals(ctx)
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			res, err = p.tedapi.Vitals(ctx)
		}
	default:
		// Remaining modes do not support this operation.
	}

	if err != nil {
		return models.VitalsData{}, err
	}

	devices := make(map[string]map[string]any)
	for k, v := range res {
		if devMap, ok := v.(map[string]any); ok {
			devices[k] = devMap
		}
	}

	return models.VitalsData{Devices: devices}, nil
}

// Temps returns temperatures of Powerwalls from vitals TETHC devices.
func (p *Powerwall) Temps(ctx context.Context) models.PowerwallTemps {
	temps := make(map[string]float64)
	vitals, err := p.Vitals(ctx)
	if err != nil || len(vitals.Devices) == 0 {
		return models.PowerwallTemps{Temps: temps}
	}

	for dev, data := range vitals.Devices {
		if strings.HasPrefix(dev, "TETHC") {
			if t, ok := data["THC_AmbientTemp"].(float64); ok {
				temps[dev] = t
			}
		}
	}

	return models.PowerwallTemps{Temps: temps}
}

// Alerts returns active system and device alerts.
func (p *Powerwall) Alerts(ctx context.Context, _ ...bool) models.AlertsList {
	alertSet := make(map[string]struct{})

	vitals, _ := p.Vitals(ctx)
	for _, data := range vitals.Devices {
		if rawAlerts, ok := data["alerts"].([]any); ok {
			for _, a := range rawAlerts {
				alertSet[fmt.Sprintf("%v", a)] = struct{}{}
			}
		}
	}

	gridStatus := p.Poll(ctx, "/api/system_status/grid_status")
	if gridStatus != nil {
		if Lookup(gridStatus, "grid_services_active") == true {
			alertSet["GridServicesActive"] = struct{}{}
		} else if gStatus := Lookup(gridStatus, "grid_status"); gStatus != nil {
			alertSet[fmt.Sprintf("%v", gStatus)] = struct{}{}
		}
	}

	list := make([]string, 0, len(alertSet))
	for a := range alertSet {
		norm := strings.ReplaceAll(a, "SystemGridConnected", "SystemConnectedToGrid")
		list = append(list, norm)
	}
	sort.Strings(list)

	return models.AlertsList{Alerts: list}
}

// Strings returns solar string measurements.
func (p *Powerwall) Strings(ctx context.Context, _ ...bool) models.SolarStrings {
	strMap := make(map[string]models.StringMetric)
	vitals, _ := p.Vitals(ctx)

	for dev, data := range vitals.Devices {
		if strings.HasPrefix(dev, "PVAC") {
			for stringID := range []string{"A", "B", "C", "D"} {
				key := strconv.Itoa(stringID)
				strMap[key] = models.StringMetric{
					Connected: true,
					Voltage:   LookupFloat(data, "PVAC_Vsolar"+key),
					Current:   LookupFloat(data, "PVAC_Isolar"+key),
					Power:     LookupFloat(data, "PVAC_Psolar"+key),
				}
			}
		}
	}

	return models.SolarStrings{Strings: strMap}
}

// BatteryBlocks returns battery module data.
func (p *Powerwall) BatteryBlocks(ctx context.Context) map[string]models.BatteryBlock {
	res := make(map[string]models.BatteryBlock)
	sys := p.Poll(ctx, "/api/system_status")
	if sys == nil {
		return res
	}

	blocks, ok := Lookup(sys, "battery_blocks").([]any)
	if !ok {
		return res
	}

	for _, b := range blocks {
		raw, err := json.Marshal(b)
		if err != nil {
			continue
		}
		var block models.BatteryBlock
		if unmarshalErr := json.Unmarshal(raw, &block); unmarshalErr == nil && block.PackageSerialNumber != "" {
			res[block.PackageSerialNumber] = block
		}
	}

	return res
}

// SystemStatus returns full system status.
func (p *Powerwall) SystemStatus(ctx context.Context) (models.SystemStatus, error) {
	raw := p.PollRaw(ctx, "/api/system_status")
	if len(raw) == 0 {
		return models.SystemStatus{}, ErrNotFound
	}
	var res models.SystemStatus
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SystemStatus{}, fmt.Errorf("unmarshal system status: %w", err)
	}

	return res, nil
}

// SOE returns state of energy percentage.
func (p *Powerwall) SOE(ctx context.Context) (models.SOE, error) {
	raw := p.PollRaw(ctx, "/api/system_status/soe")
	if len(raw) == 0 {
		return models.SOE{}, ErrNotFound
	}
	var res models.SOE
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SOE{}, fmt.Errorf("unmarshal soe: %w", err)
	}

	return res, nil
}

// GridStatus returns grid status formatted according to outputType.
func (p *Powerwall) GridStatus(ctx context.Context, outputType ...GridStatusOutput) any {
	t := GridStatusString
	if len(outputType) > 0 {
		t = outputType[0]
	}

	resp, err := p.GridStatusResponse(ctx)
	if err != nil {
		if t == GridStatusJSON {
			return "{}"
		}

		return "Unknown"
	}

	switch t {
	case GridStatusJSON:
		b, _ := json.Marshal(resp)

		return string(b)
	case GridStatusNumeric:
		if resp.GridStatus == "SystemGridConnected" || resp.GridStatus == "SystemConnectedToGrid" {
			return 1
		}

		return 0
	default:
		if resp.GridStatus == "SystemGridConnected" || resp.GridStatus == "SystemConnectedToGrid" {
			return "Connected"
		}

		return "Transition"
	}
}

// GridStatusResponse returns strongly-typed grid status.
func (p *Powerwall) GridStatusResponse(ctx context.Context) (models.GridStatusResponse, error) {
	raw := p.PollRaw(ctx, "/api/system_status/grid_status")
	if len(raw) == 0 {
		return models.GridStatusResponse{}, ErrNotFound
	}
	var res models.GridStatusResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.GridStatusResponse{}, fmt.Errorf("unmarshal grid status: %w", err)
	}

	return res, nil
}

// Operation returns active mode and backup reserve percentage.
func (p *Powerwall) Operation(ctx context.Context) (models.Operation, error) {
	raw := p.PollRaw(ctx, "/api/operation")
	if len(raw) == 0 {
		return models.Operation{}, ErrNotFound
	}
	var res models.Operation
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.Operation{}, fmt.Errorf("unmarshal operation: %w", err)
	}

	return res, nil
}

// SiteInfo returns site configuration parameters.
func (p *Powerwall) SiteInfo(ctx context.Context) (models.SiteInfo, error) {
	raw := p.PollRaw(ctx, "/api/site_info")
	if len(raw) == 0 {
		return models.SiteInfo{}, ErrNotFound
	}
	var res models.SiteInfo
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SiteInfo{}, fmt.Errorf("unmarshal site info: %w", err)
	}

	return res, nil
}

// GetReserve returns current backup reserve percentage.
func (p *Powerwall) GetReserve(ctx context.Context, scale ...bool) *float64 {
	op, err := p.Operation(ctx)
	if err != nil {
		return nil
	}
	val := op.BackupReservePercent
	if len(scale) > 0 && scale[0] {
		val = calc.ScaleBatteryLevel(val)
	}

	return &val
}

// GetMode returns current real mode.
func (p *Powerwall) GetMode(ctx context.Context) *string {
	op, err := p.Operation(ctx)
	if err != nil || op.RealMode == "" {
		return nil
	}

	return &op.RealMode
}

// SetReserve sets the battery reserve level (0-100).
func (p *Powerwall) SetReserve(ctx context.Context, level float64) (models.Operation, error) {
	return p.SetOperation(ctx, &level, nil)
}

// SetMode sets the battery operation mode.
func (p *Powerwall) SetMode(ctx context.Context, mode string) (models.Operation, error) {
	return p.SetOperation(ctx, nil, &mode)
}

// SetOperation sets battery reserve percentage and/or operation mode.
func (p *Powerwall) SetOperation(ctx context.Context, level *float64, mode *string) (models.Operation, error) {
	if level != nil && (*level < 0 || *level > 100) {
		return models.Operation{}, backend.ErrReserveOutOfRange
	}

	payload := make(map[string]any)
	if level != nil {
		payload["backup_reserve_percent"] = *level
	}
	if mode != nil && *mode != "" {
		payload["real_mode"] = *mode
	}

	dinStr := ""
	if d := p.Din(ctx); d != nil {
		dinStr = *d
	}

	res := p.Post(ctx, "/api/operation", payload, dinStr)
	if res == nil {
		return models.Operation{}, backend.ErrSetOperationFailed
	}

	result := models.Operation{}
	if level != nil {
		result.BackupReservePercent = *level
	}
	if mode != nil {
		result.RealMode = *mode
	}

	return result, nil
}

// GetTimeRemaining returns backup time remaining in hours.
func (p *Powerwall) GetTimeRemaining(ctx context.Context) *float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeLocal:
		if p.local != nil {
			t, _ := p.local.GetTimeRemaining(ctx)

			return t
		}
	case ModeTEDAPI, ModeV1r:
		if p.tedapi != nil {
			t, _ := p.tedapi.GetTimeRemaining(ctx)

			return t
		}
	case ModeCloud:
		if p.cloud != nil {
			t, _ := p.cloud.GetTimeRemaining(ctx)

			return t
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			t, _ := p.fleetapi.GetTimeRemaining(ctx)

			return t
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil
}

// SetGridCharging enables or disables grid charging.
func (p *Powerwall) SetGridCharging(ctx context.Context, mode bool) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			_, err := p.cloud.SetGridCharging(ctx, mode)

			return models.Operation{GridCharging: mode}, err
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			_, err := p.fleetapi.SetGridCharging(ctx, mode)

			return models.Operation{GridCharging: mode}, err
		}
	default:
		// Remaining modes do not support this operation.
	}

	return models.Operation{}, ErrUnsupported
}

// GetGridCharging returns the current grid charging setting.
func (p *Powerwall) GetGridCharging(ctx context.Context) *bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			b, _ := p.cloud.GetGridCharging(ctx)

			return b
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			b, _ := p.fleetapi.GetGridCharging(ctx)

			return b
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil
}

// SetGridExport sets grid export mode.
func (p *Powerwall) SetGridExport(ctx context.Context, mode string) (models.Operation, error) {
	if mode != "battery_ok" && mode != "pv_only" && mode != "never" {
		return models.Operation{}, fmt.Errorf(
			"%w: %s (must be battery_ok, pv_only, or never)",
			backend.ErrInvalidGridExportMode,
			mode,
		)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			_, err := p.cloud.SetGridExport(ctx, mode)

			return models.Operation{GridExport: mode}, err
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			_, err := p.fleetapi.SetGridExport(ctx, mode)

			return models.Operation{GridExport: mode}, err
		}
	default:
		// Remaining modes do not support this operation.
	}

	return models.Operation{}, ErrUnsupported
}

// GetGridExport returns current grid export mode.
func (p *Powerwall) GetGridExport(ctx context.Context) *string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			s, _ := p.cloud.GetGridExport(ctx)

			return s
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			s, _ := p.fleetapi.GetGridExport(ctx)

			return s
		}
	default:
		// Remaining modes do not support this operation.
	}

	return nil
}

// ScheduleMaxBackup schedules a maximum backup event.
func (p *Powerwall) ScheduleMaxBackup(ctx context.Context, durationSeconds ...int) (models.Operation, error) {
	dur := defaultBackupDur
	if len(durationSeconds) > 0 {
		dur = durationSeconds[0]
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.ScheduleMaxBackup(ctx, dur)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// CancelMaxBackup cancels a scheduled maximum backup event.
func (p *Powerwall) CancelMaxBackup(ctx context.Context) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.CancelMaxBackup(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// GetBackupEvents queries backup event history.
func (p *Powerwall) GetBackupEvents(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetBackupEvents(ctx)
	}

	return nil, ErrUnsupported
}

// GoOffGrid disconnects system from the grid.
func (p *Powerwall) GoOffGrid(ctx context.Context, confirm bool) (models.Operation, error) {
	if !confirm {
		return models.Operation{}, ErrOffGridConfirm
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.GoOffGrid(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// ReconnectGrid reconnects system to the grid.
func (p *Powerwall) ReconnectGrid(ctx context.Context) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.ReconnectGrid(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// GetFileStoreConfig returns TEDAPI configuration if active.
func (p *Powerwall) GetFileStoreConfig(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetConfig(ctx), nil
	}

	return nil, ErrUnsupported
}

// LookupFloat retrieves a float from an untyped map.
func LookupFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		switch num := v.(type) {
		case float64:
			return num
		case int:
			return float64(num)
		}
	}

	return 0.0
}
