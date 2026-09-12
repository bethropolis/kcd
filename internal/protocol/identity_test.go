package protocol

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDecodeDeviceName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain ascii", "Pixel 8 Pro", "Pixel 8 Pro"},
		{"real utf8 untouched", "Café", "Café"},
		{"decimal escapes", `Caf\195\169`, "Café"},
		{"leading decimal escape", `\195\169clair`, "éclair"},
		{"decimal escape mid-string", `T\195\169st`, "Tést"},
		{"truncated tail escape dropped", `Phone-\195`, "Phone-"},
		{"out of range kept", `val\999end`, `val\999end`},
		{"invalid escape kept", `back\slash`, `back\slash`},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecodeDeviceName(tc.input); got != tc.want {
				t.Errorf("DecodeDeviceName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeDeviceName(t *testing.T) {
	if got := SanitizeDeviceName(`Caf\195\169`); got != "Café" {
		t.Errorf("escaped name not decoded: got %q", got)
	}
	if got := SanitizeDeviceName("Phone (Work); rm -rf"); got != "Phone Work rm -rf" {
		t.Errorf("injection chars not stripped: got %q", got)
	}
	// Multibyte truncation must never split a rune nor exceed the cap.
	long := strings.Repeat("Ö", 40) // 80 bytes
	got := SanitizeDeviceName(long)
	if len(got) > MaxDeviceNameLength {
		t.Errorf("truncated name exceeds cap: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated name is invalid UTF-8: %q", got)
	}
	if n := len([]rune(got)); n != MaxDeviceNameLength/2 {
		t.Errorf("expected 16 Ö runes (32 bytes), got %d runes", n)
	}
}
