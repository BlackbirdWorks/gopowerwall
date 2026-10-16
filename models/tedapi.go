package models

import (
	"encoding/json"
	"strings"
)

// TEDAPIMode represents the TEDAPI sub-mode.
type TEDAPIMode string

const (
	TEDAPIOff    TEDAPIMode = "off"
	TEDAPIFull   TEDAPIMode = "full"
	TEDAPIHybrid TEDAPIMode = "hybrid"
	TEDAPIV1r    TEDAPIMode = "v1r"
)

// TEDAPIApiVersion represents the protobuf/query version set.
type TEDAPIApiVersion string

const (
	TEDAPIVersion2024_06 TEDAPIApiVersion = "V2024_06"
	TEDAPIVersion2026_06 TEDAPIApiVersion = "V2026_06"
)

// CoerceTEDAPIApiVersion parses a string into a valid TEDAPIApiVersion.
func CoerceTEDAPIApiVersion(v string) TEDAPIApiVersion {
	u := strings.ToUpper(strings.TrimSpace(v))
	if u == "V2026_06" || u == "2026_06" || u == "V2026" {
		return TEDAPIVersion2026_06
	}

	return TEDAPIVersion2024_06
}

// ComponentSignal represents a single telemetry signal reported within a TEDAPI component.
type ComponentSignal struct {
	Value     *float64 `json:"value,omitempty"`
	BoolValue *bool    `json:"boolValue,omitempty"`
	TextValue *string  `json:"textValue,omitempty"`
	Name      string   `json:"name"`
}

// AnyValue returns the first non-nil signal value among value, boolValue, and textValue,
// mirroring upstream pypowerwall's _component_signal_value signal resolution.
func (s ComponentSignal) AnyValue() any {
	if s.Value != nil {
		return *s.Value
	}
	if s.BoolValue != nil {
		return *s.BoolValue
	}
	if s.TextValue != nil {
		return *s.TextValue
	}

	return nil
}

// FloatValue returns the float64 value if value is present and non-nil.
func (s ComponentSignal) FloatValue() (float64, bool) {
	if s.Value != nil {
		return *s.Value, true
	}

	return 0, false
}

// StringValue returns the string text value if textValue is present and non-nil.
func (s ComponentSignal) StringValue() (string, bool) {
	if s.TextValue != nil {
		return *s.TextValue, true
	}

	return "", false
}

// BooleanValue returns the boolean value if boolValue is present and non-nil.
func (s ComponentSignal) BooleanValue() (bool, bool) {
	if s.BoolValue != nil {
		return *s.BoolValue, true
	}

	return false, false
}

// ComponentAlert represents an active alert within a component.
type ComponentAlert struct {
	Name string `json:"name"`
}

// Component represents a hardware component block reported by TEDAPI
// (e.g. in components.bms, components.hvp, components.pch, components.msa).
type Component struct {
	PartNumber   string            `json:"partNumber,omitempty"`
	SerialNumber string            `json:"serialNumber,omitempty"`
	ActiveAlerts []ComponentAlert  `json:"activeAlerts,omitempty"`
	Signals      []ComponentSignal `json:"signals,omitempty"`
}

// Signal finds the first signal matching name.
func (c Component) Signal(name string) (ComponentSignal, bool) {
	for _, sig := range c.Signals {
		if sig.Name == name {
			return sig, true
		}
	}

	return ComponentSignal{}, false
}

// SignalValue returns the first non-nil value of signal name in this component.
func (c Component) SignalValue(name string) any {
	if sig, ok := c.Signal(name); ok {
		return sig.AnyValue()
	}

	return nil
}

// SignalFloat returns the float64 value of signal name if present.
func (c Component) SignalFloat(name string) (float64, bool) {
	if sig, ok := c.Signal(name); ok && sig.Value != nil {
		return *sig.Value, true
	}

	return 0, false
}

// ComponentList represents a slice of components with lookup helpers.
type ComponentList []Component

// SignalValue finds the first non-nil signal value for name across the component list.
// Mirrors upstream pypowerwall's _component_signal_value(components, name).
func (cl ComponentList) SignalValue(name string) any {
	for _, comp := range cl {
		if val := comp.SignalValue(name); val != nil {
			return val
		}
	}

	return nil
}

// SignalFloat finds the first float64 value for name across the component list.
func (cl ComponentList) SignalFloat(name string) (float64, bool) {
	for _, comp := range cl {
		if val, ok := comp.SignalFloat(name); ok {
			return val, true
		}
	}

	return 0, false
}

// FindBySerialNumber finds the component matching the given serial number.
func (cl ComponentList) FindBySerialNumber(serial string) (Component, bool) {
	for _, comp := range cl {
		if comp.SerialNumber == serial {
			return comp, true
		}
	}

	return Component{}, false
}

// ComponentsMap represents the components payload structure in TEDAPI.
type ComponentsMap struct {
	BMS   ComponentList `json:"bms,omitempty"`
	HVP   ComponentList `json:"hvp,omitempty"`
	PCH   ComponentList `json:"pch,omitempty"`
	PWS   ComponentList `json:"pws,omitempty"`
	BAGGR ComponentList `json:"baggr,omitempty"`
	MSA   ComponentList `json:"msa,omitempty"`
}

// ParseComponents parses a raw components map or GraphQL response into a typed ComponentsMap.
func ParseComponents(data any) ComponentsMap {
	if data == nil {
		return ComponentsMap{}
	}
	if m, ok := data.(map[string]any); ok {
		if sub, hasSub := m["components"].(map[string]any); hasSub {
			data = sub
		}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ComponentsMap{}
	}
	var cm ComponentsMap
	_ = json.Unmarshal(b, &cm)

	return cm
}
