package tedapi_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/blackbirdworks/gopowerwall/backend"
	"github.com/blackbirdworks/gopowerwall/backend/tedapi"
	"github.com/blackbirdworks/gopowerwall/proto/tedapi/combined"
)

const (
	v1rTimeout  = 5 * time.Second
	v1rPoolSize = 2
	rsaKeyBits  = 2048
)

// newTLSServer starts a self-signed TLS test server and registers its cleanup.
func newTLSServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// hostOf returns the host:port of a test server.
func hostOf(srv *httptest.Server) string {
	return srv.Listener.Addr().String()
}

// generateRSAKeyFile writes an RSA private key PEM to a temp file, PKCS1 or
// PKCS8 encoded per pkcs8, and returns its path.
func generateRSAKeyFile(t *testing.T, dir, name string, pkcs8 bool) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	require.NoError(t, err)

	var der []byte
	blockType := "RSA PRIVATE KEY"
	if pkcs8 {
		der, err = x509.MarshalPKCS8PrivateKey(key)
		blockType = "PRIVATE KEY"
	} else {
		der = x509.MarshalPKCS1PrivateKey(key)
	}
	require.NoError(t, err)

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	require.NoError(t, pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}))

	return path
}

func TestNewTEDAPIv1r(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	require.NoError(t, err)
	ecPath := filepath.Join(dir, "ec.pem")
	require.NoError(t, os.WriteFile(ecPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}), 0o600))

	garbagePath := filepath.Join(dir, "garbage.pem")
	require.NoError(t, os.WriteFile(
		garbagePath,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not a key")}),
		0o600,
	))

	notPEMPath := filepath.Join(dir, "notpem.txt")
	require.NoError(t, os.WriteFile(notPEMPath, []byte("this is not PEM data"), 0o600))

	type testCase struct {
		keyPath func() string
		name    string
		wantErr bool
	}

	cases := []testCase{
		{
			name:    "PKCS1 RSA key succeeds",
			keyPath: func() string { return generateRSAKeyFile(t, dir, "pkcs1.pem", false) },
		},
		{
			name:    "PKCS8 RSA key succeeds",
			keyPath: func() string { return generateRSAKeyFile(t, dir, "pkcs8.pem", true) },
		},
		{
			name:    "missing key file",
			keyPath: func() string { return filepath.Join(dir, "does-not-exist.pem") },
			wantErr: true,
		},
		{
			name:    "non-PEM content",
			keyPath: func() string { return notPEMPath },
			wantErr: true,
		},
		{
			name:    "PEM block that is not a valid key",
			keyPath: func() string { return garbagePath },
			wantErr: true,
		},
		{
			name:    "PKCS8 key that is not RSA",
			keyPath: func() string { return ecPath },
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, newErr := tedapi.NewTEDAPIv1r("host", "pwd", tc.keyPath(), v1rTimeout, v1rPoolSize)
			if tc.wantErr {
				require.ErrorIs(t, newErr, backend.ErrRSAKeyParse)
				assert.Nil(t, v)

				return
			}
			require.NoError(t, newErr)
			require.NotNil(t, v)
			assert.NotEmpty(t, v.KeyFingerprint)
		})
	}
}

func newV1r(t *testing.T, host string) *tedapi.TEDAPIv1r {
	t.Helper()

	dir := t.TempDir()
	keyPath := generateRSAKeyFile(t, dir, "key.pem", false)
	v, err := tedapi.NewTEDAPIv1r(host, "gwpass", keyPath, v1rTimeout, v1rPoolSize)
	require.NoError(t, err)

	return v
}

