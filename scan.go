package gopowerwall

import (
	"context"
	"os"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/scan"
)

// ScanOptions re-exports models.ScanOptions.
type ScanOptions = models.ScanOptions

// DiscoveredDevice re-exports models.DiscoveredDevice.
type DiscoveredDevice = models.DiscoveredDevice

// Scan re-exports scan.Scan.
func Scan(opts ScanOptions) ([]DiscoveredDevice, error) {
	return scan.Scan(context.Background(), opts, os.Stdout)
}
