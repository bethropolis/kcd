package main

import (
	"testing"

	"github.com/bethropolis/kcd/internal/device"
)

func TestFilterDevices(t *testing.T) {
	online := device.DeviceInfo{ID: "on", State: device.StatePaired, Connected: true}
	offline := device.DeviceInfo{ID: "off", State: device.StatePaired, Connected: false}
	stranger := device.DeviceInfo{ID: "stranger", State: device.StateUnpaired, Connected: true}
	all := []device.DeviceInfo{online, offline, stranger}

	ids := func(devs []device.DeviceInfo) []string {
		var out []string
		for _, d := range devs {
			out = append(out, d.ID)
		}
		return out
	}
	same := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	for _, tc := range []struct {
		name      string
		paired    bool
		connected bool
		want      []string
	}{
		{"no flags passes everything through", false, false, []string{"on", "off", "stranger"}},
		{"paired keeps offline pairs, hides strangers", true, false, []string{"on", "off"}},
		{"connected requires paired and live", false, true, []string{"on"}},
		{"both flags intersect", true, true, []string{"on"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(filterDevices(all, tc.paired, tc.connected)); !same(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
