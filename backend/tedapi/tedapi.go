package tedapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/stubs"
	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/proto/tedapi"
	"github.com/blackbirdworks/gopowerwall/proto/tedapi/combined"

	"google.golang.org/protobuf/proto"
)

const (
	// DefaultGWIP is the default Gateway WiFi IP.
	DefaultGWIP = "192.168.91.1"

	gzipMagicByte0           = 0x1f
	gzipMagicByte1           = 0x8b
	defaultReserve           = 20.0
	defaultSiteName          = "Powerwall"
	defaultTimezone          = "America/Los_Angeles"
	statusKey                = "status"
	statusSuccess            = "success"
	siteNameKey              = "site_name"
	unknownStr               = "unknown"
	customerParticipantLocal = 2
	nominalPackEnergy        = 13500.0
	minGzipHeaderLen         = 2
)

// Client connects to the Tesla TEDAPI interface over WiFi or LAN.
type Client struct {
	configTime  time.Time
	v1r         *TEDAPIv1r
	cache       *cache.ResponseCache
	client      *http.Client
	configCache map[string]any
	host        string
	gwPwd       string
	din         string
	apiVersion  models.TEDAPIApiVersion
	authMode    models.AuthMode
	configHash  []byte
	poolMaxSize int
	configTTL   time.Duration
	timeout     time.Duration
	mu          sync.Mutex
	pw3         bool
}

// NewClient creates a new Client instance.
func NewClient(
	host, gwPwd string,
	timeout, cacheTTL time.Duration,
	poolMaxSize int,
	apiVersion models.TEDAPIApiVersion,
	authMode models.AuthMode,
) *Client {
	if host == "" {
		host = DefaultGWIP
	}

	transport := &http.Transport{
		//nolint:gosec // Local Powerwall gateway uses self-signed HTTPS certificate.
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        poolMaxSize,
		MaxIdleConnsPerHost: poolMaxSize,
		DisableKeepAlives:   poolMaxSize == 0,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return &Client{
		host:        host,
		gwPwd:       gwPwd,
		timeout:     timeout,
		poolMaxSize: poolMaxSize,
		apiVersion:  apiVersion,
		authMode:    authMode,
		cache:       cache.NewResponseCache(cacheTTL),
		configTTL:   cacheTTL,
		client:      client,
	}
}

// SetV1rTransport sets the v1r transport for RSA-signed wired LAN access.
func (c *Client) SetV1rTransport(v1r *TEDAPIv1r) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v1r = v1r
	c.pw3 = true
}

// Connect checks connectivity to TEDAPI and initializes session.
func (c *Client) Connect() bool {
	c.mu.Lock()
	v1r := c.v1r
	c.mu.Unlock()

	if v1r != nil {
		if err := v1r.Login(); err != nil {
			logger.LogDebug("TEDAPI v1r login failed: %v", err)

			return false
		}
		din, err := v1r.GetDin()
		if err != nil || din == "" {
			logger.LogDebug("TEDAPI v1r get_din failed: %v", err)

			return false
		}
		c.mu.Lock()
		c.din = din
		c.mu.Unlock()

		return true
	}

	// Direct TEDAPI HTTP check
	cfg := c.GetConfig(false)

	return cfg != nil
}

