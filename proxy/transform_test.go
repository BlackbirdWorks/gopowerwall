package proxy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

func TestDetectMimeType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		path string
		want string
	}

	for _, tc := range []testCase{
		{name: "javascript", path: "app.js", want: "application/javascript"},
		{name: "css", path: "styles.css", want: "text/css"},
		{name: "png", path: "logo.PNG", want: "image/png"},
		{name: "html", path: "index.html", want: "text/html"},
		{name: "htm", path: "index.htm", want: "text/html"},
		{name: "otf font", path: "font.otf", want: "font/opentype"},
		{name: "woff font", path: "font.woff", want: "font/woff"},
		{name: "woff2 font", path: "font.woff2", want: "font/woff2"},
		{name: "ttf font", path: "font.ttf", want: "font/ttf"},
		{name: "svg", path: "icon.svg", want: "image/svg+xml"},
		{name: "eot font", path: "font.eot", want: "application/vnd.ms-fontobject"},
		{name: "json", path: "data.json", want: "application/json"},
		{name: "xml", path: "data.xml", want: "application/xml"},
		{name: "unknown falls back to plain text", path: "data.bin", want: "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, proxy.DetectMimeType(tc.path))
		})
	}
}

func TestInjectJS(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		html    string
		want    string
		scripts []string
	}

	for _, tc := range []testCase{
		{
			name:    "injects before closing body tag",
			html:    "<html><body><h1>hi</h1></body></html>",
			scripts: []string{"clear.js"},
			want:    `<html><body><h1>hi</h1><script type="text/javascript" src="clear.js"></script></body></html>`,
		},
		{
			name:    "matches closing body tag case-insensitively",
			html:    "<html><BODY>hi</BODY></html>",
			scripts: []string{"a.js"},
			want:    `<html><BODY>hi<script type="text/javascript" src="a.js"></script></BODY></html>`,
		},
		{
			name:    "appends when no body tag present",
			html:    "<div>no body here</div>",
			scripts: []string{"a.js"},
			want:    `<div>no body here</div><script type="text/javascript" src="a.js"></script>`,
		},
		{
			name:    "injects multiple scripts in order",
			html:    "<body></body>",
			scripts: []string{"a.js", "b.js"},
			want: `<body><script type="text/javascript" src="a.js"></script>` +
				`<script type="text/javascript" src="b.js"></script></body>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := proxy.InjectJS([]byte(tc.html), tc.scripts...)
			assert.Equal(t, tc.want, string(got))
		})
	}
}

func TestGetStaticFromEmbeddedFS(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		path        string
		wantMime    string
		wantContain string
		wantErr     bool
	}

	for _, tc := range []testCase{
		{name: "root maps to index.html", path: "/", wantMime: "text/html", wantContain: "<html"},
		{name: "empty path maps to index.html", path: "", wantMime: "text/html", wantContain: "<html"},
		{name: "explicit index.html", path: "/index.html", wantMime: "text/html", wantContain: "<html"},
		{name: "query string is stripped", path: "/index.html?v=123", wantMime: "text/html", wantContain: "<html"},
		{name: "missing file errors", path: "/does-not-exist.js", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content, mime, err := proxy.GetStatic("", tc.path)
			if tc.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantMime, mime)
			assert.Contains(t, strings.ToLower(string(content)), tc.wantContain)
		})
	}
}

func TestGetStaticDiskOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>custom</html>"), 0o600))

	content, mime, err := proxy.GetStatic(dir, "/index.html")
	require.NoError(t, err)
	assert.Equal(t, "text/html", mime)
	assert.Equal(t, "<html>custom</html>", string(content))
}

func TestGetStaticDiskFallsBackToEmbedded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// dir exists but has no favicon.ico, so GetStatic must fall back to the
	// embedded WebFS copy instead of erroring.
	content, mime, err := proxy.GetStatic(dir, "/favicon.ico")
	require.NoError(t, err)
	assert.NotEmpty(t, content)
	assert.NotEmpty(t, mime)
}

func TestGetStaticDiskRejectsPathEscape(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	t.Cleanup(func() { _ = os.Remove(outside) })

	// Escaping webRoot via ".." must not read the sibling file; GetStatic
	// should fall through to the embedded FS (and fail, since it has no
	// such path) rather than serve content from outside webRoot.
	_, _, err := proxy.GetStatic(dir, "/../secret.txt")
	require.Error(t, err)
}

func TestGetStaticEmptyWebRootUsesEmbeddedOnly(t *testing.T) {
	t.Parallel()

	content, mime, err := proxy.GetStatic("", "/favicon.ico")
	require.NoError(t, err)
	assert.NotEmpty(t, content)
	// DetectMimeType has no dedicated .ico case, so it falls back to
	// text/plain; this pins that (possibly surprising) actual behaviour.
	assert.Equal(t, "text/plain", mime)
}
