package wiz

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/caseymrm/wizcamera/pkg/light"
)

func TestPilotForBusy(t *testing.T) {
	b, err := json.Marshal(request{Method: "setPilot", Params: pilotFor(light.Busy)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"method":"setPilot","params":{"state":true,"r":255,"g":0,"b":0,"dimming":100}}`
	if string(b) != want {
		t.Errorf("busy payload = %s, want %s", b, want)
	}
}

func TestPilotForFree(t *testing.T) {
	b, err := json.Marshal(request{Method: "setPilot", Params: pilotFor(light.Free)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"method":"setPilot","params":{"state":false}}`
	if string(b) != want {
		t.Errorf("free payload = %s, want %s", b, want)
	}
}

func TestFriendlyName(t *testing.T) {
	for _, tc := range []struct {
		module, mac, want string
	}{
		{"ESP01_SHRGB1C_31", "a8bb50123456", "Wiz color bulb 123456"},
		{"ESP56_SHTW3_01", "a8bb50123456", "Wiz tunable white bulb 123456"},
		{"ESP25_SOCKET_01", "a8bb50123456", "Wiz smart plug 123456"},
		{"", "abc", "Wiz light ABC"},
	} {
		if got := friendlyName(tc.module, tc.mac); got != tc.want {
			t.Errorf("friendlyName(%q, %q) = %q, want %q", tc.module, tc.mac, got, tc.want)
		}
	}
}

func TestProviderRegistered(t *testing.T) {
	l, err := light.Connect(light.Config{Provider: "wiz", ID: "a8bb50123456", Address: "192.168.1.242"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := l.Config()
	if cfg.ID != "a8bb50123456" || cfg.Address != "192.168.1.242" || cfg.Provider != "wiz" {
		t.Errorf("round-tripped config = %+v", cfg)
	}
}

func TestConnectUnknownProvider(t *testing.T) {
	if _, err := light.Connect(light.Config{Provider: "nope"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestDiscoverRuns(t *testing.T) {
	// No bulbs on this network; just verify the broadcast socket setup
	// works and the scan terminates cleanly.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := Discover(ctx); err != nil {
		t.Fatalf("Discover: %v", err)
	}
}