func TestV1rLogin(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs error
		handler   http.HandlerFunc
		name      string
		noServer  bool
		wantErr   bool
	}

	cases := []testCase{
		{
			name: "success",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"token":"abc123"}`))
			},
		},
		{
			name: "non-200 status returns ErrLogin",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErrIs: backend.ErrLogin,
		},
		{
			name: "malformed JSON body returns an error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`not-json`))
			},
			wantErr: true,
		},
		{
			name:     "unreachable host returns an error",
			noServer: true,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			host := "127.0.0.1:1"
			if tc.handler != nil {
				host = hostOf(newTLSServer(t, tc.handler))
			}
			v := newV1r(t, host)
			err := v.Login(t.Context())

			switch {
			case tc.wantErrIs != nil:
				require.ErrorIs(t, err, tc.wantErrIs)
			case tc.wantErr:
				require.Error(t, err)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestV1rGetDin(t *testing.T) {
	t.Parallel()

	t.Run("success returns the trimmed body", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		var gotAuth string
		handler := func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("  1232100-00-E--TG1234567890G1  \n"))
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		din, err := v.GetDin(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "1232100-00-E--TG1234567890G1", din)
		assert.Empty(t, gotAuth)

		// A second call must be served from the cached value.
		din2, err2 := v.GetDin(t.Context())
		require.NoError(t, err2)
		assert.Equal(t, din, din2)
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("sends the bearer token when logged in", func(t *testing.T) {
		t.Parallel()

		var gotAuth string
		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"tok-xyz"}`))
			case "/tedapi/din":
				gotAuth = r.Header.Get("Authorization")
				_, _ = w.Write([]byte("din-value"))
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())
		require.NoError(t, v.Login(t.Context()))

		_, err := v.GetDin(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "Bearer tok-xyz", gotAuth)
	})

	t.Run("non-200 status returns ErrUnexpectedStatus", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.GetDin(t.Context())
		require.ErrorIs(t, err, backend.ErrUnexpectedStatus)
	})

	t.Run("unreachable host returns an error", func(t *testing.T) {
		t.Parallel()

		v := newV1r(t, "127.0.0.1:1")
		_, err := v.GetDin(t.Context())
		require.Error(t, err)
	})
}

func TestV1rBuildTLVPayload(t *testing.T) {
	t.Parallel()

	v := newV1r(t, "127.0.0.1:1")
	inner := []byte{0xAA, 0xBB}
	got := v.BuildTLVPayload("DIN1", 0x01020304, inner)

	want := []byte{
		0x00, 0x01, 0x07, // signature type = RSA(7)
		0x01, 0x01, 0x07, // domain = ENERGY_DEVICE(7)
		0x02, 0x04, 'D', 'I', 'N', '1', // personalization = din
		0x04, 0x04, 0x01, 0x02, 0x03, 0x04, // expires at, big-endian
		0xFF,       // end tag
		0xAA, 0xBB, // inner payload appended
	}
	assert.Equal(t, want, got)
}

func TestV1rSignProducesAVerifiableSignature(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	keyPath := generateRSAKeyFile(t, dir, "key.pem", false)
	v, err := tedapi.NewTEDAPIv1r("host", "pwd", keyPath, v1rTimeout, v1rPoolSize)
	require.NoError(t, err)

	keyBytes, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	block, _ := pem.Decode(keyBytes)
	require.NotNil(t, block)
	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	require.NoError(t, err)

	payload := []byte("some tlv payload")
	sig, err := v.Sign(payload)
	require.NoError(t, err)

	hashed := sha512.Sum512(payload)
	assert.NoError(t, rsa.VerifyPKCS1v15(&privKey.PublicKey, crypto.SHA512, hashed[:], sig))
}

// readRoutableEnvelope decodes the request body as a RoutableMessage and
// returns the embedded MessageEnvelope.
func readRoutableEnvelope(t *testing.T, r *http.Request) *combined.MessageEnvelope {
	t.Helper()

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	var routable combined.RoutableMessage
	require.NoError(t, proto.Unmarshal(body, &routable))
	require.NotNil(t, routable.GetSignatureData())

	var env combined.MessageEnvelope
	require.NoError(t, proto.Unmarshal(routable.GetProtobufMessageAsBytes(), &env))

	return &env
}

func writeRoutableEnvelope(t *testing.T, w http.ResponseWriter, env *combined.MessageEnvelope) {
	t.Helper()

	envBytes, err := proto.Marshal(env)
	require.NoError(t, err)
	routable := &combined.RoutableMessage{
		Payload: &combined.RoutableMessage_ProtobufMessageAsBytes{ProtobufMessageAsBytes: envBytes},
	}
	data, err := proto.Marshal(routable)
	require.NoError(t, err)
	_, _ = w.Write(data)
}

func writeRoutableFault(t *testing.T, w http.ResponseWriter, fault combined.MessageFault_E) {
	t.Helper()

	routable := &combined.RoutableMessage{
		SignedMessageStatus: &combined.MessageStatus{MessageFault: fault},
	}
	data, err := proto.Marshal(routable)
	require.NoError(t, err)
	_, _ = w.Write(data)
}

