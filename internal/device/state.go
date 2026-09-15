// Package device manages KDE Connect devices, their connections, state, and routing.
package device

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// PairingState represents the current pairing status with a device.
type PairingState int

const (
	StateUnknown             PairingState = iota
	StatePairRequested                    // We sent a pair request, waiting for response
	StatePairRequestedByPeer              // Peer sent a pair request, waiting for user to accept
	StatePaired
	StateUnpaired
)

func (s PairingState) String() string {
	switch s {
	case StateUnknown:
		return "UNKNOWN"
	case StatePairRequested:
		return "PAIR_REQUESTED"
	case StatePairRequestedByPeer:
		return "PAIR_REQUESTED_BY_PEER"
	case StatePaired:
		return "PAIRED"
	case StateUnpaired:
		return "UNPAIRED"
	default:
		return "INVALID"
	}
}

// MarshalJSON serializes the state as a string for readability.
func (s PairingState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON handles both string (PAIRED) and integer (3) formats.
func (s *PairingState) UnmarshalJSON(data []byte) error {
	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*s = PairingState(asInt)
		return nil
	}

	var asStr string
	if err := json.Unmarshal(data, &asStr); err != nil {
		return err
	}

	switch asStr {
	case "UNKNOWN":
		*s = StateUnknown
	case "PAIR_REQUESTED":
		*s = StatePairRequested
	case "PAIR_REQUESTED_BY_PEER":
		*s = StatePairRequestedByPeer
	case "PAIRED":
		*s = StatePaired
	case "UNPAIRED":
		*s = StateUnpaired
	default:
		*s = StateUnknown
	}
	return nil
}

// DeviceInfo represents the persistent state of a device.
type DeviceInfo struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Type      string       `json:"type"`
	State     PairingState `json:"state"`
	CertFP    string       `json:"cert_fp"`
	LastSeen  time.Time    `json:"last_seen"`
	Connected bool         `json:"connected"`
	// LastIP/LastPort is the dial target from the most recent successful
	// connection, so paired auto-dial survives daemon restarts. Both are
	// validated on load (the file is user-editable); garbage is dropped.
	LastIP   string `json:"last_ip,omitempty"`
	LastPort int    `json:"last_port,omitempty"`
}

// DialTarget returns the persisted dial target for auto-dial after a
// restart. A garbage IP yields nil (no target); an out-of-range port
// yields 0 and callers fall back to the default port. The state file is
// user-editable, so nothing here is trusted blindly.
func (info DeviceInfo) DialTarget() (net.IP, int) {
	ip := net.ParseIP(info.LastIP)
	if ip == nil {
		return nil, 0
	}
	if info.LastPort <= 0 || info.LastPort > 65535 {
		return ip, 0
	}
	return ip, info.LastPort
}

// LoadDevices loads known devices from the given JSON file path.
func LoadDevices(path string) ([]DeviceInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []DeviceInfo{}, nil
		}
		return nil, fmt.Errorf("device: read %s: %w", path, err)
	}

	var devices []DeviceInfo
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, fmt.Errorf("device: parse %s: %w", path, err)
	}
	return devices, nil
}

// SaveDevices writes the list of known devices to the given JSON file path.
func SaveDevices(path string, devices []DeviceInfo) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("device: create dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return fmt.Errorf("device: marshal devices: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("device: open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("device: write %s: %w", path, err)
	}
	return nil
}
