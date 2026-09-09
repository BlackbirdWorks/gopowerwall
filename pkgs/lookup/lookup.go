// Package lookup provides safe navigation for nested dynamic maps.
package lookup

import (
	"errors"
	"strings"
)

// ErrNotFound indicates the requested key path was not found or the value was nil.
var ErrNotFound = errors.New("key path not found")

// ErrTypeMismatch indicates the found value did not match the expected type.
var ErrTypeMismatch = errors.New("type mismatch")

func flattenKeys(keys []string) []string {
	for _, k := range keys {
		if strings.Contains(k, ".") {
			flattened := make([]string, 0, len(keys))
			for _, key := range keys {
				if strings.Contains(key, ".") {
					flattened = append(flattened, strings.Split(key, ".")...)
				} else {
					flattened = append(flattened, key)
				}
			}

			return flattened
		}
	}

	return keys
}

func stepMap(curr any, key string) (any, bool) {
	switch m := curr.(type) {
	case map[string]any:
		val, ok := m[key]

		return val, ok
	case map[string]string:
		val, ok := m[key]

		return val, ok
	default:
		return nil, false
	}
}

// Lookup safely traverses nested maps and returns value or nil if not found.
// Matches Python lookup(data, keylist). Supports variadic keys and dot-notation ("a.b.c").
func Lookup(data any, keys ...string) any {
	if len(keys) == 0 {
		return data
	}

	curr := data
	for _, key := range flattenKeys(keys) {
		if curr == nil {
			return nil
		}
		val, ok := stepMap(curr, key)
		if !ok {
			return nil
		}
		curr = val
	}

	return curr
}

// Value traverses data via keys and returns the value cast to T.
func Value[T any](data any, keys ...string) (T, bool) {
	v := Lookup(data, keys...)
	if v == nil {
		var zero T

		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		var zero T

		return zero, false
	}

	return typed, true
}

// Float64 traverses data via keys and converts numeric results (float64 or int) to float64.
func Float64(data any, keys ...string) (float64, error) {
	v := Lookup(data, keys...)
	if v == nil {
		return 0, ErrNotFound
	}
	switch val := v.(type) {
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	default:
		return 0, ErrTypeMismatch
	}
}

// String traverses data via keys and returns the string value if present.
func String(data any, keys ...string) (string, error) {
	v := Lookup(data, keys...)
	if v == nil {
		return "", ErrNotFound
	}
	if s, ok := v.(string); ok {
		return s, nil
	}

	return "", ErrTypeMismatch
}
