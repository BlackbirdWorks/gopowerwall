package lookup

// OrNil converts a (value, error) pair returned by an accessor into an any that is
// nil if err is non-nil, or v if err is nil. This simplifies mapping accessor
// results into JSON-serializable structures where errors should render as null.
func OrNil[T any](v T, err error) any {
	if err != nil {
		return nil
	}

	return v
}
