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
	// Shell metacharacters must not survive sanitization.
	for _, tc := range []struct{ in, want string }{
		{"`id`", "id"},
		{"${HOME}", "HOME"},
		{"$(whoami)", "whoami"},
		{"a|cat /etc/passwd", "acat etcpasswd"},
		{"a & rm", "a  rm"},
		{`back\slash`, "backslash"},
		{"a*b?c", "abc"},
	} {
		if got := SanitizeDeviceName(tc.in); got != tc.want {
			t.Errorf("SanitizeDeviceName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Control characters (NUL, newlines, ESC, bidi overrides) are dropped.
	for _, in := range []string{
		"A\x00B",
		"Phone\nFAKE-INFO",
		"Phone\r\nspam",
		"\x1b]0;pwned\x07Phone",
		"ab\u202ec routine",
		"tab\there",
	} {
		got := SanitizeDeviceName(in)
		for _, r := range got {
			if r == 0 || r == '\n' || r == '\r' || r == '\x1b' || r == '\u202e' || r == '\t' {
				t.Errorf("SanitizeDeviceName(%q) keeps control rune: got %q", in, got)
			}
		}
		if !utf8.ValidString(got) {
			t.Errorf("SanitizeDeviceName(%q) invalid UTF-8: %q", in, got)
		}
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

func TestDisplayName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Pixel 8 Pro", "Pixel 8 Pro"},
		{"Café", "Café"},
		{"A\nB", "A B"},
		{"A\r\nB", "A  B"},
		{"A\tB", "A B"},
		{"\x1b[2JPhone", "Phone"},
		{"\x1b[31mRed\x1b[0m", "Red"},
		{"\x1b]0;title\x07Phone", "Phone"},
	}
	for _, tc := range cases {
		if got := DisplayName(tc.in); got != tc.want {
			t.Errorf("DisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