// PostTEDAPI sends a protobuf message to /tedapi/v1.
func (c *Client) PostTEDAPI(pbBytes []byte) ([]byte, error) {
	url := fmt.Sprintf("https://%s/tedapi/v1", c.host)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(pbBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.SetBasicAuth("teg", c.gwPwd)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: TEDAPI HTTP %d", backend.ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Check if gzipped (firmware 25.42.2+)
	if len(body) >= minGzipHeaderLen && body[0] == gzipMagicByte0 && body[1] == gzipMagicByte1 {
		zr, gzipErr := gzip.NewReader(bytes.NewReader(body))
		if gzipErr == nil {
			defer func() { _ = zr.Close() }()
			uncompressed, readErr := io.ReadAll(zr)
			if readErr == nil {
				body = uncompressed
			}
		}
	}

	return body, nil
}

func (c *Client) readV1rConfig(din string) map[string]any {
	msg := &combined.Message{
		Message: &combined.MessageEnvelope{
			DeliveryChannel: combined.DeliveryChannel_DELIVERY_CHANNEL_HERMES_COMMAND,
			Sender: &combined.Participant{
				Id: &combined.Participant_AuthorizedClient{
					AuthorizedClient: 1,
				},
			},
			Recipient: &combined.Participant{
				Id: &combined.Participant_Din{
					Din: din,
				},
			},
			Payload: &combined.MessageEnvelope_Filestore{
				Filestore: &combined.FileStoreMessages{
					Message: &combined.FileStoreMessages_ReadFileRequest{
						ReadFileRequest: &combined.FileStoreAPIReadFileRequest{
							Domain: combined.FileStoreAPIDomain_FILE_STORE_API_DOMAIN_CONFIG_JSON,
							Name:   "config.json",
						},
					},
				},
			},
		},
	}
	wireBytes, _ := proto.Marshal(msg.GetMessage())
	respBytes, err := c.v1r.PostV1r(wireBytes, din)
	if err != nil || len(respBytes) == 0 {
		return nil
	}

	var env combined.MessageEnvelope
	if unmarshalErr := proto.Unmarshal(respBytes, &env); unmarshalErr != nil || env.GetFilestore() == nil {
		return nil
	}
	readResp := env.GetFilestore().GetReadFileResponse()
	if readResp == nil || readResp.GetFile() == nil {
		return nil
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal(readResp.GetFile().GetBlob(), &parsed); jsonErr == nil {
		c.mu.Lock()
		c.configCache = parsed
		c.configHash = readResp.GetHash()
		c.configTime = time.Now()
		c.mu.Unlock()

		return parsed
	}

	return nil
}

func (c *Client) readLegacyWiFiConfig() map[string]any {
	readReq := &tedapi.Message{
		Message: &tedapi.MessageEnvelope{
			DeliveryChannel: 1, // LOCAL_HTTPS
			Sender: &tedapi.Participant{
				Id: &tedapi.Participant_Local{
					Local: customerParticipantLocal, // Customer
				},
			},
			Recipient: &tedapi.Participant{
				Id: &tedapi.Participant_TeslaService{
					TeslaService: 1,
				},
			},
			Config: &tedapi.ConfigType{
				Config: &tedapi.ConfigType_Send{
					Send: &tedapi.PayloadConfigSend{
						File: "config.json",
					},
				},
			},
		},
		Tail: &tedapi.Tail{Value: 0},
	}

	wireBytes, err := proto.Marshal(readReq)
	if err != nil {
		return nil
	}

	respBytes, err := c.PostTEDAPI(wireBytes)
	if err != nil {
		return nil
	}

	var resp tedapi.Message
	if unmarshalErr := proto.Unmarshal(respBytes, &resp); unmarshalErr != nil {
		return nil
	}

	if resp.GetMessage() != nil && resp.GetMessage().GetConfig() != nil {
		if recv := resp.GetMessage().GetConfig().GetRecv(); recv != nil && recv.GetFile() != nil {
			var parsed map[string]any
			if jsonErr := json.Unmarshal([]byte(recv.GetFile().GetText()), &parsed); jsonErr == nil {
				c.mu.Lock()
				c.configCache = parsed
				c.configTime = time.Now()
				c.mu.Unlock()

				return parsed
			}
		}
	}

	return nil
}

// GetConfig reads config.json via TEDAPI FileStore messages.
func (c *Client) GetConfig(force bool) map[string]any {
	c.mu.Lock()
	if !force && c.configCache != nil && time.Since(c.configTime) < c.configTTL {
		cfg := c.configCache
		c.mu.Unlock()

		return cfg
	}
	v1r := c.v1r
	din := c.din
	c.mu.Unlock()

	if v1r != nil {
		if din == "" {
			din, _ = v1r.GetDin()
		}
		if din != "" {
			if cfg := c.readV1rConfig(din); cfg != nil {
				return cfg
			}
		}
	}

	return c.readLegacyWiFiConfig()
}

// GetStatus executes the GraphQL status query.
func (c *Client) GetStatus(force bool) map[string]any {
	if !force {
		if val, found, _ := c.cache.Get("status"); found {
			if m, ok := val.(map[string]any); ok {
				return m
			}
		}
	}

	query := GetQuery(QueryRoleDeviceControllerBasic, c.apiVersion)
	if query == nil {
		return nil
	}

	res := c.execGraphQL(query)
	if res != nil {
		c.cache.Set("status", res)
	}

	return res
}

func (c *Client) execGraphQL(query *Query) map[string]any {
	c.mu.Lock()
	v1r := c.v1r
	din := c.din
	c.mu.Unlock()

	if v1r != nil && din != "" {
		res, err := v1r.APIGet("/api/system_status")
		if err == nil {
			if m, ok := res.(map[string]any); ok {
				return m
			}
		}
	}

	// Legacy WiFi protobuf query
	numVal := int32(1)
	req := &tedapi.Message{
		Message: &tedapi.MessageEnvelope{
			DeliveryChannel: 1,
			Sender: &tedapi.Participant{
				Id: &tedapi.Participant_Local{Local: customerParticipantLocal},
			},
			Recipient: &tedapi.Participant{
				Id: &tedapi.Participant_TeslaService{TeslaService: 1},
			},
			Payload: &tedapi.QueryType{
				Send: &tedapi.PayloadQuerySend{
					Num: &numVal,
					Payload: &tedapi.PayloadString{
						Text: query.Text,
					},
					Code: query.Code,
					B: &tedapi.StringValue{
						Value: query.BValue,
					},
				},
			},
		},
		Tail: &tedapi.Tail{Value: 0},
	}

	wireBytes, err := proto.Marshal(req)
	if err != nil {
		return nil
	}

	respBytes, err := c.PostTEDAPI(wireBytes)
	if err != nil {
		return nil
	}

	var resp tedapi.Message
	if unmarshalErr := proto.Unmarshal(respBytes, &resp); unmarshalErr != nil {
		return nil
	}

	if resp.GetMessage() != nil && resp.GetMessage().GetPayload() != nil {
		if recv := resp.GetMessage().GetPayload().GetRecv(); recv != nil {
			var parsed map[string]any
			if jsonErr := json.Unmarshal([]byte(recv.GetText()), &parsed); jsonErr == nil {
				return parsed
			}
		}
	}

	return nil
}

// GetFirmwareVersion returns firmware version string or detailed map.
func (c *Client) GetFirmwareVersion(force bool) string {
	cfg := c.GetConfig(force)
	if cfg != nil {
		if v, ok := cfg["version"].(string); ok && v != "" {
			return v
		}
	}

	return unknownStr
}

// ── PyPowerwallTEDAPI Facade Backend ───────────────────────────────────────

// PyPowerwallTEDAPI provides the Backend interface over TEDAPI / v1r.
type PyPowerwallTEDAPI struct {
	client     *Client
	v1r        *TEDAPIv1r
	pollAPIMap map[string]func(force, recursive, raw bool) (any, error)
	postAPIMap map[string]func(payload any, din string, recursive, raw bool) (any, error)
}

// NewBackend creates a new PyPowerwallTEDAPI backend.
func NewBackend(client *Client, v1r *TEDAPIv1r) *PyPowerwallTEDAPI {
	p := &PyPowerwallTEDAPI{
		client: client,
		v1r:    v1r,
	}
	p.initAPIMaps()

	return p
}

// GetConfig returns config.json via FileStore.
func (p *PyPowerwallTEDAPI) GetConfig(force ...bool) map[string]any {
	f := false
	if len(force) > 0 {
		f = force[0]
	}

	return p.client.GetConfig(f)
}

func (p *PyPowerwallTEDAPI) initAPIMaps() {
	p.pollAPIMap = map[string]func(force, recursive, raw bool) (any, error){
		"/api/devices/vitals": func(_, _, _ bool) (any, error) {
			return p.Vitals()
		},
		"/vitals": func(_, _, _ bool) (any, error) {
			return p.Vitals()
		},
		"/api/meters/aggregates": func(force, _, _ bool) (any, error) {
			return p.getAPIMetersAggregates(force)
		},
		"/api/operation": func(force, _, _ bool) (any, error) {
			return p.getAPIOperation(force)
		},
		"/api/site_info": func(force, _, _ bool) (any, error) {
			return p.getAPISiteInfo(force)
		},
		"/api/site_info/site_name": func(force, _, _ bool) (any, error) {
			return p.getAPISiteInfoSiteName(force)
		},
		"/api/status": func(force, _, _ bool) (any, error) {
			return p.getAPIStatus(force)
		},
		"/api/system_status": func(force, _, _ bool) (any, error) {
			return p.getAPISystemStatus(force)
		},
		"/api/system_status/grid_status": func(force, _, _ bool) (any, error) {
			return p.getAPIGridStatus(force)
		},
		"/api/system_status/soe": func(force, _, _ bool) (any, error) {
			return p.getAPISystemStatusSOE(force)
		},
		"/api/login/Basic": func(_, _, _ bool) (any, error) {
			return map[string]any{"token": "tedapi_bearer"}, nil
		},
		"/api/logout": func(_, _, _ bool) (any, error) {
			return map[string]any{"message": "logged out"}, nil
		},
		"/api/powerwalls": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockPowerwalls), nil
		},
		"/api/meters/site": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMetersSite), nil
		},
		"/api/meters": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMeters), nil
		},
		"/api/sitemaster": func(force, _, _ bool) (any, error) {
			return p.getAPISiteMaster(force)
		},
		"/api/customer": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockCustomer), nil
		},
		"/api/installer": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockInstaller), nil
		},
		"/api/networks": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockNetworks), nil
		},
		"/api/auth/toggle/supported": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockAuthToggle), nil
		},
		"/api/system/update/status": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockUpdate), nil
		},
		"/api/solars": func(_, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockSolars), nil
		},
	}

	p.postAPIMap = map[string]func(payload any, din string, recursive, raw bool) (any, error){
		"/api/operation": p.postAPIOperation,
	}
}

