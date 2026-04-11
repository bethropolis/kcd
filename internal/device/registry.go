package device

import (
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/events"
)

// Registry manages the set of known devices in memory.
type Registry struct {
	devices sync.Map // map[string]*Device (keyed by device ID)
	bus     *events.Bus
}

// NewRegistry creates a new empty device registry.
func NewRegistry(bus *events.Bus) *Registry {
	return &Registry{
		bus: bus,
	}
}

// Add inserts or replaces a device in the registry.
func (r *Registry) Add(d *Device) {
	d.SetBus(r.bus)
	r.devices.Store(d.ID(), d)
	if r.bus != nil {
		r.bus.Publish(events.TypeDeviceAdded, d.ID(), d.Name())
	}
}

// Remove deletes a device from the registry.
func (r *Registry) Remove(id string) {
	r.devices.Delete(id)
	if r.bus != nil {
		r.bus.Publish(events.TypeDeviceRemoved, id, nil)
	}
}

// Get finds a device by its ID.
func (r *Registry) Get(id string) (*Device, bool) {
	v, ok := r.devices.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Device), true
}

// List returns all devices currently in the registry.
func (r *Registry) List() []*Device {
	var list []*Device
	r.devices.Range(func(_, value any) bool {
		list = append(list, value.(*Device))
		return true // continue
	})
	return list
}

// Connected returns only devices that currently have an active connection.
func (r *Registry) Connected() []*Device {
	var list []*Device
	r.devices.Range(func(_, value any) bool {
		dev := value.(*Device)
		if dev.IsConnected() {
			list = append(list, dev)
		}
		return true
	})
	return list
}

// AllPairedDevicesConnected returns true if there is at least one paired device
// and all paired devices are currently connected.
func (r *Registry) AllPairedDevicesConnected() bool {
	allConnected := true
	hasPaired := false

	r.devices.Range(func(_, value any) bool {
		dev := value.(*Device)
		if dev.State() == StatePaired {
			hasPaired = true
			if !dev.IsConnected() {
				allConnected = false
				return false // break early
			}
		}
		return true
	})

	return hasPaired && allConnected
}

// ReconnectBackoff calculates exponential backoff duration based on attempts.
// Caps out at maxDuration.
func ReconnectBackoff(attempt int, maxDuration time.Duration) time.Duration {
	if attempt <= 0 {
		return time.Second * 2
	}
	dur := time.Duration(1<<attempt) * time.Second * 2
	if dur > maxDuration || dur < 0 { // < 0 checks integer overflow
		return maxDuration
	}
	return dur
}
