package contacts

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

// KDE Connect contacts packet types (see kdeconnect-kde
// plugins/contacts/contactsplugin.h and the Android ContactsPlugin).
const (
	PacketTypeContactsRequestUIDs    = "kdeconnect.contacts.request_all_uids_timestamps"
	PacketTypeContactsRequestVCards  = "kdeconnect.contacts.request_vcards_by_uid"
	PacketTypeContactsResponseUIDs   = "kdeconnect.contacts.response_uids_timestamps"
	PacketTypeContactsResponseVCards = "kdeconnect.contacts.response_vcards"
)

const (
	// maxContactUIDs bounds a single uids response (address books are in
	// the hundreds; this is two orders of magnitude of headroom).
	maxContactUIDs = 20000
	// maxVCardBytes caps one vCard (text contact cards are ~1KB).
	maxVCardBytes = 64 * 1024
	// maxSyncBytes caps a whole vcards response before anything hits disk.
	maxSyncBytes = 64 * 1024 * 1024
	// vcardChunkSize bounds our outbound request packets.
	vcardChunkSize = 100
	// maxDisplayLen truncates contact fields shown to clients.
	maxDisplayLen = 256
)

// ContactSummary is the parsed, display-safe view of one contact.
type ContactSummary struct {
	UID       string   `json:"uid"`
	Name      string   `json:"name"`
	Phones    []string `json:"phones,omitempty"`
	Emails    []string `json:"emails,omitempty"`
	Timestamp int64    `json:"timestamp"`
}

// indexEntry is the persisted per-contact record (index.json sidecar, so
// listing never reparses hundreds of vCards). Fetched marks that the vCard
// round completed; entries with only a timestamp are pending fetch.
type indexEntry struct {
	Timestamp int64    `json:"timestamp"`
	Fetched   bool     `json:"fetched"`
	Name      string   `json:"name"`
	Phones    []string `json:"phones,omitempty"`
	Emails    []string `json:"emails,omitempty"`
}

// ContactsPlugin syncs the phone address book: it requests UID/timestamp
// lists, fetches vCards for new or changed contacts, caches them per
// device, and deletes stale entries.
type ContactsPlugin struct {
	bus     *events.Bus
	logger  log.Logger
	baseDir string

	// mu serializes sync processing across devices. Syncs are rare;
	// one lock avoids per-device lock bookkeeping.
	mu sync.Mutex
}

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

func (p *ContactsPlugin) Name() string { return "Contacts" }

func (p *ContactsPlugin) Timeout() time.Duration { return 5 * time.Second }

func (p *ContactsPlugin) IncomingTypes() []string {
	return []string{PacketTypeContactsResponseUIDs, PacketTypeContactsResponseVCards}
}

func (p *ContactsPlugin) OutgoingTypes() []string {
	return []string{PacketTypeContactsRequestUIDs, PacketTypeContactsRequestVCards}
}

// OnConnect starts a sync for freshly connected paired devices, mirroring
// upstream's connected() -> synchronizeRemoteWithLocal().
func (p *ContactsPlugin) OnConnect(dev device.Sender) {
	if dev.State() != device.StatePaired {
		return
	}
	if err := p.RequestSync(dev); err != nil {
		p.logger.Debug("contacts: initial sync request failed", log.Error(err))
	}
}

func (p *ContactsPlugin) OnDisconnect(dev device.Sender) {}
