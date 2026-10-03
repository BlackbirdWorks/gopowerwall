package compress_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/compress"
)

func decompressResponse(t *testing.T, encoding string, body []byte) []byte {
	t.Helper()

	switch encoding {
	case compress.EncodingZstd:
		zr, err := zstd.NewReader(bytes.NewReader(body))
		require.NoError(t, err)
		defer zr.Close()
		decompressed, err := io.ReadAll(zr)
		require.NoError(t, err)

		return decompressed

	case compress.EncodingBrotli:
		br := brotli.NewReader(bytes.NewReader(body))
		decompressed, err := io.ReadAll(br)
		require.NoError(t, err)

		return decompressed

	case compress.EncodingGzip:
		gr, err := gzip.NewReader(bytes.NewReader(body))
		require.NoError(t, err)
		defer func() {
			_ = gr.Close()
		}()
		decompressed, err := io.ReadAll(gr)
		require.NoError(t, err)

		return decompressed

	default:
		return body
	}
}

func TestNegotiate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name           string
		acceptEncoding string
		wantEncoding   string
	}

	for _, tc := range []testCase{
		{
			name:           "empty header",
			acceptEncoding: "",
			wantEncoding:   "",
		},
		{
			name:           "zstd only",
			acceptEncoding: "zstd",
			wantEncoding:   compress.EncodingZstd,
		},
		{
			name:           "brotli only",
			acceptEncoding: "br",
			wantEncoding:   compress.EncodingBrotli,
		},
		{
			name:           "gzip only",
			acceptEncoding: "gzip",
			wantEncoding:   compress.EncodingGzip,
		},
		{
			name:           "all three prefers zstd",
			acceptEncoding: "gzip, deflate, br, zstd",
			wantEncoding:   compress.EncodingZstd,
		},
		{
			name:           "br and gzip prefers br",
			acceptEncoding: "gzip, deflate, br",
			wantEncoding:   compress.EncodingBrotli,
		},
		{
			name:           "unsupported algorithm",
			acceptEncoding: "snappy, lz4",
			wantEncoding:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := compress.Negotiate(tc.acceptEncoding)
			assert.Equal(t, tc.wantEncoding, got)
		})
	}
}

func TestIsCompressible(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		contentType string
		want        bool
	}

	for _, tc := range []testCase{
		{name: "empty content type defaults true", contentType: "", want: true},
		{name: "application json", contentType: "application/json", want: true},
		{name: "application json with charset", contentType: "application/json; charset=utf-8", want: true},
		{name: "text html", contentType: "text/html", want: true},
		{name: "text csv", contentType: "text/csv", want: true},
		{name: "text plain", contentType: "text/plain", want: true},
		{name: "application javascript", contentType: "application/javascript", want: true},
		{name: "application xml", contentType: "application/xml", want: true},
		{name: "image svg", contentType: "image/svg+xml", want: true},
		{name: "image png is binary", contentType: "image/png", want: false},
		{name: "image jpeg is binary", contentType: "image/jpeg", want: false},
		{name: "application octet-stream is binary", contentType: "application/octet-stream", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := compress.IsCompressible(tc.contentType)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMiddleware(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name           string
		acceptEncoding string
		contentType    string
		responseBody   string
		wantEncoding   string
		wantStatus     int
	}

	for _, tc := range []testCase{
		{
			name:           "compresses json with zstd",
			acceptEncoding: "zstd, br, gzip",
			contentType:    "application/json",
			responseBody:   `{"solar": 5200.5, "battery": 13500.0, "grid": -200.0}`,
			wantEncoding:   compress.EncodingZstd,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "compresses json with brotli when zstd absent",
			acceptEncoding: "br, gzip",
			contentType:    "application/json",
			responseBody:   `{"solar": 5200.5, "battery": 13500.0, "grid": -200.0}`,
			wantEncoding:   compress.EncodingBrotli,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "compresses json with gzip when only gzip supported",
			acceptEncoding: "gzip",
			contentType:    "application/json",
			responseBody:   `{"solar": 5200.5, "battery": 13500.0, "grid": -200.0}`,
			wantEncoding:   compress.EncodingGzip,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "passes through uncompressed when client has no encoding",
			acceptEncoding: "",
			contentType:    "application/json",
			responseBody:   `{"solar": 5200.5, "battery": 13500.0, "grid": -200.0}`,
			wantEncoding:   "",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "skips compression for binary types like image/png",
			acceptEncoding: "zstd, br, gzip",
			contentType:    "image/png",
			responseBody:   "rawbinarypngbytes",
			wantEncoding:   "",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "handles custom status codes like 404",
			acceptEncoding: "zstd",
			contentType:    "application/json",
			responseBody:   `{"error": "not found"}`,
			wantEncoding:   compress.EncodingZstd,
			wantStatus:     http.StatusNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.wantStatus)
				_, _ = w.Write([]byte(tc.responseBody))
			})

			c := compress.NewCompressor()
			wrapped := c.Handler(handler)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/test", nil)
			if tc.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			res := rec.Result()
			defer func() {
				_ = res.Body.Close()
			}()

			assert.Equal(t, tc.wantStatus, res.StatusCode)
			assert.Equal(t, tc.wantEncoding, res.Header.Get("Content-Encoding"))
			assert.Contains(t, res.Header.Get("Vary"), "Accept-Encoding")

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			decompressed := decompressResponse(t, tc.wantEncoding, body)
			assert.Equal(t, tc.responseBody, string(decompressed))
		})
	}
}