func TestV1rPostV1r(t *testing.T) {
	t.Parallel()

	t.Run("success returns the inner protobuf bytes", func(t *testing.T) {
		t.Parallel()

		respEnv := &combined.MessageEnvelope{Sender: &combined.Participant{Id: &combined.Participant_Din{Din: "abc"}}}
		handler := func(w http.ResponseWriter, r *http.Request) {
			readRoutableEnvelope(t, r)
			writeRoutableEnvelope(t, w, respEnv)
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		reqEnv := &combined.MessageEnvelope{Sender: &combined.Participant{Id: &combined.Participant_Din{Din: "req"}}}
		reqEnvBytes, err := proto.Marshal(reqEnv)
		require.NoError(t, err)

		out, err := v.PostV1r(t.Context(), reqEnvBytes, "din1")
		require.NoError(t, err)

		var gotEnv combined.MessageEnvelope
		require.NoError(t, proto.Unmarshal(out, &gotEnv))
		assert.Equal(t, "abc", gotEnv.GetSender().GetDin())
	})

	t.Run("401 triggers a re-login and retry", func(t *testing.T) {
		t.Parallel()

		var postCalls atomic.Int32
		respEnv := &combined.MessageEnvelope{}
		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				_, _ = w.Write([]byte(`{"token":"new-token"}`))
			case "/tedapi/v1r":
				n := postCalls.Add(1)
				if n == 1 {
					w.WriteHeader(http.StatusUnauthorized)

					return
				}
				writeRoutableEnvelope(t, w, respEnv)
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.PostV1r(t.Context(), []byte("x"), "din1")
		require.NoError(t, err)
		assert.Equal(t, int32(2), postCalls.Load())
	})

	t.Run("unknown key id fault maps to ErrUnknownKeyID", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			writeRoutableFault(t, w, combined.MessageFault_E_MESSAGEFAULT_ERROR_UNKNOWN_KEY_ID)
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.PostV1r(t.Context(), []byte("x"), "din1")
		require.ErrorIs(t, err, backend.ErrUnknownKeyID)
	})

	t.Run("other fault maps to ErrMessageFault", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			writeRoutableFault(t, w, combined.MessageFault_E_MESSAGEFAULT_ERROR_BUSY)
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.PostV1r(t.Context(), []byte("x"), "din1")
		require.ErrorIs(t, err, backend.ErrMessageFault)
	})

	t.Run("non-200 after failed re-login returns ErrUnexpectedStatus", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/login/Basic":
				w.WriteHeader(http.StatusUnauthorized)
			case "/tedapi/v1r":
				w.WriteHeader(http.StatusForbidden)
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.PostV1r(t.Context(), []byte("x"), "din1")
		require.ErrorIs(t, err, backend.ErrUnexpectedStatus)
	})

	t.Run("malformed response body returns an unmarshal error", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.PostV1r(t.Context(), []byte("x"), "din1")
		require.Error(t, err)
	})

	t.Run("context cancellation aborts the request", func(t *testing.T) {
		t.Parallel()

		v := newV1r(t, "127.0.0.1:1")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := v.PostV1r(ctx, []byte("x"), "din1")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestV1rScheduleAndCancelMaxBackup(t *testing.T) {
	t.Parallel()

	t.Run("schedules successfully and clamps short durations", func(t *testing.T) {
		t.Parallel()

		var lastDuration atomic.Uint32
		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din1"))
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
					duration := teg.GetScheduleManualBackupEventRequest().GetSchedulingInfo().GetDurationSeconds()
					lastDuration.Store(duration)
					writeRoutableEnvelope(t, w, &combined.MessageEnvelope{
						Payload: &combined.MessageEnvelope_Teg{Teg: &combined.TEGMessages{
							Message: &combined.TEGMessages_ScheduleManualBackupEventResponse{
								ScheduleManualBackupEventResponse: &combined.TEGAPIScheduleManualBackupEventResponse{},
							},
						}},
					})
				}
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		ok, err := v.ScheduleMaxBackup(t.Context(), 5)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, uint32(60), lastDuration.Load(), "durations under 60s should be clamped")
	})

	t.Run("response missing the expected oneof returns ErrUnexpectedResponse", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din1"))
			case "/tedapi/v1r":
				writeRoutableEnvelope(t, w, &combined.MessageEnvelope{})
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.ScheduleMaxBackup(t.Context(), 120)
		require.ErrorIs(t, err, backend.ErrUnexpectedResponse)
	})

	t.Run("GetDin failure short-circuits scheduling", func(t *testing.T) {
		t.Parallel()

		v := newV1r(t, "127.0.0.1:1")
		_, err := v.ScheduleMaxBackup(t.Context(), 120)
		require.Error(t, err)
	})

	t.Run("cancel succeeds", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din1"))
			case "/tedapi/v1r":
				writeRoutableEnvelope(t, w, &combined.MessageEnvelope{
					Payload: &combined.MessageEnvelope_Teg{Teg: &combined.TEGMessages{
						Message: &combined.TEGMessages_CancelManualBackupEventResponse{
							CancelManualBackupEventResponse: &combined.TEGAPICancelManualBackupEventResponse{},
						},
					}},
				})
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		ok, err := v.CancelMaxBackup(t.Context())
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("cancel with unexpected response returns ErrUnexpectedResponse", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din1"))
			case "/tedapi/v1r":
				writeRoutableEnvelope(t, w, &combined.MessageEnvelope{})
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.CancelMaxBackup(t.Context())
		require.ErrorIs(t, err, backend.ErrUnexpectedResponse)
	})
}

