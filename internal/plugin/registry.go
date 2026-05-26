package plugin

import (
	"context"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// Registry manages the set of active plugins and routes packets to them.
type Registry struct {
	plugins map[string]Plugin // keyed by packet type string
	byName  map[string]Plugin // keyed by plugin.Name()
	list    []Plugin          // ordered list for iteration
	mu      sync.RWMutex
	logger  *zap.Logger
}

// NewRegistry creates a new plugin registry.
func NewRegistry(logger *zap.Logger) *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		byName:  make(map[string]Plugin),
		logger:  logger.With(zap.String("component", "plugin_registry")),
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
				zap.String("type", typ),
				zap.String("old_plugin", existing.Name()),
				zap.String("new_plugin", p.Name()),
			)
		}
		r.plugins[typ] = p
	}

	r.logger.Debug("registered plugin", zap.String("plugin", p.Name()))
}

// Dispatch routes an incoming packet to the appropriate plugin.
// Returns true if the packet was processed within the timeout and may be safely
// returned to the packet pool. Returns false if the plugin timed out — the
// caller must NOT recycle the packet because the background goroutine may still
// hold a reference to it.
func (r *Registry) Dispatch(ctx context.Context, dev device.Sender, pkt *protocol.Packet) bool {
	r.mu.RLock()
	p, ok := r.plugins[pkt.Type]
	r.mu.RUnlock()

	if !ok {
		// No plugin registered for this type — safely ignore
		r.logger.Debug("unhandled packet type", zap.String("type", pkt.Type))
		return true
	}

	timeout := p.Timeout()
	if timeout == 0 {
		timeout = 15 * time.Second // Fallback default
	}

	// Because we must recover from panics, we execute Handle in a separate goroutine.
	// But the goroutine itself might panic. So we need to recover inside the goroutine.
	// The outer defer will catch panics that happen before spawning or in select, but
	// the plugin execution is where it matters.

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if err := recover(); err != nil {
				r.logger.Error("plugin panic recovered",
					zap.String("plugin", p.Name()),
					zap.String("packet_type", pkt.Type),
					zap.Any("error", err),
				)
				done <- nil // unblock select
			}
		}()
		done <- p.Handle(ctx, dev, pkt)
	}()

	select {
	case err := <-done:
		if err != nil {
			r.logger.Error("plugin handle error",
				zap.String("plugin", p.Name()),
				zap.String("packet_type", pkt.Type),
				zap.Error(err),
			)
		}
		return true // Completed in time; safe to recycle
	case <-ctx.Done():
		r.logger.Warn("plugin handle timeout",
			zap.String("plugin", p.Name()),
			zap.String("packet_type", pkt.Type),
			zap.Duration("timeout", timeout),
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
func (r *Registry) OnConnect(dev device.Sender) {
	for _, p := range r.All() {
		p.OnConnect(dev)
	}
}

// OnDisconnect is called when a device disconnects. It calls OnDisconnect on all registered plugins.
func (r *Registry) OnDisconnect(dev device.Sender) {
	for _, p := range r.All() {
		p.OnDisconnect(dev)
	}
}

// GetByName returns a registered plugin by its Name().
func (r *Registry) GetByName(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byName[name]
	return p, ok
}
