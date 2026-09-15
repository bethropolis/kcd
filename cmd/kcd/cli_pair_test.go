package main

import (
	"testing"

	"github.com/bethropolis/kcd/internal/ipc"
)

func TestNormalizeFingerprint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abcdef1234", "abcdef1234"},
		{"AB:CD:EF:12:34", "abcdef1234"},
		{"ab cd ef 12 34", "abcdef1234"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeFingerprint(tc.in); got != tc.want {
			t.Errorf("normalizeFingerprint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCheckPairCandidate(t *testing.T) {
	fp := "aa:bb:cc:dd"
	candidate := &ipc.PairListenResult{DeviceID: "dev1", Fingerprint: "aabbccdd"}
	known := map[string]bool{"dev1": true}

	if err := checkPairCandidate("", false, candidate, nil); err != nil {
		t.Errorf("no constraints should accept: %v", err)
	}
	if err := checkPairCandidate(fp, false, candidate, nil); err != nil {
		t.Errorf("matching fingerprint should accept: %v", err)
	}
	if err := checkPairCandidate("00:11:22:33", false, candidate, nil); err == nil {
		t.Error("mismatched fingerprint should refuse")
	}
	if err := checkPairCandidate(fp, false, &ipc.PairListenResult{DeviceID: "dev1"}, nil); err == nil {
		t.Error("missing candidate fingerprint should refuse when one is expected")
	}
	if err := checkPairCandidate("", true, candidate, known); err != nil {
		t.Errorf("known device should accept with known-only: %v", err)
	}
	if err := checkPairCandidate("", true, &ipc.PairListenResult{DeviceID: "stranger"}, known); err == nil {
		t.Error("unknown device should refuse with known-only")
	}
	// Nil known set behaves as "nothing known".
	if err := checkPairCandidate("", true, candidate, nil); err == nil {
		t.Error("empty known set should refuse with known-only")
	}
}
