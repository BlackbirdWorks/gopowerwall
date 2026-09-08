package tedapi_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/tedapi"
	"github.com/blackbirdworks/gopowerwall/models"
	tedapipb "github.com/blackbirdworks/gopowerwall/proto/tedapi"
	"github.com/blackbirdworks/gopowerwall/proto/tedapi/combined"
	"github.com/blackbirdworks/gopowerwall/proto/teslapower"
)

const (
	clientTimeout  = 5 * time.Second
	clientCacheTTL = time.Minute
	clientPoolSize = 2
	testGWPassword = "gwpwd"
)

func newLegacyClient(host string) *tedapi.Client {
	return tedapi.NewClient(
		host, testGWPassword, clientTimeout, clientCacheTTL, clientPoolSize,
		models.TEDAPIVersion2024_06, models.AuthModeCookie,
	)
}

// buildLegacyConfigResponse marshals a legacy WiFi TEDAPI Message carrying a
// config.json payload in its Config.Recv.File.Text field.
func buildLegacyConfigResponse(t *testing.T, configJSON string) []byte {
	t.Helper()

	msg := &tedapipb.Message{
		Message: &tedapipb.MessageEnvelope{
			Config: &tedapipb.ConfigType{
				Config: &tedapipb.ConfigType_Recv{
					Recv: &tedapipb.PayloadConfigRecv{
						File: &tedapipb.ConfigString{Text: configJSON},
					},
				},
			},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	return data
}

// buildLegacyQueryResponse marshals a legacy WiFi TEDAPI Message carrying a
// GraphQL query result in its Payload.Recv.Text field.
func buildLegacyQueryResponse(t *testing.T, jsonText string) []byte {
	t.Helper()

	msg := &tedapipb.Message{
		Message: &tedapipb.MessageEnvelope{
			Payload: &tedapipb.QueryType{
				Recv: &tedapipb.PayloadString{Text: jsonText},
			},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	return data
}

// newLegacyTEDAPIServer serves both config.json reads and GraphQL query
// requests over the legacy /tedapi/v1 WiFi protocol.
// readLegacyMessage decodes the request body as a legacy WiFi TEDAPI Message
// and returns its envelope. Kept out of handler literals since testifylint
// forbids calling require inside an http.HandlerFunc body directly.
func readLegacyMessage(t *testing.T, r *http.Request) *tedapipb.MessageEnvelope {
	t.Helper()

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	var msg tedapipb.Message
	require.NoError(t, proto.Unmarshal(body, &msg))

	return msg.GetMessage()
}

func newLegacyTEDAPIServer(t *testing.T, configJSON, queryJSON string) (string, *int) {
	t.Helper()

	count := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		count++
		env := readLegacyMessage(t, r)

		switch {
		case env.GetConfig() != nil:
			_, _ = w.Write(buildLegacyConfigResponse(t, configJSON))
		case env.GetPayload() != nil:
			_, _ = w.Write(buildLegacyQueryResponse(t, queryJSON))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	srv := newTLSServer(t, handler)

	return hostOf(srv), &count
}

func TestNewClientDefaultsHost(t *testing.T) {
	t.Parallel()

	const shortTimeout = 300 * time.Millisecond
	c := tedapi.NewClient(
		"", testGWPassword, shortTimeout, clientCacheTTL, clientPoolSize,
		models.TEDAPIVersion2024_06, models.AuthModeCookie,
	)
	require.NotNil(t, c)
	assert.False(t, c.Connect(t.Context()), "an unreachable default host should fail to connect")
}

func TestClientConnectLegacy(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"vin":"abc"}`, `{}`)
		c := newLegacyClient(host)
		assert.True(t, c.Connect(t.Context()))
	})

	t.Run("failure when unreachable", func(t *testing.T) {
		t.Parallel()

		c := newLegacyClient("127.0.0.1:1")
		assert.False(t, c.Connect(t.Context()))
	})
}

func TestClientConnectV1r(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			}
		}
		srv := newTLSServer(t, handler)
		v1r := newV1r(t, hostOf(srv))
		c := newLegacyClient(hostOf(srv))
		c.SetV1rTransport(v1r)

		assert.True(t, c.Connect(t.Context()))
	})

	t.Run("login failure", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}
		srv := newTLSServer(t, handler)
		v1r := newV1r(t, hostOf(srv))
		c := newLegacyClient(hostOf(srv))
		c.SetV1rTransport(v1r)

		assert.False(t, c.Connect(t.Context()))
	})

	t.Run("get_din failure", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
		srv := newTLSServer(t, handler)
		v1r := newV1r(t, hostOf(srv))
		c := newLegacyClient(hostOf(srv))
		c.SetV1rTransport(v1r)

		assert.False(t, c.Connect(t.Context()))
	})
}

func TestPostTEDAPI(t *testing.T) {
	t.Parallel()

	t.Run("plain body is returned as-is", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("plain-response"))
		}
		host := hostOf(newTLSServer(t, handler))
		c := newLegacyClient(host)

		out, err := c.PostTEDAPI(t.Context(), []byte("req"))
		require.NoError(t, err)
		assert.Equal(t, []byte("plain-response"), out)
	})

	t.Run("gzipped body is transparently decompressed", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, err := gz.Write([]byte("decompressed-content"))
		require.NoError(t, err)
		require.NoError(t, gz.Close())

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(buf.Bytes())
		}
		host := hostOf(newTLSServer(t, handler))
		c := newLegacyClient(host)

		out, err := c.PostTEDAPI(t.Context(), []byte("req"))
		require.NoError(t, err)
		assert.Equal(t, []byte("decompressed-content"), out)
	})

	t.Run("non-200 status returns ErrUnexpectedStatus", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		host := hostOf(newTLSServer(t, handler))
		c := newLegacyClient(host)

		_, err := c.PostTEDAPI(t.Context(), []byte("req"))
		require.ErrorIs(t, err, backend.ErrUnexpectedStatus)
	})

	t.Run("context cancellation aborts the request", func(t *testing.T) {
		t.Parallel()

		c := newLegacyClient("127.0.0.1:1")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := c.PostTEDAPI(ctx, []byte("req"))
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestClientGetConfigLegacy(t *testing.T) {
	t.Parallel()

	t.Run("success caches the parsed config", func(t *testing.T) {
		t.Parallel()

		host, calls := newLegacyTEDAPIServer(t, `{"vin":"VIN123","site_info":{"real_mode":"backup"}}`, `{}`)
		c := newLegacyClient(host)

		cfg := c.GetConfig(t.Context(), false)
		require.NotNil(t, cfg)
		assert.Equal(t, "VIN123", cfg["vin"])

		cfg2 := c.GetConfig(t.Context(), false)
		assert.Equal(t, cfg, cfg2)
		assert.Equal(t, 1, *calls, "the second call should be served from cache")

		cfg3 := c.GetConfig(t.Context(), true)
		require.NotNil(t, cfg3)
		assert.Equal(t, 2, *calls, "force=true should bypass the cache")
	})

	t.Run("malformed config JSON returns nil and is not cached", func(t *testing.T) {
		t.Parallel()

		host, calls := newLegacyTEDAPIServer(t, `not-json`, `{}`)
		c := newLegacyClient(host)

		assert.Nil(t, c.GetConfig(t.Context(), false))
		assert.Nil(t, c.GetConfig(t.Context(), false))
		assert.Equal(t, 2, *calls, "failures should never populate the cache")
	})

	t.Run("unreachable host returns nil", func(t *testing.T) {
		t.Parallel()

		c := newLegacyClient("127.0.0.1:1")
		assert.Nil(t, c.GetConfig(t.Context(), false))
	})
}

func TestClientGetConfigV1r(t *testing.T) {
	t.Parallel()

	t.Run("success reads config via the FileStore API", func(t *testing.T) {
		t.Parallel()

		configJSON := []byte(`{"vin":"V1RVIN","site_info":{"real_mode":"self_consumption"}}`)
		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/tedapi/v1r":
				env := readRoutableEnvelope(t, r)
				assert.NotNil(t, env.GetFilestore().GetReadFileRequest())
				writeRoutableEnvelope(t, w, &combined.MessageEnvelope{
					Payload: &combined.MessageEnvelope_Filestore{
						Filestore: &combined.FileStoreMessages{
							Message: &combined.FileStoreMessages_ReadFileResponse{
								ReadFileResponse: &combined.FileStoreAPIReadFileResponse{
									File: &combined.FileStoreAPIFile{
										Content: &combined.FileStoreAPIFile_Blob{Blob: configJSON},
									},
									Hash: []byte{0x01, 0x02},
								},
							},
						},
					},
				})
			}
		}
		srv := newTLSServer(t, handler)
		v1r := newV1r(t, hostOf(srv))
		c := newLegacyClient(hostOf(srv))
		c.SetV1rTransport(v1r)

		cfg := c.GetConfig(t.Context(), false)
		require.NotNil(t, cfg)
		assert.Equal(t, "V1RVIN", cfg["vin"])
	})

	t.Run("get_din failure falls back to the legacy WiFi path", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				w.WriteHeader(http.StatusInternalServerError)
			case "/tedapi/v1":
				if readLegacyMessage(t, r).GetConfig() != nil {
					_, _ = w.Write(buildLegacyConfigResponse(t, `{"vin":"LEGACY"}`))
				}
			}
		}
		srv := newTLSServer(t, handler)
		v1r := newV1r(t, hostOf(srv))
		c := newLegacyClient(hostOf(srv))
		c.SetV1rTransport(v1r)

		cfg := c.GetConfig(t.Context(), false)
		require.NotNil(t, cfg)
		assert.Equal(t, "LEGACY", cfg["vin"])
	})

	t.Run("malformed FileStore response falls back to the legacy WiFi path", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/tedapi/v1r":
				writeRoutableEnvelope(t, w, &combined.MessageEnvelope{})
			case "/tedapi/v1":
				if readLegacyMessage(t, r).GetConfig() != nil {
					_, _ = w.Write(buildLegacyConfigResponse(t, `{"vin":"FALLBACK"}`))
				}
			}
		}
		srv := newTLSServer(t, handler)
		v1r := newV1r(t, hostOf(srv))
		c := newLegacyClient(hostOf(srv))
		c.SetV1rTransport(v1r)

		cfg := c.GetConfig(t.Context(), false)
		require.NotNil(t, cfg)
		assert.Equal(t, "FALLBACK", cfg["vin"])
	})
}

func TestClientGetStatus(t *testing.T) {
	t.Parallel()

	t.Run("legacy GraphQL query is cached", func(t *testing.T) {
		t.Parallel()

		host, calls := newLegacyTEDAPIServer(t, `{}`, `{"control":{"soe":42.5}}`)
		c := newLegacyClient(host)

		status := c.GetStatus(t.Context(), false)
		require.NotNil(t, status)
		control, ok := status["control"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 42.5, control["soe"], 0.001)

		status2 := c.GetStatus(t.Context(), false)
		assert.Equal(t, status, status2)
		assert.Equal(t, 1, *calls)

		status3 := c.GetStatus(t.Context(), true)
		require.NotNil(t, status3)
		assert.Equal(t, 2, *calls)
	})

	t.Run("v1r path delegates to APIGet", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/api/system_status":
				_, _ = w.Write([]byte(`{"running":true}`))
			}
		}
		srv := newTLSServer(t, handler)
		v1r := newV1r(t, hostOf(srv))
		c := newLegacyClient(hostOf(srv))
		c.SetV1rTransport(v1r)
		require.True(t, c.Connect(t.Context()), "Connect must resolve the DIN before execGraphQL can use the v1r path")

		status := c.GetStatus(t.Context(), false)
		require.NotNil(t, status)
		assert.Equal(t, true, status["running"])
	})

	t.Run("unreachable host returns nil", func(t *testing.T) {
		t.Parallel()

		c := newLegacyClient("127.0.0.1:1")
		assert.Nil(t, c.GetStatus(t.Context(), false))
	})
}

func TestClientGetFirmwareVersion(t *testing.T) {
	t.Parallel()

	t.Run("returns the version from config", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"version":"24.4.0"}`, `{}`)
		c := newLegacyClient(host)

		assert.Equal(t, "24.4.0", c.GetFirmwareVersion(t.Context(), false))
	})

	t.Run("returns unknown when config is unreachable", func(t *testing.T) {
		t.Parallel()

		c := newLegacyClient("127.0.0.1:1")
		assert.Equal(t, "unknown", c.GetFirmwareVersion(t.Context(), false))
	})

	t.Run("returns unknown when version field is absent", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"vin":"x"}`, `{}`)
		c := newLegacyClient(host)

		assert.Equal(t, "unknown", c.GetFirmwareVersion(t.Context(), false))
	})
}

func newLegacyBackend(host string) *tedapi.PyPowerwallTEDAPI {
	client := newLegacyClient(host)

	return tedapi.NewBackend(client, nil)
}

func newV1rBackend(t *testing.T, host string) *tedapi.PyPowerwallTEDAPI {
	t.Helper()

	v1r := newV1r(t, host)
	client := newLegacyClient(host)
	client.SetV1rTransport(v1r)

	return tedapi.NewBackend(client, v1r)
}

func TestBackendAuthenticate(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"vin":"abc"}`, `{}`)
		p := newLegacyBackend(host)
		require.NoError(t, p.Authenticate(t.Context()))
	})

	t.Run("failure returns ErrLogin", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")
		require.ErrorIs(t, p.Authenticate(t.Context()), backend.ErrLogin)
	})
}

