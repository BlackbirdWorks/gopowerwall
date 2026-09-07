package gopowerwall_test

import (
	"math"
	"testing"

	"github.com/blackbirdworks/gopowerwall"
)

func TestPowerwallDisconnectedDegradation(t *testing.T) {
	// Create disconnected powerwall (invalid host)
	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost("127.0.0.1:9"), // non-routable port
		gopowerwall.WithPassword("test"),
		gopowerwall.WithCloudMode(false),
	)
	if err != nil {
		t.Fatalf("Unexpected New() err: %v", err)
	}

	// Must report disconnected
	if pw.IsConnected() {
		t.Errorf("Expected IsConnected() = false")
	}

	// Facade methods must never panic and gracefully return nil or stub defaults
	if p := pw.Poll(t.Context(), "/api/status"); p != nil {
		t.Errorf("Expected Poll() = nil, got %v", p)
	}
	if v := pw.Level(t.Context()); v != nil {
		t.Errorf("Expected Level() = nil, got %v", v)
	}
	if p := pw.Power(t.Context()); p.Site != 0 || p.Battery != 0 {
		t.Errorf("Expected zero Power(), got %v", p)
	}
	if s := pw.Site(t.Context(), true); s != nil {
		t.Errorf("Expected Site(true) = nil, got %v", s)
	}
	if sol := pw.Solar(t.Context(), true); sol != nil {
		t.Errorf("Expected Solar(true) = nil, got %v", sol)
	}
	if b := pw.Battery(t.Context(), true); b != nil {
		t.Errorf("Expected Battery(true) = nil, got %v", b)
	}
	if l := pw.Load(t.Context(), true); l != nil {
		t.Errorf("Expected Load(true) = nil, got %v", l)
	}
	if g := pw.Grid(t.Context(), true); g != nil {
		t.Errorf("Expected Grid(true) = nil, got %v", g)
	}
	if h := pw.Home(t.Context(), true); h != nil {
		t.Errorf("Expected Home(true) = nil, got %v", h)
	}
	if vit, err := pw.Vitals(t.Context()); err == nil && len(vit.Devices) > 0 {
		t.Errorf("Expected empty Vitals(), got %v", vit)
	}
	if str := pw.Strings(t.Context()); len(str.Strings) > 0 {
		t.Errorf("Expected empty Strings(), got %v", str)
	}
	if d := pw.Din(t.Context()); d != nil {
		t.Errorf("Expected Din() = nil, got %v", d)
	}
	if u := pw.Uptime(t.Context()); u != nil {
		t.Errorf("Expected Uptime() = nil, got %v", u)
	}
	if sn := pw.SiteName(t.Context()); sn != nil {
		t.Errorf("Expected SiteName() = nil, got %v", sn)
	}
	if tm := pw.GetTimeRemaining(t.Context()); tm != nil {
		t.Errorf("Expected GetTimeRemaining() = nil, got %v", tm)
	}
	if r := pw.GetReserve(t.Context()); r != nil {
		t.Errorf("Expected GetReserve() = nil, got %v", r)
	}
	if m := pw.GetMode(t.Context()); m != nil {
		t.Errorf("Expected GetMode() = nil, got %v", m)
	}
	if gc := pw.GetGridCharging(t.Context()); gc != nil {
		t.Errorf("Expected GetGridCharging() = nil, got %v", gc)
	}
	if ge := pw.GetGridExport(t.Context()); ge != nil {
		t.Errorf("Expected GetGridExport() = nil, got %v", ge)
	}
}

func TestScaleFormula(t *testing.T) {
	// Formula: (level / 0.95) - (5.0 / 0.95)
	// For level 100: (100 / 0.95) - (5 / 0.95) = 95 / 0.95 = 100.0
	// For level 5.0: (5 / 0.95) - (5 / 0.95) = 0.0
	scaleFormula := func(level float64) float64 {
		return (level / 0.95) - (5.0 / 0.95)
	}

	if val := scaleFormula(100.0); math.Abs(val-100.0) > 0.001 {
		t.Errorf("scaleFormula(100) = %v, want 100.0", val)
	}
	if val := scaleFormula(5.0); math.Abs(val-0.0) > 0.001 {
		t.Errorf("scaleFormula(5) = %v, want 0.0", val)
	}
}
