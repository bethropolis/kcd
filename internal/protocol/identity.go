package protocol

import (
	"regexp"
)

// ProtocolVersion is the KDE Connect protocol version we advertise.
// KDE Connect uses version 8 which requires post-TLS identity exchange.
const ProtocolVersion = 8

// TypeIdentity is the packet type for identity exchange.
const TypeIdentity = "kdeconnect.identity"

var invalidNameChars = regexp.MustCompile(`['";:.!?()\[\]<>]`)

const MaxDeviceNameLength = 32

// SanitizeDeviceName strips potential terminal injection and XSS characters
// and truncates the name to a maximum length per the KDE Connect specification.
func SanitizeDeviceName(name string) string {
	clean := invalidNameChars.ReplaceAllString(name, "")
	if len(clean) > MaxDeviceNameLength {
		clean = clean[:MaxDeviceNameLength]
	}
	return clean
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
