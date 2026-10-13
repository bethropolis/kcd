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
	// LastIP/LastPort is the dial target from the last good connection, so
	// auto-dial survives restarts. Validated on load; garbage is dropped.
	LastIP   string `json:"last_ip,omitempty"`
	LastPort int    `json:"last_port,omitempty"`
}

// Info builds the serialisable view of a device, matching what the devices
// command and state.snapshot expose.
func (d *Device) Info() DeviceInfo {
	info := DeviceInfo{
		ID:        d.ID(),
		Name:      d.Name(),
		Type:      d.Type,
		State:     d.State(),
		CertFP:    d.CertFP,
		LastSeen:  d.LastSeen(),
		Connected: d.IsConnected(),
		LastPort:  d.LastPort(),
	}
	// net.IP.String() on nil renders "<nil>" -- store empty instead.
	if ip := d.LastIP(); ip != nil {
		info.LastIP = ip.String()
	}
	return info
}

// DialTarget returns the persisted auto-dial target; garbage yields nil/0
// with default-port fallback. The state file is user-editable: trust nothing.
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

// SaveDevices writes known devices to path, renaming a temp file into place
// so a crash leaves the old list or the new one, never a truncation.
func SaveDevices(path string, devices []DeviceInfo) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("device: create dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return fmt.Errorf("device: marshal devices: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("device: temp %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("device: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("device: sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("device: close %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("device: rename %s: %w", path, err)
	}
	return nil
}
