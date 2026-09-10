package poller_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/poller"
)

var (
	errMockFail    = errors.New("mock poll failed")
	errMockParams  = errors.New("unexpected params")
	errMockDefault = errors.New("mock default error")
)

type mockSource struct {
	pollFn func(ctx context.Context, api string, force, recursive, raw bool) (any, error)
}

func (m *mockSource) Poll(ctx context.Context, api string, force, recursive, raw bool) (any, error) {
	if m.pollFn != nil {
		return m.pollFn(ctx, api, force, recursive, raw)
	}

	return nil, errMockDefault
}

type pollTestCase struct {
	errTarget error
	setupMock func() *mockSource
	name      string
	api       string
	wantVal   any
	opts      []poller.Option
	wantErr   bool
}

func TestPoller_Poll(t *testing.T) {
	t.Parallel()

	tests := []pollTestCase{
		{
			name:      "nil poller returns ErrNoSource",
			api:       "/api/status",
			wantErr:   true,
			errTarget: poller.ErrNoSource,
		},
		{
			name: "successful poll with options",
			api:  "/api/status",
			opts: []poller.Option{
				poller.WithForce(true),
				poller.WithRecursive(true),
				poller.WithRaw(false),
			},
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, api string, force, recursive, raw bool) (any, error) {
						if api == "/api/status" && force && recursive && !raw {
							return map[string]any{"status": "ok"}, nil
						}

						return nil, errMockParams
					},
				}
			},
			wantVal: map[string]any{"status": "ok"},
			wantErr: false,
		},
		{
			name: "source error propagated",
			api:  "/api/error",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return nil, errMockFail
					},
				}
			},
			wantErr:   true,
			errTarget: errMockFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p *poller.Poller
			if tt.setupMock != nil {
				p = poller.New(tt.setupMock())
			}

			val, err := p.Poll(t.Context(), tt.api, tt.opts...)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errTarget != nil {
					require.ErrorIs(t, err, tt.errTarget)
				}

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantVal, val)
		})
	}
}

type pollRawTestCase struct {
	errTarget error
	setupMock func() *mockSource
	name      string
	api       string
	wantBytes []byte
	wantErr   bool
}

func TestPoller_PollRaw(t *testing.T) {
	t.Parallel()

	tests := []pollRawTestCase{
		{
			name:      "nil poller returns ErrNoSource",
			api:       "/api/status",
			wantErr:   true,
			errTarget: poller.ErrNoSource,
		},
		{
			name: "source returns byte slice directly",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return []byte(`{"version":"1.0"}`), nil
					},
				}
			},
			wantBytes: []byte(`{"version":"1.0"}`),
			wantErr:   false,
		},
		{
			name: "source returns string",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return `{"version":"1.0"}`, nil
					},
				}
			},
			wantBytes: []byte(`{"version":"1.0"}`),
			wantErr:   false,
		},
		{
			name: "source returns map, marshaled to JSON bytes",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return map[string]string{"foo": "bar"}, nil
					},
				}
			},
			wantBytes: []byte(`{"foo":"bar"}`),
			wantErr:   false,
		},
		{
			name: "source error propagated",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return nil, errMockFail
					},
				}
			},
			wantErr:   true,
			errTarget: errMockFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p *poller.Poller
			if tt.setupMock != nil {
				p = poller.New(tt.setupMock())
			}

			b, err := p.PollRaw(t.Context(), tt.api, poller.WithRaw(true))
			if tt.wantErr {
				require.Error(t, err)
				if tt.errTarget != nil {
					require.ErrorIs(t, err, tt.errTarget)
				}

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBytes, b)
		})
	}
}

type pollJSONTestCase struct {
	errTarget error
	setupMock func() *mockSource
	name      string
	api       string
	wantJSON  string
	wantErr   bool
}

func TestPoller_PollJSON(t *testing.T) {
	t.Parallel()

	tests := []pollJSONTestCase{
		{
			name:      "nil poller returns ErrNoSource",
			api:       "/api/status",
			wantErr:   true,
			errTarget: poller.ErrNoSource,
		},
		{
			name: "source returns string directly",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return `{"msg":"hello"}`, nil
					},
				}
			},
			wantJSON: `{"msg":"hello"}`,
			wantErr:  false,
		},
		{
			name: "source returns byte slice",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return []byte(`{"msg":"hello"}`), nil
					},
				}
			},
			wantJSON: `{"msg":"hello"}`,
			wantErr:  false,
		},
		{
			name: "source returns struct/map marshaled to string",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return map[string]string{"k": "v"}, nil
					},
				}
			},
			wantJSON: `{"k":"v"}`,
			wantErr:  false,
		},
		{
			name: "source returns unmarshalable value",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return make(chan int), nil
					},
				}
			},
			wantErr: true,
		},
		{
			name: "source error propagated",
			api:  "/api/status",
			setupMock: func() *mockSource {
				return &mockSource{
					pollFn: func(_ context.Context, _ string, _, _, _ bool) (any, error) {
						return nil, errMockFail
					},
				}
			},
			wantErr:   true,
			errTarget: errMockFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p *poller.Poller
			if tt.setupMock != nil {
				p = poller.New(tt.setupMock())
			}

			s, err := p.PollJSON(t.Context(), tt.api)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errTarget != nil {
					require.ErrorIs(t, err, tt.errTarget)
				}

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantJSON, s)
		})
	}
}

type pollerSourceTestCase struct {
	setupMock func() *mockSource
	name      string
	wantNil   bool
}

func TestPoller_Source(t *testing.T) {
	t.Parallel()

	tests := []pollerSourceTestCase{
		{
			name:    "nil poller returns nil source",
			wantNil: true,
		},
		{
			name: "initialized poller returns its source",
			setupMock: func() *mockSource {
				return &mockSource{}
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p *poller.Poller
			if tt.setupMock != nil {
				p = poller.New(tt.setupMock())
			}

			src := p.Source()
			if tt.wantNil {
				assert.Nil(t, src)
			} else {
				assert.NotNil(t, src)
			}
		})
	}
}
