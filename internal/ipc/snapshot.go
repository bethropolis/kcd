package ipc

import (
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/connectivity"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
	"github.com/bethropolis/kcd/internal/plugins/sftp"
)

// BatteryStatus mirrors the battery state for embedding in summaries.
type BatteryStatus struct {
	Charge       int   `json:"charge"`
	Charging     bool  `json:"charging"`
	BatteryAgeMs int64 `json:"batteryAgeMs"`
}

// MediaState is the cached now-playing state plus its age, so clients can
// apply their own staleness rules instead of the daemon hiding paused media.
type MediaState struct {
	mpris.NowPlaying
	MediaAgeMs int64 `json:"mediaAgeMs"`
}

// DeviceSummary enriches DeviceInfo with cached sub-states for clients only;
// DeviceInfo stays the on-disk shape so persisted state never grows them.
type DeviceSummary struct {
	device.DeviceInfo
	Battery *BatteryStatus                 `json:"battery,omitempty"`
	Media   *MediaState                    `json:"media,omitempty"`
	Signal  *connectivity.ConnectivityBody `json:"signal,omitempty"`
	Sftp    *SftpMountState                `json:"sftp,omitempty"`
}

// SftpMountState carries a device's current SFTP mount state so clients can
// render a mount toggle from a snapshot alone.
type SftpMountState struct {
	Mounted    bool   `json:"mounted"`
	MountPoint string `json:"mountPoint,omitempty"`
}

// SummarizeDevice builds the enriched view of one device. Absent plugins
// or missing caches simply omit their section.
func SummarizeDevice(dev *device.Device, plugins *plugin.Registry) DeviceSummary {
	// Connected implies seen now: last_seen stamps only at connect and on
	// sightings (which skip connected devices), or live phones read "Xm ago".
	lastSeen := dev.LastSeen()
	if dev.IsConnected() {
		lastSeen = time.Now()
	}
	sum := DeviceSummary{
		DeviceInfo: device.DeviceInfo{
			ID:        dev.ID(),
			Name:      dev.Name(),
			Type:      dev.Type,
			State:     dev.State(),
			LastSeen:  lastSeen,
			Connected: dev.IsConnected(),
		},
	}

	// Omit battery until the first real packet: the zero values are not
	// a 0% measurement (see Device.HasBattery).
	if dev.HasBattery() {
		charge, charging := dev.GetBattery()
		sum.Battery = &BatteryStatus{
			Charge:       charge,
			Charging:     charging,
			BatteryAgeMs: dev.BatteryAge().Milliseconds(),
		}
	}

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
	// Report mounts only when they exist: advertising a permanent "not
	// mounted" would force clients to special-case it.
	if pl, ok := plugins.GetByName("SFTP"); ok {
		if mp := pl.(*sftp.SftpPlugin).MountedPath(dev.ID()); mp != "" {
			sum.Sftp = &SftpMountState{Mounted: true, MountPoint: mp}
		}
	}
	return sum
}

// SnapshotPayload is the state.snapshot payload: every known device with
// cached sub-states, so clients boot complete from one watch connection.
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
