package powerwall

import (
	"context"
	"io"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/powerwall/scan"
)

// ScanOptions configures a network scan for Powerwall gateways.
type ScanOptions = models.ScanOptions

// DiscoveredDevice describes a gateway found by [Scan].
type DiscoveredDevice = models.DiscoveredDevice

// Scan searches the network described by opts for Powerwall gateways,
// reporting progress to w. The caller controls cancellation through ctx and
// where progress is written, so the scan is usable from a server as well as a
// terminal; pass io.Discard to suppress progress entirely.
func Scan(ctx context.Context, opts ScanOptions, w io.Writer) ([]DiscoveredDevice, error) {
	return scan.Scan(ctx, opts, w)
}