func TestBackendClose(t *testing.T) {
	t.Parallel()

	p := newLegacyBackend("127.0.0.1:1")
	assert.NoError(t, p.Close(t.Context()))
}

func TestBackendUnknownAPIDispatch(t *testing.T) {
	t.Parallel()

	p := newLegacyBackend("127.0.0.1:1")

	pollRes, err := p.Poll(t.Context(), "/api/does-not-exist", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ERROR": "Unknown API: /api/does-not-exist"}, pollRes)

	postRes, err := p.Post(t.Context(), "/api/does-not-exist", nil, "", false, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"ERROR": "Unknown API: /api/does-not-exist"}, postRes)
}

func TestBackendKnownStubEndpoints(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		api  string
	}

	cases := []testCase{
		{name: "login", api: "/api/login/Basic"},
		{name: "logout", api: "/api/logout"},
		{name: "powerwalls", api: "/api/powerwalls"},
		{name: "meters site", api: "/api/meters/site"},
		{name: "meters", api: "/api/meters"},
		{name: "customer", api: "/api/customer"},
		{name: "installer", api: "/api/installer"},
		{name: "networks", api: "/api/networks"},
		{name: "auth toggle supported", api: "/api/auth/toggle/supported"},
		{name: "system update status", api: "/api/system/update/status"},
		{name: "solars", api: "/api/solars"},
	}

	p := newLegacyBackend("127.0.0.1:1")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := p.Poll(t.Context(), tc.api, false, false, false)
			require.NoError(t, err)
			assert.NotNil(t, res)
		})
	}
}

