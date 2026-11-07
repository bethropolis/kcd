package plugin

import (
	"context"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// defaultDispatchTimeout bounds Handle calls with no plugin Timeout; every
// bundled plugin sets one, so this fires for third-party plugins only.
const defaultDispatchTimeout = 15 * time.Second

// Registry manages the set of active plugins and routes packets to them.
type Registry struct {
	plugins             map[string]Plugin // keyed by packet type string
	byName              map[string]Plugin // keyed by plugin.Name()
	list                []Plugin          // ordered list for iteration
	mu                  sync.RWMutex
	logger              log.Logger
	devicePluginEnabled func(deviceID, pluginName string) bool
}

type policySender struct {
	device.Peer
	allowed    func(string, string) bool
	pluginName string
	logger     log.Logger
}

func (s *policySender) Send(pkt *protocol.Packet) error {
	if s.allowed != nil && !s.allowed(s.ID(), s.pluginName) {
		s.logger.Debug("dropping outbound packet for disabled plugin",
			log.String("plugin", s.pluginName),
			log.String("device_id", s.ID()),
			log.String("type", pkt.Type),
		)
		protocol.ReleasePacket(pkt)
		return nil
	}
	return s.Peer.Send(pkt)
}

func (s *policySender) UpdateBattery(charge int, charging bool) {
	if updater, ok := s.Peer.(interface{ UpdateBattery(int, bool) }); ok {
		updater.UpdateBattery(charge, charging)
	}
}

func (s *policySender) GetBattery() (int, bool) {
	if getter, ok := s.Peer.(interface{ GetBattery() (int, bool) }); ok {
		return getter.GetBattery()
	}
	return 0, false
}

// SetDevicePluginPolicy installs per-device inbound plugin gating.
func (r *Registry) SetDevicePluginPolicy(policy func(string, string) bool) {
	r.mu.Lock()
	r.devicePluginEnabled = policy
	r.mu.Unlock()
}

// NewRegistry creates a new plugin registry.
func NewRegistry(logger log.Logger) *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		byName:  make(map[string]Plugin),
		logger:  logger.With(log.String("component", "plugin_registry")),
	}
}

// Register adds a plugin to the registry and indexes it by its incoming types.
func (r *Registry) Register(p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.list = append(r.list, p)
	r.byName[p.Name()] = p

	for _, typ := range p.IncomingTypes() {
		if existing, ok := r.plugins[typ]; ok {
			r.logger.Warn("plugin packet type collision, overwriting",
				log.String("type", typ),
				log.String("old_plugin", existing.Name()),
				log.String("new_plugin", p.Name()),
			)
		}
		r.plugins[typ] = p
	}

	r.logger.Debug("registered plugin", log.String("plugin", p.Name()))
}

// Dispatch routes an incoming packet to the appropriate plugin. True means
// processed in time and safe to recycle; false means a timed-out handler
// may still hold it — the caller must NOT recycle.
func (r *Registry) Dispatch(ctx context.Context, dev device.Peer, pkt *protocol.Packet) bool {
	r.mu.RLock()
	p, ok := r.plugins[pkt.Type]
	r.mu.RUnlock()

	if !ok {
		// No plugin registered for this type — safely ignore
		r.logger.Debug("unhandled packet type", log.String("type", pkt.Type))
		return true
	}
	r.mu.RLock()
	allowed := r.devicePluginEnabled == nil || r.devicePluginEnabled(dev.ID(), p.Name())
	r.mu.RUnlock()
	if !allowed {
		r.logger.Debug("plugin disabled for device", log.String("plugin", p.Name()), log.String("device_id", dev.ID()))
		return true
	}

	timeout := p.Timeout()
	if timeout == 0 {
		timeout = defaultDispatchTimeout // Fallback default
	}

	// Handle runs in a goroutine with recovery inside it: panics must land
	// in done, or the select below blocks forever.

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pluginDev := r.senderFor(p, dev)

	done := make(chan error, 1)
	go func() {
		defer func() {
			if err := recover(); err != nil {
				r.logger.Error("plugin panic recovered",
					log.String("plugin", p.Name()),
					log.String("packet_type", pkt.Type),
					log.Any("error", err),
				)
				done <- nil // unblock select
			}
		}()
		done <- p.Handle(ctx, pluginDev, pkt)
	}()

	select {
	case err := <-done:
		if err != nil {
			r.logger.Error("plugin handle error",
				log.String("plugin", p.Name()),
				log.String("packet_type", pkt.Type),
				log.Error(err),
			)
		}
		return true // Completed in time; safe to recycle
	case <-ctx.Done():
		r.logger.Warn("plugin handle timeout",
			log.String("plugin", p.Name()),
			log.String("packet_type", pkt.Type),
			log.Duration("timeout", timeout),
		)
		return false // Timed out; DO NOT recycle — goroutine still holds packet
	}
}

// Capabilities returns the aggregated incoming and outgoing capabilities
// of all registered plugins, suitable for sending in an Identity packet.
func (r *Registry) Capabilities() (incoming []string, outgoing []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.list {
		incoming = append(incoming, p.IncomingTypes()...)
		outgoing = append(outgoing, p.OutgoingTypes()...)
	}
	return
}

// All returns a slice of all registered plugins.
func (r *Registry) All() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cpy := make([]Plugin, len(r.list))
	copy(cpy, r.list)
	return cpy
}

// OnConnect is called when a device connects. It calls OnConnect on all registered plugins.
func (r *Registry) OnConnect(dev device.Peer) {
	for _, p := range r.All() {
		p.OnConnect(r.senderFor(p, dev))
	}
}

// OnDisconnect is called when a device disconnects. It calls OnDisconnect on all registered plugins.
func (r *Registry) OnDisconnect(dev device.Peer) {
	for _, p := range r.All() {
		p.OnDisconnect(r.senderFor(p, dev))
	}
}

func (r *Registry) senderFor(p Plugin, dev device.Peer) device.Peer {
	r.mu.RLock()
	policy := r.devicePluginEnabled
	r.mu.RUnlock()
	return &policySender{
		Peer:       dev,
		allowed:    policy,
		pluginName: p.Name(),
		logger:     r.logger,
	}
}

// GetByName returns a registered plugin by its Name().
func (r *Registry) GetByName(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byName[name]
	return p, ok
}

// Lookup finds the registered plugin implementing type T.
func Lookup[T Plugin](r *Registry) (T, bool) {
	if r == nil {
		var zero T
		return zero, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.list {
		if typed, ok := p.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}