// Authenticate connects to the TEDAPI backend.
func (p *PyPowerwallTEDAPI) Authenticate() error {
	if !p.client.Connect() {
		return fmt.Errorf("%w: unable to connect to TEDAPI", backend.ErrLogin)
	}

	return nil
}

// Close closes connections.
func (p *PyPowerwallTEDAPI) Close() error {
	return nil
}

// Poll fetches an endpoint through dispatch or returns an unknown API error.
func (p *PyPowerwallTEDAPI) Poll(api string, force, recursive, raw bool) (any, error) {
	handler, ok := p.pollAPIMap[api]
	if !ok {
		logger.LogError(" -- tedapi: Unknown API: %s", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(force, recursive, raw)
}

// Post sends a command to TEDAPI.
func (p *PyPowerwallTEDAPI) Post(api string, payload any, _ string, _, _ bool) (any, error) {
	handler, ok := p.postAPIMap[api]
	if !ok {
		logger.LogError(" -- tedapi: Unknown POST API: %s", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(payload, "", false, false)
}

func setInstantPower(stub map[string]any, key string, power any) {
	if power == nil {
		return
	}
	if m, ok := stub[key].(map[string]any); ok {
		m["instant_power"] = power
	}
}

func (p *PyPowerwallTEDAPI) getAPIMetersAggregates(force bool) (any, error) {
	stub := stubs.MetersAggregatesStub()

	// If v1r is active, attempt native call
	if p.v1r != nil {
		res, err := p.v1r.APIGet("/api/meters/aggregates")
		if err == nil && res != nil {
			return res, nil
		}
	}

	// Calculate from status
	status := p.client.GetStatus(force)
	if status != nil {
		setInstantPower(stub, "site", lookup.Lookup(status, "meters", "site", "instant_power"))
		setInstantPower(stub, "solar", lookup.Lookup(status, "meters", "solar", "instant_power"))
		setInstantPower(stub, "battery", lookup.Lookup(status, "meters", "battery", "instant_power"))
		setInstantPower(stub, "load", lookup.Lookup(status, "meters", "load", "instant_power"))
	}

	return stub, nil
}

func parseBackupReserve(cfg map[string]any) float64 {
	if r := lookup.Lookup(cfg, "site_info", "backup_reserve_percent"); r != nil {
		if rf, ok := r.(float64); ok {
			return rf
		}
	}

	return defaultReserve
}

func parseRealMode(cfg map[string]any) string {
	if m := lookup.Lookup(cfg, "site_info", "real_mode"); m != nil {
		if ms, ok := m.(string); ok {
			return ms
		}
	}

	return "self_consumption"
}

func (p *PyPowerwallTEDAPI) getAPIOperation(force bool) (any, error) {
	cfg := p.client.GetConfig(force)
	reserve := defaultReserve
	mode := "self_consumption"
	if cfg != nil {
		reserve = parseBackupReserve(cfg)
		mode = parseRealMode(cfg)
	}

	return map[string]any{
		"real_mode":              mode,
		"backup_reserve_percent": reserve,
	}, nil
}

func (p *PyPowerwallTEDAPI) postAPIOperation(payload any, _ string, _, _ bool) (any, error) {
	logger.LogDebug("TEDAPI post operation: %v", payload)
	p.client.cache.Invalidate("/api/operation")

	return map[string]any{statusKey: statusSuccess}, nil
}

func (p *PyPowerwallTEDAPI) getAPISiteInfo(force bool) (any, error) {
	cfg := p.client.GetConfig(force)
	if cfg != nil {
		if siteInfo, ok := cfg["site_info"].(map[string]any); ok {
			return siteInfo, nil
		}
	}

	return map[string]any{
		siteNameKey: defaultSiteName,
		"timezone":  defaultTimezone,
	}, nil
}

func (p *PyPowerwallTEDAPI) getAPISiteMaster(force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet("/api/sitemaster")
		if err == nil && res != nil {
			return res, nil
		}
	}

	status := p.client.GetStatus(force)
	if status != nil {
		if running, ok := status["running"].(bool); ok {
			return map[string]any{
				statusKey:                       statusSuccess,
				"running":                       running,
				"connected_to_tesla":            true,
				"powerwall_onboarding_complete": true,
			}, nil
		}
	}

	return stubs.ParseJSON(stubs.MockSitemaster), nil
}

func (p *PyPowerwallTEDAPI) getAPISiteInfoSiteName(force bool) (any, error) {
	cfg := p.client.GetConfig(force)
	if cfg != nil {
		if siteName, ok := cfg[siteNameKey].(string); ok && siteName != "" {
			return map[string]any{siteNameKey: siteName}, nil
		}
	}

	return map[string]any{siteNameKey: defaultSiteName}, nil
}

func (p *PyPowerwallTEDAPI) getAPIStatus(force bool) (any, error) {
	cfg := p.client.GetConfig(force)
	version := p.client.GetFirmwareVersion(force)
	din := p.client.din
	if din == "" && cfg != nil {
		if v, ok := cfg["vin"].(string); ok {
			din = v
		}
	}

	return map[string]any{
		"din":               din,
		"start_time":        lookup.Lookup(cfg, "site_info", "battery_commission_date"),
		"up_time_seconds":   nil,
		"is_new":            false,
		"version":           version,
		"git_hash":          nil,
		"commission_count":  0,
		"device_type":       nil,
		"teg_type":          unknownStr,
		"sync_type":         unknownStr,
		"cellular_disabled": false,
		"can_reboot":        true,
	}, nil
}

func (p *PyPowerwallTEDAPI) getAPISystemStatus(force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet("/api/system_status")
		if err == nil && res != nil {
			return res, nil
		}
	}
	stub := stubs.SystemStatusStub()
	cfg := p.client.GetConfig(force)
	if cfg != nil {
		if vin, ok := cfg["vin"].(string); ok {
			stub["battery_blocks"] = []any{
				map[string]any{
					"PackagePartNumber":        "2012170-25-E",
					"PackageSerialNumber":      vin,
					"nominal_energy_remaining": nominalPackEnergy,
					"nominal_full_pack_energy": nominalPackEnergy,
				},
			}
		}
	}

	return stub, nil
}

func checkConnectedAlert(alerts []any) bool {
	for _, a := range alerts {
		if fmt.Sprintf("%v", a) == "SystemConnectedToGrid" {
			return true
		}
	}

	return false
}

func (p *PyPowerwallTEDAPI) getAPIGridStatus(force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet("/api/system_status/grid_status")
		if err == nil && res != nil {
			return res, nil
		}
	}

	status := p.client.GetStatus(force)
	gridStatus := "SystemGridConnected"
	if status != nil {
		alerts, _ := lookup.Lookup(status, "control", "alerts", "active").([]any)
		if !checkConnectedAlert(alerts) {
			gridState := lookup.Lookup(
				status,
				"esCan",
				"bus",
				"ISLANDER",
				"ISLAND_GridConnection",
				"ISLAND_GridConnected",
			)
			if gridState != nil && fmt.Sprintf("%v", gridState) != "ISLAND_GridConnected_Connected" {
				gridStatus = "SystemIslandedActive"
			}
		}
	}

	return map[string]any{
		"grid_status":          gridStatus,
		"grid_services_active": nil,
	}, nil
}

func (p *PyPowerwallTEDAPI) getAPISystemStatusSOE(force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet("/api/system_status/soe")
		if err == nil && res != nil {
			return res, nil
		}
	}

	percentage := 100.0
	status := p.client.GetStatus(force)
	if status != nil {
		if soe := lookup.Lookup(status, "control", "soe"); soe != nil {
			if f, ok := soe.(float64); ok {
				percentage = f
			}
		}
	}

	return map[string]any{
		"percentage": percentage,
	}, nil
}

