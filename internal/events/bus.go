package events

import (
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/log"
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
	TypeTelephonyTalking     EventType = "telephony.talking"
	TypeTelephonyCanceled    EventType = "telephony.canceled"
	TypeConnectivityUpdate   EventType = "connectivity.update"
	TypeSftpMount            EventType = "sftp.mount"
	TypeNotificationCanceled EventType = "notification.canceled"
	TypeVolumeUpdate         EventType = "volume.update"
	TypeSMSIncoming          EventType = "sms.incoming"
	TypeSMSAttachment        EventType = "sms.attachment"
	TypeRingReceived         EventType = "ring.received"
	TypeContactsUpdated      EventType = "contacts.updated"
	TypeMprisUpdate          EventType = "mpris.update"
	TypeStateSnapshot        EventType = "state.snapshot"
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
	logger      log.Logger
	// changeHooks run (without the bus lock held) after every subscribe
	// and unsubscribe, so demand-driven producers can start/stop with
	// the audience instead of polling HasSubscribers on a timer.
	changeHooks []func()
}

// NewBus creates a new event bus.
func NewBus(logger log.Logger) *Bus {
	return &Bus{
		subscribers: make(map[uint64]*Subscriber),
		logger:      logger.With(log.String("component", "events")),
	}
}

// Subscribe returns a new subscriber that receives events matching the filters.
// capacity sets the channel buffer size; pass 0 or DefaultSubscriberCap for the standard 64-event buffer.
// If filters is empty, it receives all events.
func (b *Bus) Subscribe(capacity int, filters ...EventType) *Subscriber {
	b.mu.Lock()

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
	b.logger.Debug("new subscriber", log.Uint64("id", id), log.Int("filters", len(filters)))
	b.mu.Unlock()
	b.notifyChange()
	return sub
}

// unsubscribe removes a subscriber.
func (b *Bus) unsubscribe(id uint64) {
	b.mu.Lock()

	removed := false
	if sub, ok := b.subscribers[id]; ok {
		close(sub.ch)
		delete(b.subscribers, id)
		b.logger.Debug("subscriber removed", log.Uint64("id", id))
		removed = true
	}
	b.mu.Unlock()
	if removed {
		b.notifyChange()
	}
}

// OnSubscriberChange registers a hook invoked after every subscribe and
// unsubscribe. Hooks run without the bus lock held and must return
// quickly; they typically re-check HasSubscribers and start/stop a
// producer. Register before subscribers arrive — hooks do not replay.
func (b *Bus) OnSubscriberChange(fn func()) {
	b.mu.Lock()
	b.changeHooks = append(b.changeHooks, fn)
	b.mu.Unlock()
}

func (b *Bus) notifyChange() {
	b.mu.RLock()
	hooks := make([]func(), len(b.changeHooks))
	copy(hooks, b.changeHooks)
	b.mu.RUnlock()
	for _, fn := range hooks {
		fn()
	}
}

// HasSubscribers reports whether at least one live subscriber would
// receive events of the given type. Subscribers with no filters match
// everything. Scanned on demand under RLock — subscriber counts are tiny
// and callers tick at most every few seconds, so no counter state to keep
// in sync on the unsubscribe path.
func (b *Bus) HasSubscribers(typ EventType) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subscribers {
		if sub.matches(typ) {
			return true
		}
	}
	return false
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
					log.Uint64("id", sub.id),
					log.String("type", string(typ)),
					log.String("device_id", deviceID))
			}
		}
	}
}
