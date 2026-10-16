package tedapi

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"

	"github.com/blackbirdworks/gopowerwall/models"
)

//go:embed queries/V2024_06.json
var v2024QueriesJSON []byte

//go:embed queries/V2026_06.json
var v2026QueriesJSON []byte

// QueryRole constants.
const (
	QueryRoleDeviceControllerBasic = "device_controller_basic"
	QueryRoleDeviceControllerFull  = "device_controller_full"
	QueryRoleComponents            = "components"
)

// Query holds a versioned TEDAPI GraphQL query definition.
type Query struct {
	Text        string
	BValue      string
	Code        []byte
	SignedBytes []byte
	Version     int
}

type queryJSONRecord struct {
	Text        string `json:"text"`
	Code        string `json:"code"`
	BValue      string `json:"b_value"`
	SignedBytes string `json:"signed_bytes"`
	Version     int    `json:"version"`
}

var (
	//nolint:gochecknoglobals // Embedded queries are loaded once at package init.
	v2024Queries = loadQuerySet(v2024QueriesJSON, true)
	//nolint:gochecknoglobals // Embedded queries are loaded once at package init.
	v2026Queries = loadQuerySet(v2026QueriesJSON, false)
)

func getExtraSignalNames() map[string]map[string][]string {
	return map[string]map[string][]string{
		QueryRoleComponents: {
			"pchSignalNames": {
				"PCH_AmbientTemp", "PCH_heatsinkTemp",
				"PCH_FanSpeed_A", "PCH_FanSpeed_B", "PCH_FanDuty_A", "PCH_FanDuty_B",
				"PCH_AcRealPowerSum", "PCH_AcVoltageCN", "PCH_AcVoltageLL", "PCH_pcbaId", "PCH_usageId",
			},
			"bmsSignalNames": {
				"BMS_LOG_tempOutOfBounds", "BMS_LOG_tempOutOfBoundsCharge",
				"BMS_kwhDischargeTotal", "BMS_LOG_ahDischargeTotal", "BMS_LOG_cacAvg",
				"BMS_LOG_numFullBattCycles", "BMS_LOG_overDischargeCounter", "BMS_LOG_selfDischargeRate",
			},
			"hvpSignalNames": {
				"HVP_PackTempMax", "HVP_PackTempMin", "HVP_ShuntTemperature",
				"HVP_SafetyBiDisconnectState", "HVP_a061_LockoutOnIsoFaultRetry",
			},
			"pwsSignalNames": {
				"PWS_ExtSwitch_State", "PWS_RSDSelfTest_Result", "PWS_RSDSelfTest_State", "PWS_RSD_State",
				"PWS_assemblyId", "PWS_pcbaId", "PWS_reversePolarityStrings",
			},
			"baggrSignalNames": {
				"BAGGR_ExpectedEnergyRemaining", "BAGGR_ExpectedFullPackEnergy", "BAGGR_OperationType",
			},
		},
		QueryRoleDeviceControllerFull: {
			"msaSignals": {
				"SYNC_Uptime",
			},
		},
	}
}

func mergeUniqueSignals(rawSlice any, extras []string) []string {
	var existing []string
	seen := make(map[string]bool)
	if items, ok := rawSlice.([]any); ok {
		for _, item := range items {
			if s, okS := item.(string); okS {
				existing = append(existing, s)
				seen[s] = true
			}
		}
	}
	for _, name := range extras {
		if !seen[name] {
			existing = append(existing, name)
			seen[name] = true
		}
	}

	return existing
}

func appendExtraSignals(role, bVal string) string {
	extras, ok := getExtraSignalNames()[role]
	if !ok || len(extras) == 0 {
		return bVal
	}
	var vars map[string]any
	if err := json.Unmarshal([]byte(bVal), &vars); err != nil {
		return bVal
	}
	for key, names := range extras {
		vars[key] = mergeUniqueSignals(vars[key], names)
	}
	encoded, err := json.Marshal(vars)
	if err != nil {
		return bVal
	}

	return string(encoded)
}

func loadQuerySet(data []byte, appendExtras bool) map[string]*Query {
	raw := make(map[string]queryJSONRecord)
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	out := make(map[string]*Query)
	for k, rec := range raw {
		code, _ := hex.DecodeString(rec.Code)
		signedBytes, _ := hex.DecodeString(rec.SignedBytes)
		bVal := rec.BValue
		if bVal == "" {
			bVal = "{}"
		}
		if appendExtras {
			bVal = appendExtraSignals(k, bVal)
		}
		out[k] = &Query{
			Text:        rec.Text,
			Code:        code,
			BValue:      bVal,
			Version:     rec.Version,
			SignedBytes: signedBytes,
		}
	}

	return out
}

func getV2026Role(role string) string {
	switch role {
	case QueryRoleDeviceControllerBasic, QueryRoleDeviceControllerFull:
		return "DeviceControllerQuery"
	case QueryRoleComponents:
		return "PW3Query"
	default:
		return role
	}
}

// GetQuery looks up a query by call-site role and API version.
func GetQuery(role string, version models.TEDAPIApiVersion) *Query {
	if version == models.TEDAPIVersion2026_06 {
		return v2026Queries[getV2026Role(role)]
	}

	return v2024Queries[role]
}

// GetQueryByName fetches a V2026_06 query directly by Tesla operation name.
func GetQueryByName(opName string) *Query {
	return v2026Queries[opName]
}
