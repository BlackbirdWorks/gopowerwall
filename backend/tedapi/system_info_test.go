package tedapi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/backend/tedapi"
)

func TestDecodeGitHash(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		want  string
		input []byte
	}

	cases := []testCase{
		{name: "empty input returns empty string", input: nil, want: ""},
		{name: "printable ASCII passes through unchanged", input: []byte("27626f98a66c"), want: "27626f98a66c"},
		{
			name:  "non-printable bytes are hex encoded",
			input: []byte{0x00, 0x01, 0xFF, 0xFE},
			want:  "0001fffe",
		},
		{
			name:  "printable text containing a newline is hex encoded",
			input: []byte("abc\ndef"),
			want:  "6162630a646566",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tedapi.DecodeGitHash(tc.input))
		})
	}
}

func TestSystemInfoToDetailsDict(t *testing.T) {
	t.Parallel()

	info := &tedapi.SystemInfo{
		DIN:          "1232100-00-E--TG1234567890G1",
		Version:      "23.28.2",
		GitHash:      "27626f98",
		PartNumber:   "1232100-00-E",
		SerialNumber: "TG1234567890G1",
		DeviceType:   "teg",
		WirelessRadios: []tedapi.RadioInfo{
			{Company: "Tesla", Model: "Radio1", FCCID: "FCC1", IC: "IC1"},
		},
	}

	details := info.ToDetailsDict()

	gateway, ok := details["gateway"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "1232100-00-E", gateway["partNumber"])
	assert.Equal(t, "TG1234567890G1", gateway["serialNumber"])

	version, ok := details["version"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "23.28.2", version["text"])
	assert.Equal(t, "27626f98", version["githash"])

	assert.Equal(t, "1232100-00-E--TG1234567890G1", details["din"])
	assert.Equal(t, "teg", details["deviceType"])

	radios, ok := details["wireless"].([]map[string]string)
	require.True(t, ok)
	require.Len(t, radios, 1)
	assert.Equal(t, "Tesla", radios[0]["company"])
}

func TestSystemInfoToDetailsDictEmptyRadios(t *testing.T) {
	t.Parallel()

	info := &tedapi.SystemInfo{}
	details := info.ToDetailsDict()

	radios, ok := details["wireless"].([]map[string]string)
	assert.True(t, ok)
	assert.Empty(t, radios)
}