func TestBackendGetAPIMetersAggregates(t *testing.T) {
	t.Parallel()

	t.Run("v1r path returns the native response directly", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/api/meters/aggregates":
				_, _ = w.Write([]byte(`{"site":{"instant_power":123}}`))
			}
		}
		srv := newTLSServer(t, handler)
		p := newV1rBackend(t, hostOf(srv))

		res, err := p.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		site, ok := m["site"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 123.0, site["instant_power"], 0.001)
	})

	t.Run("v1r failure falls back to the status-derived stub", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/api/meters/aggregates":
				w.WriteHeader(http.StatusInternalServerError)
			case "/tedapi/v1":
				if readLegacyMessage(t, r).GetPayload() != nil {
					_, _ = w.Write(buildLegacyQueryResponse(
						t, `{"meters":{"site":{"instant_power":55},"solar":{"instant_power":10},`+
							`"battery":{"instant_power":-5},"load":{"instant_power":60}}}`,
					))
				}
			}
		}
		srv := newTLSServer(t, handler)
		p := newV1rBackend(t, hostOf(srv))

		res, err := p.Poll(t.Context(), "/api/meters/aggregates", true, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		site, ok := m["site"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 55.0, site["instant_power"], 0.001)
	})

	t.Run("no v1r computes from the legacy GraphQL status", func(t *testing.T) {
		t.Parallel()

		queryJSON := `{"meters":{"site":{"instant_power":1},"solar":{"instant_power":2},` +
			`"battery":{"instant_power":3},"load":{"instant_power":4}}}`
		host, _ := newLegacyTEDAPIServer(t, `{}`, queryJSON)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		load, ok := m["load"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 4.0, load["instant_power"], 0.001)
	})

	t.Run("no status available returns the zeroed stub", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")

		res, err := p.Poll(t.Context(), "/api/meters/aggregates", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		site, ok := m["site"].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 0.0, site["instant_power"], 0.001)
	})
}

