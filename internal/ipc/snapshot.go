package ipc

import (
	"encoding/json"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
)

// BatteryStatus mirrors the battery state for embedding in summaries.
type BatteryStatus struct {
	Charge       int   `json:"charge"`
	Charging     bool  `json:"charging"`
	BatteryAgeMs int64 `json:"batteryAgeMs"`
}

// NowPlaying represents current media playback details in a device summary.
type NowPlaying struct {
	Player         string `json:"player"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	AlbumArtUrl    string `json:"albumArtUrl"`
	ArtPending     bool   `json:"artPending,omitempty"`
	Url            string `json:"url,omitempty"`
	Length         int64  `json:"length"`
	Pos            int64  `json:"pos"`
	PosAnchorMs    int64  `json:"posAnchorMs,omitempty"`
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume"`
	CanControl     bool   `json:"canControl"`
	CanGoNext      bool   `json:"canGoNext"`
	CanGoPrevious  bool   `json:"canGoPrevious"`
	CanPause       bool   `json:"canPause"`
	CanPlay        bool   `json:"canPlay"`
	CanSeek        bool   `json:"canSeek"`
	PlaybackStatus string `json:"playbackStatus"`
	Shuffle        *bool  `json:"shuffle,omitempty"`
	LoopStatus     string `json:"loopStatus,omitempty"`
}

// MediaState is the cached now-playing state plus its age, so clients can
// apply their own staleness rules instead of the daemon hiding paused media.
type MediaState struct {
	NowPlaying
	MediaAgeMs int64 `json:"mediaAgeMs"`
}

// SignalStrength represents cellular signal status for a subscription.
type SignalStrength struct {
	NetworkType         string `json:"networkType"`
	NetworkDetailedType string `json:"networkDetailedType,omitempty"`
	SignalStrength      int    `json:"signalStrength"`
}

// SignalState represents cellular connectivity information in a device summary.
type SignalState struct {
	SignalStrengths map[string]SignalStrength `json:"signalStrengths"`
}

// DeviceSummary enriches DeviceInfo with cached sub-states for clients only;
// DeviceInfo stays the on-disk shape so persisted state never grows them.
type DeviceSummary struct {
	device.DeviceInfo
	Battery *BatteryStatus  `json:"battery,omitempty"`
	Media   *MediaState     `json:"media,omitempty"`
	Signal  *SignalState    `json:"signal,omitempty"`
	Sftp    *SftpMountState `json:"sftp,omitempty"`
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

	for _, pl := range plugins.All() {
		if ms, ok := pl.(plugin.MediaSupplier); ok {
			if state, age, ok := ms.MediaSummary(dev.ID()); ok && state != nil {
				if b, err := json.Marshal(state); err == nil {
					var np NowPlaying
					if err := json.Unmarshal(b, &np); err == nil {
						sum.Media = &MediaState{
							NowPlaying: np,
							MediaAgeMs: age.Milliseconds(),
						}
					}
				}
			}
		}
		if cs, ok := pl.(plugin.ConnectivitySupplier); ok {
			if state, ok := cs.ConnectivitySummary(dev.ID()); ok && state != nil {
				if b, err := json.Marshal(state); err == nil {
					var sig SignalState
					if err := json.Unmarshal(b, &sig); err == nil {
						sum.Signal = &sig
					}
				}
			}
		}
		if ms, ok := pl.(plugin.MountSupplier); ok {
			if mp := ms.MountedPath(dev.ID()); mp != "" {
				sum.Sftp = &SftpMountState{Mounted: true, MountPoint: mp}
			}
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
