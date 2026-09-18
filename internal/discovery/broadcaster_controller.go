package discovery

import (
	"context"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Broadcast ownership: pairing mode (`kcd pair`) and the reconnect
// watcher share one loop but must not cancel each other, so starts are
// reference-counted per owner. The loop runs while any owner holds it.
const (
	OwnerPairing   = "pairing"
	OwnerReconnect = "reconnect"
)

// BroadcasterController manages the broadcast lifecycle — start/stop on demand.
// Starts in stopped state. Broadcast is only active while Start() is in effect.
type BroadcasterController struct {
	identityPacket *protocol.Packet
	interval       time.Duration
	idleInterval   time.Duration
	shouldReduce   func() bool
	logger         log.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	owners  map[string]struct{}
}

// NewBroadcasterController creates a controller that starts in stopped state.
func NewBroadcasterController(identity *protocol.Packet, interval time.Duration, logger log.Logger, shouldReduce func() bool, idleInterval ...time.Duration) *BroadcasterController {
	idle := 60 * time.Second
	if len(idleInterval) > 0 && idleInterval[0] > 0 {
		idle = idleInterval[0]
	}
	return &BroadcasterController{
		identityPacket: identity,
		interval:       interval,
		idleInterval:   idle,
		shouldReduce:   shouldReduce,
		logger:         logger.With(log.String("component", "broadcaster")),
		owners:         make(map[string]struct{}),
	}
}

// Start launches the UDP broadcaster loop for the pairing owner.
// No-op if already running (ownership is still recorded).
func (bc *BroadcasterController) Start(parentCtx context.Context) {
	bc.StartOwned(parentCtx, OwnerPairing)
}

// Stop withdraws the pairing owner. The loop stops only when no owners
// remain, so a reconnect-driven broadcast survives `kcd pair` exiting.
func (bc *BroadcasterController) Stop() {
	bc.StopOwned(OwnerPairing)
}

// StartOwned launches the loop (if needed) and records owner as needing it.
func (bc *BroadcasterController) StartOwned(parentCtx context.Context, owner string) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.owners[owner] = struct{}{}
	if bc.running {
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)
	bc.cancel = cancel
	bc.running = true

	b := &Broadcaster{
		identityPacket: bc.identityPacket,
		interval:       bc.interval,
		idleInterval:   bc.idleInterval,
		logger:         bc.logger,
	}
	go func() {
		b.Run(ctx, bc.shouldReduce)
		bc.mu.Lock()
		bc.running = false
		bc.mu.Unlock()
	}()
}

// StopOwned withdraws owner's need. No-op if the owner holds nothing.
func (bc *BroadcasterController) StopOwned(owner string) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if _, ok := bc.owners[owner]; !ok {
		return
	}
	delete(bc.owners, owner)
	if len(bc.owners) > 0 || !bc.running || bc.cancel == nil {
		return
	}
	bc.cancel()
	bc.cancel = nil
	bc.running = false
}

// IsRunning reports whether the broadcast loop is currently active.
func (bc *BroadcasterController) IsRunning() bool {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.running
}