func TestBackendOperation(t *testing.T) {
	t.Parallel()

	t.Run("reads backup reserve and mode from config", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(
			t, `{"site_info":{"backup_reserve_percent":42,"real_mode":"backup"}}`, `{}`,
		)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/operation", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 42.0, m["backup_reserve_percent"], 0.001)
		assert.Equal(t, "backup", m["real_mode"])
	})

	t.Run("defaults are used when config is unavailable", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")

		res, err := p.Poll(t.Context(), "/api/operation", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 20.0, m["backup_reserve_percent"], 0.001)
		assert.Equal(t, "self_consumption", m["real_mode"])
	})

	t.Run("post succeeds and invalidates the cache", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")

		res, err := p.Post(t.Context(), "/api/operation", map[string]any{"real_mode": "backup"}, "", false, false)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"status": "success"}, res)
	})
}

func TestBackendSiteInfo(t *testing.T) {
	t.Parallel()

	t.Run("returns site_info from config", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"site_info":{"site_name":"MyHouse"}}`, `{}`)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/site_info", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "MyHouse", m["site_name"])
	})

	t.Run("falls back to defaults when config is unavailable", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")

		res, err := p.Poll(t.Context(), "/api/site_info", false, false, false)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"site_name": "Powerwall", "timezone": "America/Los_Angeles"}, res)

		nameRes, err := p.Poll(t.Context(), "/api/site_info/site_name", false, false, false)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"site_name": "Powerwall"}, nameRes)
	})

	t.Run("site name reads the top-level site_name field", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"site_name":"TopLevelName"}`, `{}`)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/site_info/site_name", false, false, false)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"site_name": "TopLevelName"}, res)
	})
}

func TestBackendSiteMaster(t *testing.T) {
	t.Parallel()

	t.Run("v1r path returns the native response", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/api/sitemaster":
				_, _ = w.Write([]byte(`{"status":"StatusUp","running":true}`))
			}
		}
		srv := newTLSServer(t, handler)
		p := newV1rBackend(t, hostOf(srv))

		res, err := p.Poll(t.Context(), "/api/sitemaster", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "StatusUp", m["status"])
	})

	t.Run("legacy status reports running state", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{}`, `{"running":true}`)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/sitemaster", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, true, m["running"])
		assert.Equal(t, "success", m["status"])
	})

	t.Run("falls back to the canned stub when status is unavailable", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")

		res, err := p.Poll(t.Context(), "/api/sitemaster", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "StatusUp", m["status"])
	})
}

func TestBackendStatus(t *testing.T) {
	t.Parallel()

	t.Run("din falls back to config vin when client din is unset", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"vin":"VIN-1","version":"24.4.0"}`, `{}`)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "VIN-1", m["din"])
		assert.Equal(t, "24.4.0", m["version"])
	})
}

