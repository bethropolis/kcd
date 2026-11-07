package contacts

import (
	"os"
	"path/filepath"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

var _ plugin.Plugin = (*ContactsPlugin)(nil)

// NewContactsPlugin creates a contacts plugin caching under
// $XDG_DATA_HOME/kcd/contacts (0600 files, 0700 dirs).
func NewContactsPlugin(bus *events.Bus, logger log.Logger, cacheDirs ...string) *ContactsPlugin {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	baseDir := filepath.Join(dataHome, "kcd", "contacts")
	if len(cacheDirs) > 0 && cacheDirs[0] != "" {
		baseDir = cacheDirs[0]
	}
	_ = os.MkdirAll(baseDir, 0700)

	return &ContactsPlugin{
		bus:     bus,
		logger:  logger.With(log.String("plugin", "contacts")),
		baseDir: baseDir,
	}
}

func (p *ContactsPlugin) Name() string           { return "Contacts" }
func (p *ContactsPlugin) Timeout() time.Duration { return 5 * time.Second }

func (p *ContactsPlugin) IncomingTypes() []string {
	return []string{PacketTypeContactsResponseUIDs, PacketTypeContactsResponseVCards}
}

func (p *ContactsPlugin) OutgoingTypes() []string {
	return []string{PacketTypeContactsRequestUIDs, PacketTypeContactsRequestVCards}
}

// OnConnect starts a sync for freshly connected paired devices, mirroring
// upstream's connected() -> synchronizeRemoteWithLocal().
func (p *ContactsPlugin) OnConnect(dev device.Peer) {
	if dev.State() != device.StatePaired {
		return
	}
	if err := p.RequestSync(dev); err != nil {
		p.logger.Debug("contacts: initial sync request failed", log.Error(err))
	}
}

func (p *ContactsPlugin) OnDisconnect(dev device.Peer) {}
