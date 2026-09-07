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
	v2024Queries = loadQuerySet(v2024QueriesJSON)
	//nolint:gochecknoglobals // Embedded queries are loaded once at package init.
	v2026Queries = loadQuerySet(v2026QueriesJSON)
)

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

func loadQuerySet(data []byte) map[string]*Query {
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
