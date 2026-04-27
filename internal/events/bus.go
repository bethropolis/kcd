package events

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// EventType defines the kind of event being broadcast.
type EventType string

// Known event types.
const (
	TypeDeviceAdded          EventType = "device.added"
	TypeDeviceRemoved        EventType = "device.removed"
	TypeDeviceConnected      EventType = "device.connected"
	TypeDeviceDisconnected   EventType = "device.disconnected"
	TypePairRequested        EventType = "pair.requested"
	TypePairAccepted         EventType = "pair.accepted"
	TypePairRejected         EventType = "pair.rejected"
	TypeBatteryUpdate        EventType = "battery.update"
	TypeBatteryThreshold     EventType = "battery.threshold"
	TypeNotification         EventType = "notification"
	TypeShareProgress        EventType = "share.progress"
	TypeShareComplete        EventType = "share.complete"
	TypeShareText            EventType = "share.text"
	TypeShareURL             EventType = "share.url"
	TypePingReceived         EventType = "ping.received"
	TypeTelephonyRinging     EventType = "telephony.ringing"
	TypeTelephonyMissed      EventType = "telephony.missed"
	TypeTelephonyCanceled    EventType = "telephony.canceled"
	TypeConnectivityUpdate   EventType = "connectivity.update"
	TypeSftpMount            EventType = "sftp.mount"
	TypeNotificationCanceled EventType = "notification.canceled"
	TypeVolumeUpdate         EventType = "volume.update"
)

const (
	// DefaultSubscriberCap is the channel buffer size for plugin-internal
	// subscribers (battery test waits, sftp credential waits, etc.).
	DefaultSubscriberCap = 64

	// WatchSubscriberCap is the channel buffer for the kcd watch IPC stream.
	// Larger to tolerate slow CLI consumers (jq, SSH, slow terminals).
	WatchSubscriberCap = 256
)

// Event represents a single occurrence of something interesting in the daemon.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	DeviceID  string    `json:"deviceId,omitempty"`
	Payload   any       `json:"payload,omitempty"`
}

// Subscriber receives events.
type Subscriber struct {
	C       <-chan Event
	filters []EventType
	ch      chan Event
	id      uint64
	bus     *Bus
}

// Close unsubscribes from the bus.
func (s *Subscriber) Close() {
	s.bus.unsubscribe(s.id)
}

func (s *Subscriber) matches(typ EventType) bool {
	if len(s.filters) == 0 {
		return true
	}
	for _, f := range s.filters {
		if f == typ {
			return true
		}
	}
	return false
}

// Bus distributes events to multiple subscribers.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[uint64]*Subscriber
	nextID      uint64
	logger      *zap.Logger
}

// NewBus creates a new event bus.
func NewBus(logger *zap.Logger) *Bus {
	return &Bus{
		subscribers: make(map[uint64]*Subscriber),
		logger:      logger.With(zap.String("component", "events")),
	}
}

// Subscribe returns a new subscriber that receives events matching the filters.
// capacity sets the channel buffer size; pass 0 or DefaultSubscriberCap for the standard 64-event buffer.
// If filters is empty, it receives all events.
func (b *Bus) Subscribe(capacity int, filters ...EventType) *Subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	id := b.nextID
	if capacity <= 0 {
		capacity = DefaultSubscriberCap
	}
	ch := make(chan Event, capacity)

	sub := &Subscriber{
		C:       ch,
		filters: filters,
		ch:      ch,
		id:      id,
		bus:     b,
	}

	b.subscribers[id] = sub
	b.logger.Debug("new subscriber", zap.Uint64("id", id), zap.Int("filters", len(filters)))
	return sub
}

// unsubscribe removes a subscriber.
func (b *Bus) unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sub, ok := b.subscribers[id]; ok {
		close(sub.ch)
		delete(b.subscribers, id)
		b.logger.Debug("subscriber removed", zap.Uint64("id", id))
	}
}

// Publish broadcasts an event to all interested subscribers.
func (b *Bus) Publish(typ EventType, deviceID string, payload any) {
	ev := Event{
		Type:      typ,
		Timestamp: time.Now().UTC(),
		DeviceID:  deviceID,
		Payload:   payload,
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subscribers {
		if sub.matches(typ) {
			select {
			case sub.ch <- ev:
			default:
				// Drop event if channel is full
				b.logger.Warn("subscriber channel full, dropping event",
					zap.Uint64("id", sub.id),
					zap.String("type", string(typ)),
					zap.String("device_id", deviceID))
			}
		}
	}
}
