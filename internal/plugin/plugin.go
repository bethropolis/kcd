package plugin

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Plugin represents a KDE Connect functionality module.
// Implementations must not import other plugins, and must not block in Handle.
type Plugin interface {
	// Name returns the unique human-readable name of the plugin (e.g. "Battery").
	Name() string

	// IncomingTypes returns a list of packet type strings this plugin handles.
	IncomingTypes() []string

	// OutgoingTypes returns a list of packet type strings this plugin may send.
	OutgoingTypes() []string

	// Timeout returns the maximum duration allowed for this plugin's Handle execution.
	// Return 0 for no timeout (or a global default).
	Timeout() time.Duration

	// OnConnect is called when the device connects.
	OnConnect(dev device.Peer)

	// OnDisconnect is called when the device disconnects.
	OnDisconnect(dev device.Peer)

	// Handle processes a packet in the device read loop. Its context is cancelled
	// when Handle returns, so work that outlives the call needs its own context.
	// Blocking work must run asynchronously and return immediately.
	Handle(ctx context.Context, dev device.Peer, pkt *protocol.Packet) error
}

// Replayer produces initial events for a device to send upon connection or watch streaming.
type Replayer interface {
	Replay(deviceID string) []events.Event
}

// MediaSupplier provides cached now-playing media state for device summaries.
type MediaSupplier interface {
	MediaSummary(deviceID string) (state any, age time.Duration, ok bool)
}

// ConnectivitySupplier provides cached cellular connectivity reports for device summaries.
type ConnectivitySupplier interface {
	ConnectivitySummary(deviceID string) (state any, ok bool)
}

// MountSupplier reports an active mount path for a device.
type MountSupplier interface {
	MountedPath(deviceID string) string
}
