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
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
	"github.com/blackbirdworks/gopowerwall/powerwall/proto/tedapi"
	"github.com/blackbirdworks/gopowerwall/powerwall/proto/tedapi/combined"
	"github.com/blackbirdworks/gopowerwall/powerwall/stubs"

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
func (c *Client) Connect(ctx context.Context) bool {
	c.mu.Lock()
	v1r := c.v1r
	c.mu.Unlock()

	if v1r != nil {
		if err := v1r.Login(ctx); err != nil {
			logger.Load(ctx).DebugContext(ctx, "TEDAPI v1r login failed", "error", err)

			return false
		}
		din, err := v1r.GetDin(ctx)
		if err != nil || din == "" {
			logger.Load(ctx).DebugContext(ctx, "TEDAPI v1r get_din failed", "error", err)

			return false
		}
		c.mu.Lock()
		c.din = din
		c.mu.Unlock()

		return true
	}

	// Direct TEDAPI HTTP check
	cfg := c.GetConfig(ctx, false)

	return cfg != nil
}

// PostTEDAPI sends a protobuf message to /tedapi/v1.
func (c *Client) PostTEDAPI(ctx context.Context, pbBytes []byte) ([]byte, error) {
	url := fmt.Sprintf("https://%s/tedapi/v1", c.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(pbBytes))
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
		return nil, fmt.Errorf("%w: TEDAPI HTTP %d", models.ErrUnexpectedStatus, resp.StatusCode)
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

func (c *Client) readV1rConfig(ctx context.Context, din string) map[string]any {
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
	respBytes, err := c.v1r.PostV1r(ctx, wireBytes, din)
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

func (c *Client) readLegacyWiFiConfig(ctx context.Context) map[string]any {
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

	respBytes, err := c.PostTEDAPI(ctx, wireBytes)
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
func (c *Client) GetConfig(ctx context.Context, force bool) map[string]any {
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
			din, _ = v1r.GetDin(ctx)
		}
		if din != "" {
			if cfg := c.readV1rConfig(ctx, din); cfg != nil {
				return cfg
			}
		}
	}

	return c.readLegacyWiFiConfig(ctx)
}

// GetStatus executes the GraphQL status query.
func (c *Client) GetStatus(ctx context.Context, force bool) map[string]any {
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

	res := c.execGraphQL(ctx, query)
	if res != nil {
		c.cache.Set("status", res)
	}

	return res
}

// GetComponents executes the PW3 components GraphQL query (ComponentsQuery /
// PW3Query) whose response carries the raw PCH_Pv* per-string signals used
// to synthesize solar-string vitals - see [synthesizeStringVitals].
func (c *Client) GetComponents(ctx context.Context, force bool) map[string]any {
	if !force {
		if val, found, _ := c.cache.Get("components"); found {
			if m, ok := val.(map[string]any); ok {
				return m
			}
		}
	}

	query := GetQuery(QueryRoleComponents, c.apiVersion)
	if query == nil {
		return nil
	}

	res := c.execGraphQL(ctx, query)
	if res != nil {
		c.cache.Set("components", res)
	}

	return res
}

// GetDeviceController executes the DEVICE_CONTROLLER_FULL GraphQL query,
// mirroring pypowerwall's own get_device_controller
// (pypowerwall/tedapi/__init__.py:738-755, cached under the same
// "controller" name there). Its response is a superset of [Client.GetStatus]
// carrying an additional "components" section - see [ExtractFanSpeeds],
// the only current caller, for what that section is used for.
func (c *Client) GetDeviceController(ctx context.Context, force bool) map[string]any {
	if !force {
		if val, found, _ := c.cache.Get("controller"); found {
			if m, ok := val.(map[string]any); ok {
				return m
			}
		}
	}

	query := GetQuery(QueryRoleDeviceControllerFull, c.apiVersion)
	if query == nil {
		return nil
	}

	res := c.execGraphQL(ctx, query)
	if res != nil {
		c.cache.Set("controller", res)
	}

	return res
}

func (c *Client) execGraphQL(ctx context.Context, query *Query) map[string]any {
	c.mu.Lock()
	v1r := c.v1r
	din := c.din
	c.mu.Unlock()

	if v1r != nil && din != "" {
		res, err := v1r.APIGet(ctx, "/api/system_status")
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

	respBytes, err := c.PostTEDAPI(ctx, wireBytes)
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
func (c *Client) GetFirmwareVersion(ctx context.Context, force bool) string {
	cfg := c.GetConfig(ctx, force)
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
	pollAPIMap map[string]func(ctx context.Context, force, recursive, raw bool) (any, error)
	postAPIMap map[string]func(ctx context.Context, payload any, din string, recursive, raw bool) (any, error)
}

// Driver is an idiomatic alias for PyPowerwallTEDAPI.
type Driver = PyPowerwallTEDAPI

// NewBackend creates a new PyPowerwallTEDAPI models.
func NewBackend(client *Client, v1r *TEDAPIv1r) *PyPowerwallTEDAPI {
	p := &PyPowerwallTEDAPI{
		client: client,
		v1r:    v1r,
	}
	p.initAPIMaps()

	return p
}

// GetConfig returns config.json via FileStore.
func (p *PyPowerwallTEDAPI) GetConfig(ctx context.Context, force ...bool) map[string]any {
	f := false
	if len(force) > 0 {
		f = force[0]
	}

	return p.client.GetConfig(ctx, f)
}

// GetStatus returns the basic device controller status map.
func (p *PyPowerwallTEDAPI) GetStatus(ctx context.Context, force ...bool) map[string]any {
	f := false
	if len(force) > 0 {
		f = force[0]
	}

	return p.client.GetStatus(ctx, f)
}

// GetComponents returns the raw component signals query response.
func (p *PyPowerwallTEDAPI) GetComponents(ctx context.Context, force ...bool) map[string]any {
	f := false
	if len(force) > 0 {
		f = force[0]
	}

	return p.client.GetComponents(ctx, f)
}

// GetBatteryBlocks returns battery blocks extracted from configuration.
func (p *PyPowerwallTEDAPI) GetBatteryBlocks(ctx context.Context, force ...bool) []any {
	cfg := p.GetConfig(ctx, force...)
	if cfg == nil {
		return []any{}
	}
	blocks, ok := cfg["battery_blocks"].([]any)
	if !ok || blocks == nil {
		return []any{}
	}

	return blocks
}

// GetDeviceController returns the full device controller query response.
func (p *PyPowerwallTEDAPI) GetDeviceController(ctx context.Context, force ...bool) map[string]any {
	f := false
	if len(force) > 0 {
		f = force[0]
	}

	return p.client.GetDeviceController(ctx, f)
}

func (p *PyPowerwallTEDAPI) initAPIMaps() {
	p.pollAPIMap = map[string]func(ctx context.Context, force, recursive, raw bool) (any, error){
		"/api/devices/vitals": func(ctx context.Context, _, _, _ bool) (any, error) {
			return p.Vitals(ctx)
		},
		"/vitals": func(ctx context.Context, _, _, _ bool) (any, error) {
			return p.Vitals(ctx)
		},
		"/api/meters/aggregates": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPIMetersAggregates(ctx, force)
		},
		"/api/operation": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPIOperation(ctx, force)
		},
		"/api/site_info": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPISiteInfo(ctx, force)
		},
		"/api/site_info/site_name": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPISiteInfoSiteName(ctx, force)
		},
		"/api/status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPIStatus(ctx, force)
		},
		"/api/system_status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPISystemStatus(ctx, force)
		},
		"/api/system_status/grid_status": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPIGridStatus(ctx, force)
		},
		"/api/system_status/soe": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPISystemStatusSOE(ctx, force)
		},
		"/api/login/Basic": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{"token": "tedapi_bearer"}, nil
		},
		"/api/logout": func(_ context.Context, _, _, _ bool) (any, error) {
			return map[string]any{"message": "logged out"}, nil
		},
		"/api/powerwalls": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockPowerwalls), nil
		},
		"/api/meters/site": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMetersSite), nil
		},
		"/api/meters": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockMeters), nil
		},
		"/api/sitemaster": func(ctx context.Context, force, _, _ bool) (any, error) {
			return p.getAPISiteMaster(ctx, force)
		},
		"/api/customer": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockCustomer), nil
		},
		"/api/installer": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockInstaller), nil
		},
		"/api/networks": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockNetworks), nil
		},
		"/api/auth/toggle/supported": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockAuthToggle), nil
		},
		"/api/system/update/status": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockUpdate), nil
		},
		"/api/solars": func(_ context.Context, _, _, _ bool) (any, error) {
			return stubs.ParseJSON(stubs.MockSolars), nil
		},
	}

	p.postAPIMap = map[string]func(ctx context.Context, payload any, din string, recursive, raw bool) (any, error){
		"/api/operation": p.postAPIOperation,
	}
}