func TestBackendSystemStatus(t *testing.T) {
	t.Parallel()

	t.Run("v1r path returns the native response", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/api/system_status":
				_, _ = w.Write([]byte(`{"command_source":"native"}`))
			}
		}
		srv := newTLSServer(t, handler)
		p := newV1rBackend(t, hostOf(srv))

		res, err := p.Poll(t.Context(), "/api/system_status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "native", m["command_source"])
	})

	t.Run("legacy config vin populates a synthetic battery block", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"vin":"VIN-2"}`, `{}`)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/system_status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		blocks, ok := m["battery_blocks"].([]any)
		require.True(t, ok)
		require.Len(t, blocks, 1)
		block, ok := blocks[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "VIN-2", block["PackageSerialNumber"])
	})
}

func TestBackendGridStatus(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name           string
		queryJSON      string
		wantGridStatus string
	}

	cases := []testCase{
		{
			name:           "SystemConnectedToGrid alert forces grid connected",
			queryJSON:      `{"control":{"alerts":{"active":["SystemConnectedToGrid"]}}}`,
			wantGridStatus: "SystemGridConnected",
		},
		{
			name: "ISLAND_GridConnected_Connected state is grid connected",
			queryJSON: `{"esCan":{"bus":{"ISLANDER":{"ISLAND_GridConnection":` +
				`{"ISLAND_GridConnected":"ISLAND_GridConnected_Connected"}}}}}`,
			wantGridStatus: "SystemGridConnected",
		},
		{
			name: "any other island state is islanded",
			queryJSON: `{"esCan":{"bus":{"ISLANDER":{"ISLAND_GridConnection":` +
				`{"ISLAND_GridConnected":"ISLAND_GridConnected_Disconnected"}}}}}`,
			wantGridStatus: "SystemIslandedActive",
		},
		{
			name:           "no status available defaults to grid connected",
			queryJSON:      "",
			wantGridStatus: "SystemGridConnected",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var p *tedapi.PyPowerwallTEDAPI
			if tc.queryJSON == "" {
				p = newLegacyBackend("127.0.0.1:1")
			} else {
				host, _ := newLegacyTEDAPIServer(t, `{}`, tc.queryJSON)
				p = newLegacyBackend(host)
			}

			res, err := p.Poll(t.Context(), "/api/system_status/grid_status", false, false, false)
			require.NoError(t, err)
			m, ok := res.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.wantGridStatus, m["grid_status"])
		})
	}

	t.Run("v1r path returns the native response", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/api/system_status/grid_status":
				_, _ = w.Write([]byte(`{"grid_status":"SystemIslandedActive"}`))
			}
		}
		srv := newTLSServer(t, handler)
		p := newV1rBackend(t, hostOf(srv))

		res, err := p.Poll(t.Context(), "/api/system_status/grid_status", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "SystemIslandedActive", m["grid_status"])
	})
}

func TestBackendSystemStatusSOE(t *testing.T) {
	t.Parallel()

	t.Run("v1r path returns the native response", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok"}`))
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/api/system_status/soe":
				_, _ = w.Write([]byte(`{"percentage":77}`))
			}
		}
		srv := newTLSServer(t, handler)
		p := newV1rBackend(t, hostOf(srv))

		res, err := p.Poll(t.Context(), "/api/system_status/soe", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 77.0, m["percentage"], 0.001)
	})

	t.Run("legacy status reports the SOE percentage", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{}`, `{"control":{"soe":33.5}}`)
		p := newLegacyBackend(host)

		res, err := p.Poll(t.Context(), "/api/system_status/soe", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 33.5, m["percentage"], 0.001)
	})

	t.Run("defaults to 100 when status is unavailable", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")

		res, err := p.Poll(t.Context(), "/api/system_status/soe", false, false, false)
		require.NoError(t, err)
		m, ok := res.(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, 100.0, m["percentage"], 0.001)
	})
}

func TestBackendVitals(t *testing.T) {
	t.Parallel()

	t.Run("din falls back to config vin", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"vin":"VIN-3"}`, `{}`)
		p := newLegacyBackend(host)

		out, err := p.Vitals(t.Context())
		require.NoError(t, err)
		tesla, ok := out["TESLA--None"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "STSTSM--VIN-3", tesla["componentParentDin"])
		assert.Contains(t, out, "TESYNC--None--None")
	})

	// This is the regression test for the TEDAPI vitals synthesis bug: the
	// PW3Query/ComponentsQuery GraphQL query already requests the raw
	// PCH_PvState_<n>/PCH_PvVoltage<n>/PCH_PvCurrent<n> component signals
	// (backend/tedapi/queries/V2024_06.json and V2026_06.json), but nothing
	// in backend/tedapi ever parsed the response into the PVAC_PvState_<n>/
	// PVAC_PVMeasuredVoltage_<n>/PVAC_PVCurrent_<n>/PVAC_PVMeasuredPower_<n>
	// field names pypowerwall's facade (and Powerwall.Strings) expect - see
	// pypowerwall/tedapi/__init__.py:1032-1069. Before the fix, Vitals never
	// added a "PVAC--"/"PVS--" device at all in TEDAPI mode, so /strings
	// read all-zero in every mode that went through TEDAPI.
	t.Run("synthesizes PVAC/PVS string vitals from raw PCH component signals", func(t *testing.T) {
		t.Parallel()

		componentsJSON := `{
			"components": {
				"pch": [
					{
						"signals": [
							{"name": "PCH_PvState_A", "textValue": "Pv_Active"},
							{"name": "PCH_PvVoltageA", "value": 385.2},
							{"name": "PCH_PvCurrentA", "value": 12.5},
							{"name": "PCH_PvState_B", "textValue": "Pv_Standby"},
							{"name": "PCH_PvVoltageB", "value": -1.0},
							{"name": "PCH_PvCurrentB", "value": 0.0},
							{"name": "PCH_PvState_F", "textValue": "Pv_Active_Parallel"},
							{"name": "PCH_PvVoltageF", "value": 402.1},
							{"name": "PCH_PvCurrentF", "value": 9.75}
						]
					}
				]
			}
		}`
		host, _ := newLegacyTEDAPIServer(t, `{"vin":"VIN-9"}`, componentsJSON)
		p := newLegacyBackend(host)

		out, err := p.Vitals(t.Context())
		require.NoError(t, err)

		pvac, ok := out["PVAC--VIN-9"].(map[string]any)
		require.True(t, ok, "expected a synthesized PVAC device, got %v", out)
		pvs, ok := out["PVS--VIN-9"].(map[string]any)
		require.True(t, ok, "expected a synthesized PVS device, got %v", out)

		assert.Equal(t, "Pv_Active", pvac["PVAC_PvState_A"])
		assert.InDelta(t, 385.2, pvac["PVAC_PVMeasuredVoltage_A"], 0.001)
		assert.InDelta(t, 12.5, pvac["PVAC_PVCurrent_A"], 0.001)
		assert.InDelta(t, 385.2*12.5, pvac["PVAC_PVMeasuredPower_A"], 0.001)
		assert.Equal(t, true, pvs["PVS_StringA_Connected"])

		// A negative reading is clamped to 0, matching
		// pypowerwall/tedapi/__init__.py:1041-1046's own guard, and a
		// "Standby" state (no "Pv_Active" substring) is not Connected.
		assert.Equal(t, "Pv_Standby", pvac["PVAC_PvState_B"])
		assert.InDelta(t, 0.0, pvac["PVAC_PVMeasuredVoltage_B"], 0.001)
		assert.InDelta(t, 0.0, pvac["PVAC_PVMeasuredPower_B"], 0.001)
		assert.Equal(t, false, pvs["PVS_StringB_Connected"])

		// A letter with no matching signal at all still gets an entry,
		// defaulting to "Unknown"/0/not-connected (PW3 always reports A-F).
		assert.Equal(t, "Unknown", pvac["PVAC_PvState_C"])
		assert.InDelta(t, 0.0, pvac["PVAC_PVMeasuredVoltage_C"], 0.001)
		assert.Equal(t, false, pvs["PVS_StringC_Connected"])

		// The letter range is A-F (PW3's six strings), not A-D.
		assert.Equal(t, "Pv_Active_Parallel", pvac["PVAC_PvState_F"])
		assert.InDelta(t, 402.1, pvac["PVAC_PVMeasuredVoltage_F"], 0.001)
		assert.InDelta(t, 9.75, pvac["PVAC_PVCurrent_F"], 0.001)
		assert.Equal(t, true, pvs["PVS_StringF_Connected"])
	})

	t.Run("no PCH component data means no synthesized PVAC/PVS devices", func(t *testing.T) {
		t.Parallel()

		host, _ := newLegacyTEDAPIServer(t, `{"vin":"VIN-10"}`, `{}`)
		p := newLegacyBackend(host)

		out, err := p.Vitals(t.Context())
		require.NoError(t, err)
		assert.NotContains(t, out, "PVAC--VIN-10")
		assert.NotContains(t, out, "PVS--VIN-10")
	})
}

