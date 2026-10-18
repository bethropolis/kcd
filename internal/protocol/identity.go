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

// invalidNameChars strips shell metacharacters and markup abusable via
// copy-paste or unquoted expansion; control/format runes go separately below.
var invalidNameChars = regexp.MustCompile("[`'\";:.!?()\\[\\]<>$&|\\\\*~#%^{}=/]")

const MaxDeviceNameLength = 32

// SanitizeDeviceName strips terminal/XSS characters and truncates rune-aware
// to the spec length, decoding the decimal byte escapes phones may send first.
func SanitizeDeviceName(name string) string {
	clean := DecodeDeviceName(name)
	clean = stripControlRunes(clean)
	clean = invalidNameChars.ReplaceAllString(clean, "")
	if len(clean) > MaxDeviceNameLength {
		clean = truncateRunes(clean, MaxDeviceNameLength)
	}
	return clean
}

// stripControlRunes drops control and format runes: invisible, truncating,
// terminal-forging, and visually spoofing characters have no place in names.
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

// DisplayName renders a stored name for terminals/notifications: escapes out,
// CR/LF/TAB to spaces, so hostile names forge nothing. Storage keeps sanitized.
func DisplayName(name string) string {
	s := ansiEscape.ReplaceAllString(name, "")
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// decimalEscape matches a backslash plus exactly three decimal digits: the
// form some senders use for non-ASCII name bytes instead of raw UTF-8.
var decimalEscape = regexp.MustCompile(`\\([0-9]{3})`)

// DecodeDeviceName converts decimal byte escapes (\DDD) back to bytes,
// passing through strings without valid escapes and dropping broken tails.
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
	TargetProtocolVersion FlexInt  `json:"targetProtocolVersion,omitempty"`
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