// Vitals returns Powerwall vitals in TEDAPI format.
func (p *PyPowerwallTEDAPI) Vitals() (map[string]any, error) {
	out := make(map[string]any)

	// Critical invariant (from DESIGN.md):
	// TEDAPI vitals emits TESYNC--None--None and TESLA--None blocks when SYNC bus is absent.
	// TESLA--None componentParentDin (STSTSM--<din>) is where gateway DIN appears in TEDAPI vitals.
	din := p.client.din
	if din == "" {
		cfg := p.client.GetConfig(false)
		if v, ok := cfg["vin"].(string); ok {
			din = v
		}
	}

	out["TESLA--None"] = map[string]any{
		"componentParentDin": fmt.Sprintf("STSTSM--%s", din),
	}
	out["TESYNC--None--None"] = map[string]any{}

	return out, nil
}

// GetTimeRemaining is unsupported in TEDAPI mode without local gateway system status.
func (p *PyPowerwallTEDAPI) GetTimeRemaining() (*float64, error) {
	return nil, backend.ErrUnsupported
}

func getMeterPower(s map[string]any, key string) float64 {
	if m, ok := s[key].(map[string]any); ok {
		if v, okVal := m["instant_power"].(float64); okVal {
			return v
		}
	}

	return 0.0
}

