package plugin

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/device"
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

	// Handle processes a packet in the device read loop. Its context is cancelled
	// when Handle returns, so work that outlives the call needs its own context.
	// Blocking work must run asynchronously and return immediately.

	// OnConnect is called when the device connects.
	OnConnect(dev device.Sender)

	// OnDisconnect is called when the device disconnects.
	OnDisconnect(dev device.Sender)
	Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error
}
