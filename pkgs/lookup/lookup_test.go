package lookup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