// Authenticate connects to the TEDAPI models.
func (p *PyPowerwallTEDAPI) Authenticate(ctx context.Context) error {
	if !p.client.Connect(ctx) {
		return fmt.Errorf("%w: unable to connect to TEDAPI", models.ErrLogin)
	}

	return nil
}

// Close closes connections.
func (p *PyPowerwallTEDAPI) Close(_ context.Context) error {
	return nil
}

// Poll fetches an endpoint through dispatch or returns an unknown API error.
func (p *PyPowerwallTEDAPI) Poll(ctx context.Context, api string, force, recursive, raw bool) (any, error) {
	handler, ok := p.pollAPIMap[api]
	if !ok {
		logger.Load(ctx).ErrorContext(ctx, "unknown TEDAPI poll endpoint", "api", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(ctx, force, recursive, raw)
}

// Post sends a command to TEDAPI.
func (p *PyPowerwallTEDAPI) Post(ctx context.Context, api string, payload any, _ string, _, _ bool) (any, error) {
	handler, ok := p.postAPIMap[api]
	if !ok {
		logger.Load(ctx).ErrorContext(ctx, "unknown TEDAPI post endpoint", "api", api)

		return map[string]string{"ERROR": "Unknown API: " + api}, nil
	}

	return handler(ctx, payload, "", false, false)
}

func setInstantPower(stub map[string]any, key string, power any) {
	if power == nil {
		return
	}
	if m, ok := stub[key].(map[string]any); ok {
		m["instant_power"] = power
	}
}

func (p *PyPowerwallTEDAPI) getAPIMetersAggregates(ctx context.Context, force bool) (any, error) {
	stub := stubs.MetersAggregatesStub()

	// If v1r is active, attempt native call
	if p.v1r != nil {
		res, err := p.v1r.APIGet(ctx, "/api/meters/aggregates")
		if err == nil && res != nil {
			return res, nil
		}
	}

	// Calculate from status
	status := p.client.GetStatus(ctx, force)
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

func (p *PyPowerwallTEDAPI) getAPIOperation(ctx context.Context, force bool) (any, error) {
	cfg := p.client.GetConfig(ctx, force)
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

func (p *PyPowerwallTEDAPI) postAPIOperation(ctx context.Context, payload any, _ string, _, _ bool) (any, error) {
	logger.Load(ctx).DebugContext(ctx, "TEDAPI post operation", "payload", payload)
	p.client.cache.Invalidate("/api/operation")

	return map[string]any{statusKey: statusSuccess}, nil
}

func (p *PyPowerwallTEDAPI) getAPISiteInfo(ctx context.Context, force bool) (any, error) {
	cfg := p.client.GetConfig(ctx, force)
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

func (p *PyPowerwallTEDAPI) getAPISiteMaster(ctx context.Context, force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet(ctx, "/api/sitemaster")
		if err == nil && res != nil {
			return res, nil
		}
	}

	status := p.client.GetStatus(ctx, force)
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

func (p *PyPowerwallTEDAPI) getAPISiteInfoSiteName(ctx context.Context, force bool) (any, error) {
	cfg := p.client.GetConfig(ctx, force)
	if cfg != nil {
		if siteName, ok := cfg[siteNameKey].(string); ok && siteName != "" {
			return map[string]any{siteNameKey: siteName}, nil
		}
	}

	return map[string]any{siteNameKey: defaultSiteName}, nil
}

func (p *PyPowerwallTEDAPI) getAPIStatus(ctx context.Context, force bool) (any, error) {
	cfg := p.client.GetConfig(ctx, force)
	version := p.client.GetFirmwareVersion(ctx, force)
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

func (p *PyPowerwallTEDAPI) getAPISystemStatus(ctx context.Context, force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet(ctx, "/api/system_status")
		if err == nil && res != nil {
			return res, nil
		}
	}
	stub := stubs.SystemStatusStub()
	cfg := p.client.GetConfig(ctx, force)
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
		if s, ok := a.(string); ok {
			if s == "SystemConnectedToGrid" {
				return true
			}

			continue
		}
		if fmt.Sprint(a) == "SystemConnectedToGrid" {
			return true
		}
	}

	return false
}

func (p *PyPowerwallTEDAPI) getAPIGridStatus(ctx context.Context, force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet(ctx, "/api/system_status/grid_status")
		if err == nil && res != nil {
			return res, nil
		}
	}

	status := p.client.GetStatus(ctx, force)
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

func (p *PyPowerwallTEDAPI) getAPISystemStatusSOE(ctx context.Context, force bool) (any, error) {
	if p.v1r != nil {
		res, err := p.v1r.APIGet(ctx, "/api/system_status/soe")
		if err == nil && res != nil {
			return res, nil
		}
	}

	percentage := 100.0
	status := p.client.GetStatus(ctx, force)
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

// pchStringLetters are the PW3 solar string identifiers found in raw
// PCH_Pv* component signals - "PW3 has 6 strings A-F"
// (pypowerwall/tedapi/__init__.py:1030).
//
//nolint:gochecknoglobals // Read-only constant table, not mutated.
var pchStringLetters = []string{"A", "B", "C", "D", "E", "F"}

// pchSignalValue extracts a signal's numeric "value" as a float64, matching
// pypowerwall's own guard (pypowerwall/tedapi/__init__.py:1041-1046): a
// missing, null, or non-positive reading is treated as 0 rather than
// passed through, since a disconnected string reports a nonsensical
// negative or zero raw value.
func pchSignalValue(sig map[string]any) float64 {
	if v, ok := sig["value"].(float64); ok && v > 0 {
		return v
	}

	return 0
}

// scanPCHStringSignals finds string letter n's PCH_PvState_<n>,
// PCH_PvVoltage<n>, and PCH_PvCurrent<n> signals across every "pch"
// component, returning the raw state text (default "Unknown" if never
// found) and clamped voltage/current readings.
func scanPCHStringSignals(pchList []any, n string) (string, float64, float64) {
	pvState := "Unknown"

	var pvVoltage, pvCurrent float64

	for _, compAny := range pchList {
		comp, okComp := compAny.(map[string]any)
		if !okComp {
			continue
		}

		signals, _ := comp["signals"].([]any)
		for _, sigAny := range signals {
			sig, okSig := sigAny.(map[string]any)
			if !okSig {
				continue
			}

			name, _ := sig["name"].(string)
			switch name {
			case "PCH_PvState_" + n:
				if tv, okTV := sig["textValue"].(string); okTV {
					pvState = tv
				}
			case "PCH_PvVoltage" + n:
				pvVoltage = pchSignalValue(sig)
			case "PCH_PvCurrent" + n:
				pvCurrent = pchSignalValue(sig)
			}
		}
	}

	return pvState, pvVoltage, pvCurrent
}

// synthesizeStringVitals maps the raw PCH_PvState_<n>/PCH_PvVoltage<n>/
// PCH_PvCurrent<n> component signals the PW3 components GraphQL query
// returns into the PVAC_PvState_<n>/PVAC_PVMeasuredVoltage_<n>/
// PVAC_PVCurrent_<n>/PVAC_PVMeasuredPower_<n> vitals field names that
// pypowerwall's facade (and gopowerwall's own Powerwall.Strings) expect,
// plus a sibling PVS_String<n>_Connected flag - mirroring
// pypowerwall/tedapi/__init__.py:1032-1069 exactly, including the letter
// range (A-F, not a numeric index) and the "Pv_Active" substring test for
// Connected. It returns nil, nil if components carries no "pch" signal
// data at all (e.g. non-PW3 hardware, or a query the gateway did not
// answer), so callers can skip adding synthesized devices entirely rather
// than fabricating an all-"Unknown"/all-zero PVAC/PVS pair.
func synthesizeStringVitals(components map[string]any) (map[string]any, map[string]any) {
	pchList, ok := lookup.Lookup(components, "components", "pch").([]any)
	if !ok || len(pchList) == 0 {
		return nil, nil
	}

	pvac := make(map[string]any, len(pchStringLetters)*4) //nolint:mnd // 4 vitals keys per string letter
	pvs := make(map[string]any, len(pchStringLetters))

	for _, n := range pchStringLetters {
		pvState, pvVoltage, pvCurrent := scanPCHStringSignals(pchList, n)

		pvac["PVAC_PvState_"+n] = pvState
		pvac["PVAC_PVMeasuredVoltage_"+n] = pvVoltage
		pvac["PVAC_PVCurrent_"+n] = pvCurrent
		pvac["PVAC_PVMeasuredPower_"+n] = pvVoltage * pvCurrent
		pvs["PVS_String"+n+"_Connected"] = strings.Contains(pvState, "Pv_Active")
	}

	return pvac, pvs
}

// fanSpeedSignalValue extracts a signal's numeric "value" as a *float64,
// returning nil when absent or null - mirroring pypowerwall's own
// `signal.get("value") is not None` guard (pypowerwall/tedapi/__init__.py:
// 1895-1897), which passes a found value through untouched rather than
// clamping or zeroing it the way [pchSignalValue] does for solar-string
// readings.
func fanSpeedSignalValue(sig map[string]any) *float64 {
	if v, ok := sig["value"].(float64); ok {
		return &v
	}

	return nil
}

// ExtractFanSpeeds scans data's "components.msa" component list for
// PVAC_Fan_Speed_Actual_RPM/PVAC_Fan_Speed_Target_RPM signals, returning one
// [models.FanSpeedEntry] per component that reported at least one of them,
// keyed by "PVAC--<partNumber>--<serialNumber>" - a byte-for-byte port of
// pypowerwall's extract_fan_speeds (pypowerwall/tedapi/__init__.py:
// 1879-1903), including its ambiguity: the "msa" alias in this project's
// own DeviceControllerQuery text (backend/tedapi/queries/V2026_06.json,
// V2024_06.json) requests only MSA_*/METER_Z_* signal names, never
// PVAC_Fan_Speed_*, and upstream's own query text has the identical gap -
// the fan-speed signals live instead under the same response's
// esCan.bus.PVAC.PVAC_Logging block. Reading them from there instead would
// make this function work where upstream's does not, silently diverging
// from pypowerwall's documented (if seemingly buggy) behavior on real
// hardware. This function therefore faithfully reproduces the ambiguity
// rather than working around it: it is expected to return an empty map
// against real gateways today. Confirming whether get_fan_speeds() ever
// returns real data in practice needs a live V2026_06-firmware gateway, not
// more static analysis - see docs/parity-matrix.md's /fans row.
func ExtractFanSpeeds(data map[string]any) map[string]models.FanSpeedEntry {
	msaList, _ := lookup.Lookup(data, "components", "msa").([]any)
	result := make(map[string]models.FanSpeedEntry, len(msaList))

	for _, compAny := range msaList {
		comp, ok := compAny.(map[string]any)
		if !ok {
			continue
		}

		var entry models.FanSpeedEntry

		signals, _ := comp["signals"].([]any)
		for _, sigAny := range signals {
			sig, okSig := sigAny.(map[string]any)
			if !okSig {
				continue
			}

			switch sig["name"] {
			case "PVAC_Fan_Speed_Actual_RPM":
				entry.ActualRPM = fanSpeedSignalValue(sig)
			case "PVAC_Fan_Speed_Target_RPM":
				entry.TargetRPM = fanSpeedSignalValue(sig)
			}
		}

		if entry.ActualRPM == nil && entry.TargetRPM == nil {
			continue
		}

		partNumber, _ := comp["partNumber"].(string)
		serialNumber, _ := comp["serialNumber"].(string)
		result["PVAC--"+partNumber+"--"+serialNumber] = entry
	}

	return result
}

// GetFanSpeeds returns the raw cooling-fan speed readings reported by the
// gateway's device controller, mirroring pypowerwall's get_fan_speeds
// (pypowerwall/tedapi/__init__.py:1906-1908). See [ExtractFanSpeeds] for the
// upstream ambiguity this faithfully reproduces.
func (p *PyPowerwallTEDAPI) GetFanSpeeds(ctx context.Context, force bool) map[string]models.FanSpeedEntry {
	return ExtractFanSpeeds(p.client.GetDeviceController(ctx, force))
}

// Vitals returns Powerwall vitals in TEDAPI format.
func (p *PyPowerwallTEDAPI) Vitals(ctx context.Context) (map[string]any, error) {
	out := make(map[string]any)

	// Critical invariant (from DESIGN.md):
	// TEDAPI vitals emits TESYNC--None--None and TESLA--None blocks when SYNC bus is absent.
	// TESLA--None componentParentDin (STSTSM--<din>) is where gateway DIN appears in TEDAPI vitals.
	din := p.client.din
	if din == "" {
		cfg := p.client.GetConfig(ctx, false)
		if v, ok := cfg["vin"].(string); ok {
			din = v
		}
	}

	out["TESLA--None"] = map[string]any{
		"componentParentDin": "STSTSM--" + din,
	}
	out["TESYNC--None--None"] = map[string]any{}

	if components := p.client.GetComponents(ctx, false); components != nil {
		if pvac, pvs := synthesizeStringVitals(components); pvac != nil {
			out["PVAC--"+din] = pvac
			out["PVS--"+din] = pvs
		}
	}

	return out, nil
}

// GetTimeRemaining is unsupported in TEDAPI mode without local gateway system status.
func (p *PyPowerwallTEDAPI) GetTimeRemaining(_ context.Context) (*float64, error) {
	return nil, models.ErrUnsupported
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
func (p *PyPowerwallTEDAPI) Power(ctx context.Context) (map[string]float64, error) {
	site, solar, battery, load := 0.0, 0.0, 0.0, 0.0
	payload, err := p.Poll(ctx, "/api/meters/aggregates", false, false, false)
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
func (p *PyPowerwallTEDAPI) FetchPower(ctx context.Context, sensor string, verbose bool) (any, error) {
	if verbose {
		payload, err := p.Poll(ctx, "/api/meters/aggregates", false, false, false)
		if err != nil {
			return nil, err
		}
		if payload != nil {
			return lookup.Lookup(payload, sensor), nil
		}

		return nil, models.ErrNotFound
	}
	power, err := p.Power(ctx)
	if err != nil {
		return 0.0, err
	}

	return power[sensor], nil
}

// ScheduleMaxBackup delegates to v1r if available.
func (p *PyPowerwallTEDAPI) ScheduleMaxBackup(ctx context.Context, durationSeconds int) (map[string]any, error) {
	if p.v1r == nil {
		return nil, models.ErrUnsupported
	}
	ok, err := p.v1r.ScheduleMaxBackup(ctx, durationSeconds)
	if err != nil {
		return nil, err
	}

	return map[string]any{"success": ok}, nil
}

// CancelMaxBackup delegates to v1r if available.
func (p *PyPowerwallTEDAPI) CancelMaxBackup(ctx context.Context) (map[string]any, error) {
	if p.v1r == nil {
		return nil, models.ErrUnsupported
	}
	ok, err := p.v1r.CancelMaxBackup(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{"success": ok}, nil
}

// GetBackupEvents delegates to v1r if available.
func (p *PyPowerwallTEDAPI) GetBackupEvents(ctx context.Context) (map[string]any, error) {
	if p.v1r == nil {
		return nil, models.ErrUnsupported
	}

	return p.v1r.GetBackupEvents(ctx)
}

// GoOffGrid requests intentional islanding through the signed v1r transport.
func (p *PyPowerwallTEDAPI) GoOffGrid(ctx context.Context) (models.Operation, error) {
	if p.v1r == nil {
		return models.Operation{}, models.ErrUnsupported
	}
	din, _ := p.v1r.GetDin(ctx)
	if din == "" {
		din = p.client.din
	}
	if din == "" {
		cfg := p.client.GetConfig(ctx, false)
		if v, ok := cfg["vin"].(string); ok {
			din = v
		}
	}
	if din == "" {
		return models.Operation{}, models.ErrNotFound
	}

	_, err := p.v1r.SendIslandMode(ctx, din, IslandModeOffGrid, true)
	if err != nil {
		return models.Operation{}, err
	}

	return models.Operation{}, nil
}

// ReconnectGrid requests grid reconnection through the signed v1r transport.
func (p *PyPowerwallTEDAPI) ReconnectGrid(ctx context.Context) (models.Operation, error) {
	if p.v1r == nil {
		return models.Operation{}, models.ErrUnsupported
	}
	din, _ := p.v1r.GetDin(ctx)
	if din == "" {
		din = p.client.din
	}
	if din == "" {
		cfg := p.client.GetConfig(ctx, false)
		if v, ok := cfg["vin"].(string); ok {
			din = v
		}
	}
	if din == "" {
		return models.Operation{}, models.ErrNotFound
	}

	_, err := p.v1r.SendIslandMode(ctx, din, IslandModeReconnect, false)
	if err != nil {
		return models.Operation{}, err
	}

	return models.Operation{}, nil
}
