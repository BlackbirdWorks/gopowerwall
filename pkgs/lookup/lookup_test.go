package lookup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

func TestLookup(t *testing.T) {
	t.Parallel()

	nested := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": 42,
			},
		},
		"simple":     "hello",
		"stringmap":  map[string]string{"key": "value"},
		"notAMap":    123,
		"nilValue":   nil,
		"emptyLevel": map[string]any{},
	}

	type testCase struct {
		data any
		want any
		name string
		keys []string
	}

	for _, tc := range []testCase{
		{name: "flat key lookup", data: nested, keys: []string{"simple"}, want: "hello"},
		{name: "nested variadic keys", data: nested, keys: []string{"a", "b", "c"}, want: 42},
		{name: "nested dot notation", data: nested, keys: []string{"a.b.c"}, want: 42},
		{name: "mixed dot and plain keys", data: nested, keys: []string{"a.b", "c"}, want: 42},
		{name: "missing key at leaf", data: nested, keys: []string{"a", "missing"}, want: nil},
		{name: "missing top level key", data: nested, keys: []string{"missing"}, want: nil},
		{name: "nil input data", data: nil, keys: []string{"a"}, want: nil},
		{name: "no keys returns data unchanged", data: nested, keys: nil, want: nested},
		{name: "string map value lookup", data: map[string]string{"key": "value"}, keys: []string{"key"}, want: "value"},
		{
			name: "string map missing key",
			data: map[string]string{"key": "value"},
			keys: []string{"missing"},
			want: nil,
		},
		{name: "traversal through nested string map", data: nested, keys: []string{"stringmap", "key"}, want: "value"},
		{
			name: "descending into non-map value returns nil",
			data: nested,
			keys: []string{"notAMap", "anything"},
			want: nil,
		},
		{name: "descending into nil value returns nil", data: nested, keys: []string{"nilValue", "anything"}, want: nil},
		{name: "descending into empty map returns nil", data: nested, keys: []string{"emptyLevel", "missing"}, want: nil},
		{name: "non-map, non-string-map root returns nil", data: 42, keys: []string{"a"}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, lookup.Lookup(tc.data, tc.keys...))
		})
	}
}

func TestValue(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"greeting": "hello",
		"count":    42,
		"nested": map[string]any{
			"valid": true,
		},
	}

	type testCase struct {
		validate func(t *testing.T)
		name     string
	}

	for _, tc := range []testCase{
		{
			name: "string type match",
			validate: func(t *testing.T) {
				t.Helper()
				v, ok := lookup.Value[string](data, "greeting")
				assert.True(t, ok)
				assert.Equal(t, "hello", v)
			},
		},
		{
			name: "string type mismatch with int",
			validate: func(t *testing.T) {
				t.Helper()
				v, ok := lookup.Value[string](data, "count")
				assert.False(t, ok)
				assert.Empty(t, v)
			},
		},
		{
			name: "bool nested lookup",
			validate: func(t *testing.T) {
				t.Helper()
				v, ok := lookup.Value[bool](data, "nested.valid")
				assert.True(t, ok)
				assert.True(t, v)
			},
		},
		{
			name: "missing key",
			validate: func(t *testing.T) {
				t.Helper()
				v, ok := lookup.Value[int](data, "nonexistent")
				assert.False(t, ok)
				assert.Zero(t, v)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.validate(t)
		})
	}
}

func TestFloat64(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"float":  12.34,
		"int":    56,
		"string": "notanumber",
	}

	type testCase struct {
		wantErr error
		name    string
		keys    []string
		want    float64
	}

	for _, tc := range []testCase{
		{name: "float64 value", keys: []string{"float"}, want: 12.34, wantErr: nil},
		{name: "int converted to float64", keys: []string{"int"}, want: 56.0, wantErr: nil},
		{name: "non-numeric string type mismatch", keys: []string{"string"}, want: 0, wantErr: lookup.ErrTypeMismatch},
		{name: "missing key not found", keys: []string{"missing"}, want: 0, wantErr: lookup.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := lookup.Float64(data, tc.keys...)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.InDelta(t, tc.want, got, 0.0001)
			}
		})
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"text": "sample",
		"num":  100,
	}

	type testCase struct {
		wantErr error
		name    string
		want    string
		keys    []string
	}

	for _, tc := range []testCase{
		{name: "string value", keys: []string{"text"}, want: "sample", wantErr: nil},
		{name: "numeric type mismatch", keys: []string{"num"}, want: "", wantErr: lookup.ErrTypeMismatch},
		{name: "missing key not found", keys: []string{"missing"}, want: "", wantErr: lookup.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := lookup.String(data, tc.keys...)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