func TestBackendGetTimeRemainingUnsupported(t *testing.T) {
	t.Parallel()

	p := newLegacyBackend("127.0.0.1:1")
	_, err := p.GetTimeRemaining(t.Context())
	require.ErrorIs(t, err, backend.ErrUnsupported)
}

func TestBackendPowerAndFetchPower(t *testing.T) {
	t.Parallel()

	queryJSON := `{"meters":{"site":{"instant_power":10},"solar":{"instant_power":20},` +
		`"battery":{"instant_power":30},"load":{"instant_power":40}}}`
	host, _ := newLegacyTEDAPIServer(t, `{}`, queryJSON)
	p := newLegacyBackend(host)

	power, err := p.Power(t.Context())
	require.NoError(t, err)
	assert.InDelta(t, 40.0, power["load"], 0.001)

	verbose, err := p.FetchPower(t.Context(), "solar", true)
	require.NoError(t, err)
	m, ok := verbose.(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 20.0, m["instant_power"], 0.001)

	single, err := p.FetchPower(t.Context(), "solar", false)
	require.NoError(t, err)
	assert.InDelta(t, 20.0, single, 0.001)
}

func TestBackendMaxBackupDelegation(t *testing.T) {
	t.Parallel()

	t.Run("no v1r returns ErrUnsupported for all delegated operations", func(t *testing.T) {
		t.Parallel()

		p := newLegacyBackend("127.0.0.1:1")

		_, err := p.ScheduleMaxBackup(t.Context(), 120)
		require.ErrorIs(t, err, backend.ErrUnsupported)

		_, err = p.CancelMaxBackup(t.Context())
		require.ErrorIs(t, err, backend.ErrUnsupported)

		_, err = p.GetBackupEvents(t.Context())
		require.ErrorIs(t, err, backend.ErrUnsupported)
	})

	t.Run("with v1r, operations are delegated and succeed", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din-1"))
			case "/tedapi/v1r":
				env := readRoutableEnvelope(t, r)
				teg := env.GetTeg()
				switch {
				case teg.GetCancelManualBackupEventRequest() != nil:
					writeRoutableEnvelope(t, w, &combined.MessageEnvelope{
						Payload: &combined.MessageEnvelope_Teg{Teg: &combined.TEGMessages{
							Message: &combined.TEGMessages_CancelManualBackupEventResponse{
								CancelManualBackupEventResponse: &combined.TEGAPICancelManualBackupEventResponse{},
							},
						}},
					})
				case teg.GetScheduleManualBackupEventRequest() != nil:
					writeRoutableEnvelope(t, w, &combined.MessageEnvelope{
						Payload: &combined.MessageEnvelope_Teg{Teg: &combined.TEGMessages{
							Message: &combined.TEGMessages_ScheduleManualBackupEventResponse{
								ScheduleManualBackupEventResponse: &combined.TEGAPIScheduleManualBackupEventResponse{},
							},
						}},
					})
				case teg.GetGetBackupEventsRequest() != nil:
					writeRoutableEnvelope(t, w, &combined.MessageEnvelope{
						Payload: &combined.MessageEnvelope_Teg{Teg: &combined.TEGMessages{
							Message: &combined.TEGMessages_GetBackupEventsResponse{
								GetBackupEventsResponse: &combined.TEGAPIGetBackupEventsResponse{},
							},
						}},
					})
				}
			}
		}
		srv := newTLSServer(t, handler)
		p := newV1rBackend(t, hostOf(srv))

		schedRes, err := p.ScheduleMaxBackup(t.Context(), 120)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"success": true}, schedRes)

		cancelRes, err := p.CancelMaxBackup(t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"success": true}, cancelRes)

		events, err := p.GetBackupEvents(t.Context())
		require.NoError(t, err)
		assert.Contains(t, events, "backup_events")
	})
}