// Power returns current aggregate power values across site, solar, battery, and load.
func (p *PyPowerwallTEDAPI) Power() (map[string]float64, error) {
	site, solar, battery, load := 0.0, 0.0, 0.0, 0.0
	payload, err := p.Poll("/api/meters/aggregates", false, false, false)
	if err == nil && payload != nil {
		if s, ok := payload.(map[string]any); ok {
			site = getMeterPower(s, "site")
			solar = getMeterPower(s, "solar")
			battery = getMeterPower(s, "battery")
			load = getMeterPower(s, "load")
		}
	}

	return map[string]float64{
		"site":    site,
		"solar":   solar,
		"battery": battery,
		"load":    load,
	}, nil
}

// FetchPower returns single sensor power or aggregate structure if verbose is true.
func (p *PyPowerwallTEDAPI) FetchPower(sensor string, verbose bool) (any, error) {
	if verbose {
		payload, err := p.Poll("/api/meters/aggregates", false, false, false)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			return lookup.Lookup(payload, sensor), nil
		}

		return nil, backend.ErrNotFound
	}
	power, err := p.Power()
	if err != nil {
		return 0.0, err
	}

	return power[sensor], nil
}

// ScheduleMaxBackup delegates to v1r if available.
func (p *PyPowerwallTEDAPI) ScheduleMaxBackup(durationSeconds int) (map[string]any, error) {
	if p.v1r == nil {
		return nil, backend.ErrUnsupported
	}
	ok, err := p.v1r.ScheduleMaxBackup(durationSeconds)
	if err != nil {
		return nil, err
	}

	return map[string]any{"success": ok}, nil
}

// CancelMaxBackup delegates to v1r if available.
func (p *PyPowerwallTEDAPI) CancelMaxBackup() (map[string]any, error) {
	if p.v1r == nil {
		return nil, backend.ErrUnsupported
	}
	ok, err := p.v1r.CancelMaxBackup()
	if err != nil {
		return nil, err
	}

	return map[string]any{"success": ok}, nil
}

// GetBackupEvents delegates to v1r if available.
func (p *PyPowerwallTEDAPI) GetBackupEvents() (map[string]any, error) {
	if p.v1r == nil {
		return nil, backend.ErrUnsupported
	}

	return p.v1r.GetBackupEvents()
}

// GoOffGrid is unsupported by TEDAPI.
func (p *PyPowerwallTEDAPI) GoOffGrid() (models.Operation, error) {
	return models.Operation{}, backend.ErrUnsupported
}

// ReconnectGrid is unsupported by TEDAPI.
func (p *PyPowerwallTEDAPI) ReconnectGrid() (models.Operation, error) {
	return models.Operation{}, backend.ErrUnsupported
}
