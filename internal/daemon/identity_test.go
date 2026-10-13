package daemon

import (
	"encoding/json"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
)

// The configured device type must reach the advertised identity: it was
// hardcoded to "desktop" while config validated and documented the field.
func TestBuildIdentityCarriesDeviceType(t *testing.T) {
	for _, typ := range []string{"desktop", "laptop", "phone", "tablet", "tv"} {
		cfg := config.Defaults()
		cfg.DeviceType = typ
		pkt, err := buildIdentity(cfg, nil, nil)
		if err != nil {
			t.Fatalf("buildIdentity(%s): %v", typ, err)
		}
		var body struct {
			DeviceType string `json:"deviceType"`
		}
		if err := json.Unmarshal(pkt.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.DeviceType != typ {
			t.Errorf("deviceType = %q, want %q", body.DeviceType, typ)
		}
	}
}
