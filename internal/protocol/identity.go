package protocol

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProtocolVersion is the KDE Connect protocol version we advertise.
// KDE Connect uses version 8 which requires post-TLS identity exchange.
const ProtocolVersion = 8

// TypeIdentity is the packet type for identity exchange.
const TypeIdentity = "kdeconnect.identity"

// invalidNameChars strips shell metacharacters and markup that could be
// abused via copy-paste, unquoted shell expansion, or log/terminal rendering.
// Control and format characters (NUL, newlines, ANSI, bidi overrides) are
// dropped separately by stripControlRunes below.
var invalidNameChars = regexp.MustCompile("[`'\";:.!?()\\[\\]<>$&|\\\\*~#%^{}=/]")

const MaxDeviceNameLength = 32

// SanitizeDeviceName strips potential terminal injection and XSS characters
// and truncates the name to a maximum length per the KDE Connect specification.
//
// Some senders (notably phones with non-ASCII names) transmit the name with
// decimal byte escapes already applied (e.g. "Caf\195\169" instead of
// "Café"). Decode those first so listings show real UTF-8.
// Truncation is rune-aware so multi-byte characters are never split.
func SanitizeDeviceName(name string) string {
	clean := DecodeDeviceName(name)
	clean = stripControlRunes(clean)
	clean = invalidNameChars.ReplaceAllString(clean, "")
	if len(clean) > MaxDeviceNameLength {
		clean = truncateRunes(clean, MaxDeviceNameLength)
	}
	return clean
}

// stripControlRunes drops Unicode control (Cc, incl. NUL, CR, LF, ESC) and
// format (Cf, incl. bidi overrides U+202A-U+202E, U+2066-U+2069) runes.
// These are invisible, break C-backed consumers (NUL truncation), enable
// log/terminal forgery (newlines, ESC sequences), and visual spoofing
// (bidi overrides) — none are legitimate in a display name.
func stripControlRunes(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// ansiEscape matches terminal escape sequences: CSI (ESC [ ... letter),
// OSC (ESC ] ... BEL or ESC \), and lone ESC / two-byte sequences.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-_]")

// DisplayName renders a stored device name for terminal or notification
// sinks: ANSI escapes are removed and CR/LF/TAB become spaces so a hostile
// name can't forge log lines, break CLI tables, or inject terminal codes.
// Storage and JSON keep the sanitized (not display) form.
func DisplayName(name string) string {
	s := ansiEscape.ReplaceAllString(name, "")
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// decimalEscape matches a backslash followed by exactly three decimal
// digits, e.g. "\195". Some senders emit non-ASCII name bytes this way
// ("Caf\195\169" for "Café") instead of raw UTF-8.
var decimalEscape = regexp.MustCompile(`\\([0-9]{3})`)

// DecodeDeviceName converts decimal byte escapes (\DDD) back to the bytes
// they represent, yielding real UTF-8. Strings without backslashes — the
// common case — are returned untouched, as are strings with no valid
// escape runs (e.g. a literal "back\slash"). If decoding leaves a broken
// tail (the old byte-based truncation could chop an escape in half), the
// invalid bytes are dropped rather than keeping mojibake.
func DecodeDeviceName(name string) string {
	if name == "" || !containsBackslash(name) {
		return name
	}
	decoded := decimalEscape.ReplaceAllStringFunc(name, func(m string) string {
		d := m[1:]
		v := int(d[0]-'0')*100 + int(d[1]-'0')*10 + int(d[2]-'0')
		if v > 255 {
			return m
		}
		return string([]byte{byte(v)})
	})
	if decoded == name {
		return name
	}
	if !utf8.ValidString(decoded) {
		decoded = strings.ToValidUTF8(decoded, "")
	}
	return decoded
}

func containsBackslash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			return true
		}
	}
	return false
}

// truncateRunes cuts s to at most maxBytes without splitting a UTF-8 rune.
func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		cut = i
	}
	return s[:cut]
}

// IdentityBody contains the fields of an identity packet body.
type IdentityBody struct {
	DeviceID              string   `json:"deviceId"`
	DeviceName            string   `json:"deviceName"`
	DeviceType            string   `json:"deviceType"`
	ProtocolVersion       int      `json:"protocolVersion"`
	TCPPort               int      `json:"tcpPort"`
	IncomingCapabilities  []string `json:"incomingCapabilities,omitempty"`
	OutgoingCapabilities  []string `json:"outgoingCapabilities,omitempty"`
	TargetDeviceID        string   `json:"targetDeviceId,omitempty"`
	TargetProtocolVersion int      `json:"targetProtocolVersion,omitempty"`
}

// NewIdentityPacket creates a fully-formed identity packet ready for the wire.
func NewIdentityPacket(
	deviceID, deviceName, deviceType string,
	tcpPort int,
	incoming, outgoing []string,
) (*Packet, error) {
	body := IdentityBody{
		DeviceID:             deviceID,
		DeviceName:           SanitizeDeviceName(deviceName),
		DeviceType:           deviceType,
		ProtocolVersion:      ProtocolVersion,
		TCPPort:              tcpPort,
		IncomingCapabilities: incoming,
		OutgoingCapabilities: outgoing,
	}

	pkt, err := NewPacket(TypeIdentity, body)
	if err != nil {
		return nil, err
	}
	// Identity packets traditionally use id=0 in some implementations,
	// but using a timestamp is also fine. We keep the timestamp default.
	return pkt, nil
}
