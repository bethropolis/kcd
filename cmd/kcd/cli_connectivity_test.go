package main

import (
	"testing"

	"github.com/bethropolis/kcd/internal/plugins/connectivity"
)

func TestFormatConnectivity(t *testing.T) {
	cases := []struct {
		name string
		body connectivity.ConnectivityBody
		want []string
	}{
		{
			name: "empty",
			body: connectivity.ConnectivityBody{},
			want: []string{"No signal data reported"},
		},
		{
			name: "single sim detailed type",
			body: connectivity.ConnectivityBody{SignalStrengths: map[string]connectivity.SignalStrength{
				"0": {NetworkType: "LTE", NetworkDetailedType: "LTE", SignalStrength: 3},
			}},
			want: []string{"LTE [███░] (3/4)"},
		},
		{
			name: "falls back to network type",
			body: connectivity.ConnectivityBody{SignalStrengths: map[string]connectivity.SignalStrength{
				"0": {NetworkType: "5G", SignalStrength: 4},
			}},
			want: []string{"5G [████] (4/4)"},
		},
		{
			name: "clamps out of range",
			body: connectivity.ConnectivityBody{SignalStrengths: map[string]connectivity.SignalStrength{
				"0": {NetworkType: "GSM", SignalStrength: 9},
			}},
			want: []string{"GSM [████] (4/4)"},
		},
		{
			name: "dual sim primary first",
			body: connectivity.ConnectivityBody{SignalStrengths: map[string]connectivity.SignalStrength{
				"1": {NetworkType: "EDGE", SignalStrength: 2},
				"0": {NetworkType: "LTE", SignalStrength: 4},
			}},
			want: []string{"SIM 0: LTE [████] (4/4)", "SIM 1: EDGE [██░░] (2/4)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatConnectivity(tc.body)
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
