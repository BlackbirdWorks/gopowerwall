package models

// AuthMode represents authentication method ("cookie", "token", "basic", "bearer").
type AuthMode string

const (
	AuthModeCookie AuthMode = "cookie"
	AuthModeToken  AuthMode = "token"
	AuthModeBasic  AuthMode = "basic"
	AuthModeBearer AuthMode = "bearer"
)

// LocalAuthCredentials represents cached session in .powerwall.
type LocalAuthCredentials struct {
	AuthCookie string `json:"AuthCookie,omitempty"`
	UserRecord string `json:"UserRecord,omitempty"`
}

// TokenData represents OAuth2 token payload.
type TokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

// CloudAccountEntry represents an account record in .pypowerwall.auth.
type CloudAccountEntry struct {
	SSO TokenData `json:"sso"`
}

// FleetAPIConfig represents credentials in .pypowerwall.fleetapi.
type FleetAPIConfig struct {
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	Domain       string    `json:"domain"`
	SiteID       string    `json:"siteid,omitempty"`
	Token        TokenData `json:"token"`
}
