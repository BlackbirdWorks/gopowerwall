package atomicfile_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/atomicfile"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name     string
		existing []byte
		data     []byte
		perm     os.FileMode
	}

	cases := []testCase{
		{name: "creates a new file", data: []byte("hello"), perm: 0o600},
		{name: "replaces an existing file", existing: []byte("old"), data: []byte("new"), perm: 0o600},
		{name: "empty payload", data: []byte{}, perm: 0o600},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "target.txt")
			if tc.existing != nil {
				require.NoError(t, os.WriteFile(path, tc.existing, 0o600))
			}

			require.NoError(t, atomicfile.Write(path, tc.data, tc.perm))

			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tc.data, got)

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Len(t, entries, 1, "no temp file should be left behind")

			if runtime.GOOS != "windows" {
				info, statErr := os.Stat(path)
				require.NoError(t, statErr)
				assert.Equal(t, tc.perm, info.Mode().Perm())
			}
		})
	}
}

func TestWriteRejectsMissingDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "does-not-exist", "target.txt")
	err := atomicfile.Write(path, []byte("data"), 0o600)
	require.Error(t, err)
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	type testCase struct {
		value any
		name  string
	}

	cases := []testCase{
		{name: "map", value: map[string]any{"a": 1, "b": "two"}},
		{name: "slice", value: []int{1, 2, 3}},
		{name: "nil", value: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "target.json")

			require.NoError(t, atomicfile.WriteJSON(path, tc.value, 0o600))

			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.NotEmpty(t, got)
		})
	}
}