func TestBackendGoOffGridAndReconnectGrid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErrIs error
		name      string
		action    string
		useV1r    bool
		hasDin    bool
		serverErr bool
		wantErr   bool
	}{
		{
			name:      "off-grid unsupported without v1r",
			action:    "off-grid",
			useV1r:    false,
			wantErr:   true,
			wantErrIs: backend.ErrUnsupported,
		},
		{
			name:      "reconnect unsupported without v1r",
			action:    "reconnect",
			useV1r:    false,
			wantErr:   true,
			wantErrIs: backend.ErrUnsupported,
		},
		{
			name:      "off-grid fails when din is missing",
			action:    "off-grid",
			useV1r:    true,
			hasDin:    false,
			wantErr:   true,
			wantErrIs: backend.ErrNotFound,
		},
		{
			name:      "reconnect fails when din is missing",
			action:    "reconnect",
			useV1r:    true,
			hasDin:    false,
			wantErr:   true,
			wantErrIs: backend.ErrNotFound,
		},
		{
			name:    "off-grid success with v1r",
			action:  "off-grid",
			useV1r:  true,
			hasDin:  true,
			wantErr: false,
		},
		{
			name:    "reconnect success with v1r",
			action:  "reconnect",
			useV1r:  true,
			hasDin:  true,
			wantErr: false,
		},
		{
			name:      "off-grid server error returns error",
			action:    "off-grid",
			useV1r:    true,
			hasDin:    true,
			serverErr: true,
			wantErr:   true,
		},
		{
			name:      "reconnect server error returns error",
			action:    "reconnect",
			useV1r:    true,
			hasDin:    true,
			serverErr: true,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !tc.useV1r {
				p := newLegacyBackend("127.0.0.1:1")
				var err error
				if tc.action == "off-grid" {
					_, err = p.GoOffGrid(t.Context())
				} else {
					_, err = p.ReconnectGrid(t.Context())
				}
				require.ErrorIs(t, err, tc.wantErrIs)

				return
			}

			handler := func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/login/Basic":
					_, _ = w.Write([]byte(`{"token":"tok"}`))
				case "/tedapi/din":
					if tc.hasDin {
						_, _ = w.Write([]byte("din-123"))
					} else {
						w.WriteHeader(http.StatusNotFound)
					}
				case "/tedapi/v1r":
					if tc.serverErr {
						w.WriteHeader(http.StatusInternalServerError)

						return
					}
					readRoutableEnvelope(t, r)
					respEnv := &teslapower.MessageEnvelope{
						DeliveryChannel: 1,
						Sender: &teslapower.Participant{
							Id: &teslapower.Participant_Din{Din: "din-123"},
						},
						Payload: &teslapower.MessageEnvelope_Teg{
							Teg: &teslapower.TEGMessages{
								Message: &teslapower.TEGMessages_SetIslandModeResponse{
									SetIslandModeResponse: &teslapower.TEGAPISetIslandModeResponse{
										Result: 1,
									},
								},
							},
						},
					}
					envBytes, err := proto.Marshal(respEnv)
					assert.NoError(t, err)
					routable := &combined.RoutableMessage{
						Payload: &combined.RoutableMessage_ProtobufMessageAsBytes{
							ProtobufMessageAsBytes: envBytes,
						},
					}
					data, err := proto.Marshal(routable)
					assert.NoError(t, err)
					_, _ = w.Write(data)
				}
			}

			srv := newTLSServer(t, handler)
			p := newV1rBackend(t, hostOf(srv))

			var err error
			if tc.action == "off-grid" {
				_, err = p.GoOffGrid(t.Context())
			} else {
				_, err = p.ReconnectGrid(t.Context())
			}

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrIs != nil {
					require.ErrorIs(t, err, tc.wantErrIs)
				}

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestBackendGetConfigWrapper(t *testing.T) {
	t.Parallel()

	host, _ := newLegacyTEDAPIServer(t, `{"vin":"WRAP"}`, `{}`)
	p := newLegacyBackend(host)

	cfg := p.GetConfig(t.Context())
	require.NotNil(t, cfg)
	assert.Equal(t, "WRAP", cfg["vin"])

	cfgForced := p.GetConfig(t.Context(), true)
	require.NotNil(t, cfgForced)
	assert.Equal(t, "WRAP", cfgForced["vin"])
}

func TestBackendFetchPowerVerboseError(t *testing.T) {
	t.Parallel()

	p := newLegacyBackend("127.0.0.1:1")

	// With no reachable status source, the aggregates stub is still returned
	// (never an error), so the verbose branch resolves to nil for an unknown
	// sensor key rather than failing.
	res, err := p.FetchPower(t.Context(), "does-not-exist", true)
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestBackendMaxBackupDelegationErrorPropagation(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tedapi/din":
			_, _ = w.Write([]byte("din-1"))
		case "/tedapi/v1r":
			w.WriteHeader(http.StatusForbidden)
		}
	}
	srv := newTLSServer(t, handler)
	p := newV1rBackend(t, hostOf(srv))

	_, err := p.ScheduleMaxBackup(t.Context(), 120)
	require.Error(t, err)

	_, err = p.CancelMaxBackup(t.Context())
	require.Error(t, err)
}

