package ipc

import (
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
)

// BatteryStatus mirrors the battery state for embedding in summaries.
type BatteryStatus struct {
	Charge   int  `json:"charge"`
	Charging bool `json:"charging"`
}

// MediaState is the cached now-playing state plus its age, so clients can
// apply their own staleness rules instead of the daemon hiding paused media.
type MediaState struct {
	mpris.NowPlaying
	MediaAgeMs int64 `json:"mediaAgeMs"`
}

// DeviceSummary enriches DeviceInfo with cached sub-states for clients.
// It is IPC-only: DeviceInfo stays the on-disk shape, so persisted state
// never grows these fields. All sub-states are omitempty.
type DeviceSummary struct {
	device.DeviceInfo
	Battery *BatteryStatus                 `json:"battery,omitempty"`
	Media   *MediaState                    `json:"media,omitempty"`
	Signal  *connectivity.ConnectivityBody `json:"signal,omitempty"`
}

// SummarizeDevice builds the enriched view of one device. Absent plugins
// or missing caches simply omit their section.
func SummarizeDevice(dev *device.Device, plugins *plugin.Registry) DeviceSummary {
	sum := DeviceSummary{
		DeviceInfo: device.DeviceInfo{
			ID:        dev.ID(),
			Name:      dev.Name(),
			Type:      dev.Type,
			State:     dev.State(),
			LastSeen:  dev.LastSeen(),
			Connected: dev.IsConnected(),
		},
	}

	charge, charging := dev.GetBattery()
	sum.Battery = &BatteryStatus{Charge: charge, Charging: charging}

	if plugins == nil {
		return sum
	}
	if pl, ok := plugins.GetByName("MPRIS"); ok {
		mp := pl.(*mpris.MPRISPlugin)
		if state := mp.RemoteState(dev.ID()); state != nil {
			sum.Media = &MediaState{
				NowPlaying: *state,
				MediaAgeMs: mp.RemoteStateAge(dev.ID()).Milliseconds(),
			}
		}
	}
	if pl, ok := plugins.GetByName("Connectivity"); ok {
		if report, ok := pl.(*connectivity.ConnectivityPlugin).Report(dev.ID()); ok {
			r := report
			sum.Signal = &r
		}
	}
	return sum
}

// SnapshotPayload is the state.snapshot event payload: every known device
// (online and offline) with its cached sub-states, so clients boot with
// full state from a single watch connection.
type SnapshotPayload struct {
	Devices []DeviceSummary `json:"devices"`
}

// BuildSnapshot assembles the full-state payload for the state.snapshot event.
func BuildSnapshot(devices *device.Registry, plugins *plugin.Registry) SnapshotPayload {
	snap := SnapshotPayload{Devices: []DeviceSummary{}}
	if devices == nil {
		return snap
	}
	for _, dev := range devices.List() {
		snap.Devices = append(snap.Devices, SummarizeDevice(dev, plugins))
	}
	return snap
}