func TestV1rGetBackupEvents(t *testing.T) {
	t.Parallel()

	t.Run("success decodes events and the manual backup event", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din1"))
			case "/tedapi/v1r":
				writeRoutableEnvelope(t, w, &combined.MessageEnvelope{
					Payload: &combined.MessageEnvelope_Teg{Teg: &combined.TEGMessages{
						Message: &combined.TEGMessages_GetBackupEventsResponse{
							GetBackupEventsResponse: &combined.TEGAPIGetBackupEventsResponse{
								BackupEvents: []*combined.BackupEvent{
									{
										Id:   "evt-1",
										Name: "storm watch",
										SchedulingInfo: &combined.ControlEventSchedulingInfo{
											DurationSeconds: 3600,
											Priority:        1,
										},
									},
								},
								ManualBackupEvent: &combined.ManualBackupEvent{
									SchedulingInfo: &combined.ControlEventSchedulingInfo{
										DurationSeconds: 7200,
										Priority:        2,
									},
								},
							},
						},
					}},
				})
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		out, err := v.GetBackupEvents(t.Context())
		require.NoError(t, err)

		events, ok := out["backup_events"].([]any)
		require.True(t, ok)
		require.Len(t, events, 1)
		evt, ok := events[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "evt-1", evt["id"])
		assert.Equal(t, "storm watch", evt["name"])
		assert.Equal(t, uint32(3600), evt["duration_seconds"])

		manual, ok := out["manual_backup_event"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, uint32(7200), manual["duration_seconds"])
	})

	t.Run("empty response returns ErrEmptyResponse", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/tedapi/din":
				_, _ = w.Write([]byte("din1"))
			case "/tedapi/v1r":
				writeRoutableEnvelope(t, w, &combined.MessageEnvelope{})
			}
		}
		srv := newTLSServer(t, handler)
		v := newV1r(t, srv.Listener.Addr().String())

		_, err := v.GetBackupEvents(t.Context())
		require.ErrorIs(t, err, backend.ErrEmptyResponse)
	})

	t.Run("GetDin failure short-circuits the request", func(t *testing.T) {
		t.Parallel()

		v := newV1r(t, "127.0.0.1:1")
		_, err := v.GetBackupEvents(t.Context())
		require.Error(t, err)
	})
}

func TestV1rAPIGet(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErrIs error
		name      string
		body      string
		status    int
		wantIsMap bool
	}

	cases := []testCase{
		{name: "JSON body decodes to a map", body: `{"status":"StatusUp"}`, status: http.StatusOK, wantIsMap: true},
		{name: "non-JSON body falls back to a string", body: "plain", status: http.StatusOK},
		{
			name:      "non-200 status returns ErrUnexpectedStatus",
			status:    http.StatusInternalServerError,
			wantErrIs: backend.ErrUnexpectedStatus,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}
			srv := newTLSServer(t, handler)
			v := newV1r(t, srv.Listener.Addr().String())

			res, err := v.APIGet(t.Context(), "/api/sitemaster")
			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)

				return
			}
			require.NoError(t, err)
			if tc.wantIsMap {
				_, ok := res.(map[string]any)
				assert.True(t, ok)

				return
			}
			_, ok := res.(string)
			assert.True(t, ok)
		})
	}
}

func TestV1rAPIGetAppliesBearerToken(t *testing.T) {
	t.Parallel()

	var gotAuth string
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/Basic":
			_, _ = w.Write([]byte(`{"token":"apitoken"}`))
		default:
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{}`))
		}
	}
	srv := newTLSServer(t, handler)
	v := newV1r(t, srv.Listener.Addr().String())
	require.NoError(t, v.Login(t.Context()))

	_, err := v.APIGet(t.Context(), "/api/status")
	require.NoError(t, err)
	assert.Equal(t, "Bearer apitoken", gotAuth)
}

func TestV1rAPIGetContextCancellation(t *testing.T) {
	t.Parallel()

	v := newV1r(t, "127.0.0.1:1")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := v.APIGet(ctx, "/api/status")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