// TestExtractFanSpeeds is the regression test for /fans and /fans/pw: no
// fan-speed extraction existed anywhere in this package before this change
// (docs/parity-matrix.md's /fans row - confirmed by grep, zero matches for
// "Fan_Speed" under backend/tedapi/*.go). It is a byte-for-byte port of
// pypowerwall's extract_fan_speeds (pypowerwall/tedapi/__init__.py:
// 1879-1903), deliberately including the static ambiguity documented on
// [tedapi.ExtractFanSpeeds]: these cases exercise the function's own
// "components.msa" scanning logic against a synthetic payload, not a claim
// about what a real gateway returns - the last case specifically proves
// that fan-speed signals reported under the sibling
// esCan.bus.PVAC.PVAC_Logging path (where this project's own GraphQL query
// text actually requests them) are *not* found, matching upstream's
// identical gap rather than working around it.
func TestExtractFanSpeeds(t *testing.T) {
	t.Parallel()

	type testCase struct {
		data map[string]any
		want map[string]models.FanSpeedEntry
		name string
	}

	rpm := func(v float64) *float64 { return &v }

	for _, tc := range []testCase{
		{
			name: "nil data yields empty map",
			data: nil,
			want: map[string]models.FanSpeedEntry{},
		},
		{
			name: "no components section yields empty map",
			data: map[string]any{"esCan": map[string]any{}},
			want: map[string]models.FanSpeedEntry{},
		},
		{
			name: "msa component reporting both signals",
			data: map[string]any{
				"components": map[string]any{
					"msa": []any{
						map[string]any{
							"partNumber":   "1729100-XX-E",
							"serialNumber": "TG12345",
							"signals": []any{
								map[string]any{"name": "PVAC_Fan_Speed_Actual_RPM", "value": 2500.0},
								map[string]any{"name": "PVAC_Fan_Speed_Target_RPM", "value": 2600.0},
								map[string]any{"name": "MSA_pcbaId", "value": 1.0},
							},
						},
					},
				},
			},
			want: map[string]models.FanSpeedEntry{
				"PVAC--1729100-XX-E--TG12345": {ActualRPM: rpm(2500.0), TargetRPM: rpm(2600.0)},
			},
		},
		{
			name: "msa component reporting only one signal leaves the other nil",
			data: map[string]any{
				"components": map[string]any{
					"msa": []any{
						map[string]any{
							"partNumber":   "PN1",
							"serialNumber": "SN1",
							"signals": []any{
								map[string]any{"name": "PVAC_Fan_Speed_Actual_RPM", "value": 1800.0},
							},
						},
					},
				},
			},
			want: map[string]models.FanSpeedEntry{
				"PVAC--PN1--SN1": {ActualRPM: rpm(1800.0)},
			},
		},
		{
			name: "a null-valued signal is treated as absent, not zero",
			data: map[string]any{
				"components": map[string]any{
					"msa": []any{
						map[string]any{
							"partNumber":   "PN2",
							"serialNumber": "SN2",
							"signals": []any{
								map[string]any{"name": "PVAC_Fan_Speed_Actual_RPM", "value": nil},
								map[string]any{"name": "PVAC_Fan_Speed_Target_RPM", "value": 900.0},
							},
						},
					},
				},
			},
			want: map[string]models.FanSpeedEntry{
				"PVAC--PN2--SN2": {TargetRPM: rpm(900.0)},
			},
		},
		{
			name: "a component reporting neither fan signal is skipped entirely",
			data: map[string]any{
				"components": map[string]any{
					"msa": []any{
						map[string]any{
							"partNumber":   "PN3",
							"serialNumber": "SN3",
							"signals": []any{
								map[string]any{"name": "MSA_pcbaId", "value": 5.0},
							},
						},
					},
				},
			},
			want: map[string]models.FanSpeedEntry{},
		},
		{
			name: "fan signals under esCan.bus.PVAC.PVAC_Logging are not found (upstream's own ambiguity)",
			data: map[string]any{
				"esCan": map[string]any{
					"bus": map[string]any{
						"PVAC": map[string]any{
							"PVAC_Logging": map[string]any{
								"PVAC_Fan_Speed_Actual_RPM": 3000.0,
								"PVAC_Fan_Speed_Target_RPM": 3000.0,
							},
						},
					},
				},
				"components": map[string]any{
					"msa": []any{
						map[string]any{
							"partNumber":   "PN4",
							"serialNumber": "SN4",
							"signals": []any{
								map[string]any{"name": "MSA_pcbaId", "value": 1.0},
							},
						},
					},
				},
			},
			want: map[string]models.FanSpeedEntry{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := tedapi.ExtractFanSpeeds(tc.data)
			require.Len(t, got, len(tc.want))
			for key, wantEntry := range tc.want {
				gotEntry, ok := got[key]
				require.True(t, ok, "missing key %q in %v", key, got)

				if wantEntry.ActualRPM == nil {
					assert.Nil(t, gotEntry.ActualRPM)
				} else {
					require.NotNil(t, gotEntry.ActualRPM)
					assert.InDelta(t, *wantEntry.ActualRPM, *gotEntry.ActualRPM, 0.001)
				}

				if wantEntry.TargetRPM == nil {
					assert.Nil(t, gotEntry.TargetRPM)
				} else {
					require.NotNil(t, gotEntry.TargetRPM)
					assert.InDelta(t, *wantEntry.TargetRPM, *gotEntry.TargetRPM, 0.001)
				}
			}
		})
	}
}

// TestBackendGetFanSpeeds exercises PyPowerwallTEDAPI.GetFanSpeeds end to
// end against a mocked GraphQL response, proving the DeviceControllerFull
// query wiring (Client.GetDeviceController) and ExtractFanSpeeds compose
// correctly.
func TestBackendGetFanSpeeds(t *testing.T) {
	t.Parallel()

	queryJSON := `{
		"components": {
			"msa": [
				{
					"partNumber": "1729100-XX-E",
					"serialNumber": "TG12345",
					"signals": [
						{"name": "PVAC_Fan_Speed_Actual_RPM", "value": 2200.0},
						{"name": "PVAC_Fan_Speed_Target_RPM", "value": 2400.0}
					]
				}
			]
		}
	}`
	host, _ := newLegacyTEDAPIServer(t, `{"vin":"VIN-FAN"}`, queryJSON)
	p := newLegacyBackend(host)

	speeds := p.GetFanSpeeds(t.Context(), false)

	entry, ok := speeds["PVAC--1729100-XX-E--TG12345"]
	require.True(t, ok, "expected a fan speed entry, got %v", speeds)
	require.NotNil(t, entry.ActualRPM)
	require.NotNil(t, entry.TargetRPM)
	assert.InDelta(t, 2200.0, *entry.ActualRPM, 0.001)
	assert.InDelta(t, 2400.0, *entry.TargetRPM, 0.001)
}
