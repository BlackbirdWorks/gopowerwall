// Package lookup provides safe navigation for nested dynamic maps.
package lookup

import "strings"

// Lookup safely traverses nested maps and returns value or nil if not found.
// Matches Python lookup(data, keylist). Supports variadic keys and dot-notation ("a.b.c").
func Lookup(data any, keys ...string) any {
	curr := data
	flattened := make([]string, 0, len(keys))
	for _, k := range keys {
		if strings.Contains(k, ".") {
			flattened = append(flattened, strings.Split(k, ".")...)
		} else {
			flattened = append(flattened, k)
		}
	}

	for _, key := range flattened {
		if curr == nil {
			return nil
		}
		switch m := curr.(type) {
		case map[string]any:
			val, ok := m[key]
			if !ok {
				return nil
			}
			curr = val
		case map[string]string:
			val, ok := m[key]
			if !ok {
				return nil
			}
			curr = val
		default:
			return nil
		}
	}

	return curr
}
