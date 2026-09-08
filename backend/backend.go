// Package backend provides core backend definitions and models for Tesla Powerwall connections.
package backend

import (
	"time"

	"github.com/blackbirdworks/gopowerwall/models"
)

// Config holds common backend configuration settings.
type Config struct {
	Host        string
	Password    string
	Email       string
	Timezone    string
	AuthPath    string
	SiteID      string
	AuthMode    models.AuthMode
	Timeout     time.Duration
	CacheTTL    time.Duration
	PoolMaxSize int
}
