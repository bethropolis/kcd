package mpris

import (
	"context"
	"crypto/tls"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/godbus/dbus/v5"
)

type trackedPlayer struct {
	busName     string
	uniqueName  string
	displayName string
	shortName   string
}

type MPRISPlugin struct {
	tlsConfig *tls.Config
	logger    log.Logger
	bus       *events.Bus
	mu        sync.RWMutex
	devices   map[string]device.Sender
	dbus      *dbus.Conn

	watchCancel     context.CancelFunc
	watchCtx        context.Context
	watching        bool
	telephonyCancel context.CancelFunc

	// mprisCfg gates the position poller: with PollWhilePlaying the
	// ticker exists only while at least one local player IsPlaying.
	// pollCancel stops it; nil means no poller is running.
	mprisCfg   config.MPRISConfig
	pollCancel context.CancelFunc

	// reconcileCh nudges the D-Bus watcher loop to re-list player names
	// and heal drift. Buffered size 1 so bursts of triggers coalesce;
	// sends are non-blocking. Event-driven only — no timers.
	reconcileCh chan struct{}

	players          map[string]*trackedPlayer
	lastTracks       map[string]trackIdentity
	lastStates       map[string]*NowPlaying
	artRequests      map[string]time.Time
	remoteStates     map[string]*NowPlaying            // deviceID → last known state
	remoteStateTimes map[string]time.Time              // deviceID → when state was last updated
	positionTrackers map[string]*remotePositionTracker // deviceID → position extrapolation

	pauseMusic        bool
	callPausedPlayers []string // names of local players paused during a call

	artCache *ArtCache
}

type trackIdentity struct {
	rawArtUrl string
}

type remotePositionTracker struct {
	lastPosition   int64
	lastPositionAt time.Time
	playing        bool
}

func NewMPRISPlugin(tlsConfig *tls.Config, bus *events.Bus, pauseMusic bool, mprisCfg config.MPRISConfig, logger log.Logger, cacheDirs ...string) *MPRISPlugin {
	dbusConn, err := dbus.ConnectSessionBus()
	if err != nil {
		logger.Warn("mpris: failed to connect to D-Bus session bus", log.Error(err))
	} else {
		logger.Info("mpris: connected to D-Bus session bus")
	}

	p := &MPRISPlugin{
		tlsConfig:         tlsConfig,
		logger:            logger.With(log.String("plugin", "mpris")),
		bus:               bus,
		dbus:              dbusConn,
		pauseMusic:        pauseMusic,
		devices:           make(map[string]device.Sender),
		players:           make(map[string]*trackedPlayer),
		lastTracks:        make(map[string]trackIdentity),
		lastStates:        make(map[string]*NowPlaying),
		artRequests:       make(map[string]time.Time),
		remoteStates:      make(map[string]*NowPlaying),
		remoteStateTimes:  make(map[string]time.Time),
		positionTrackers:  make(map[string]*remotePositionTracker),
		callPausedPlayers: make([]string, 0),
		artCache:          NewArtCache(logger, cacheDirs...),
		reconcileCh:       make(chan struct{}, 1),
		mprisCfg:          mprisCfg,
	}

	// Start the watcher immediately (like C++ does in constructor).
	// Devices are registered lazily as packets arrive.
	watchCtx, cancel := context.WithCancel(context.Background())
	p.watchCtx = watchCtx
	p.watchCancel = cancel
	p.watching = true
	p.startWatcher(watchCtx)
	p.startRemoteStatePoller(watchCtx)

	// Subscribe to telephony events for pause-music-on-call.
	if p.pauseMusic && p.dbus != nil {
		telephonyCtx, telephonyCancel := context.WithCancel(context.Background())
		p.telephonyCancel = telephonyCancel
		go p.watchTelephony(telephonyCtx)
	}

	return p
}

func (p *MPRISPlugin) Name() string           { return "MPRIS" }
func (p *MPRISPlugin) Timeout() time.Duration { return 5 * time.Second }

// requestReconcile asks the D-Bus watcher loop to re-list player names
// and heal any drift. Non-blocking: a pending request already covers us.
func (p *MPRISPlugin) requestReconcile() {
	select {
	case p.reconcileCh <- struct{}{}:
	default:
	}
}

// RequestReconcile is the exported hook for daemon IPC routes (e.g. the
// remote-state listing) so user-initiated queries heal drift too.
func (p *MPRISPlugin) RequestReconcile() { p.requestReconcile() }
func (p *MPRISPlugin) IncomingTypes() []string {
	return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"}
}
func (p *MPRISPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.mpris", "kdeconnect.mpris.request"}
}
