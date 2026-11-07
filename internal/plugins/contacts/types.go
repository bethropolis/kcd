package contacts

import (
	"sync"

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

// indexEntry is the persisted per-contact record (sidecar, so listing never
// reparses vCards); Fetched marks a completed vCard round, else pending.
type indexEntry struct {
	Timestamp int64    `json:"timestamp"`
	Fetched   bool     `json:"fetched"`
	Name      string   `json:"name"`
	Phones    []string `json:"phones,omitempty"`
	Emails    []string `json:"emails,omitempty"`
}

// ContactsPlugin syncs the phone address book: UID/timestamp lists, vCard
// fetches for new or changed contacts, per-device cache, stale deletion.
type ContactsPlugin struct {
	bus     *events.Bus
	logger  log.Logger
	baseDir string

	// mu serializes sync processing across devices. Syncs are rare;
	// one lock avoids per-device lock bookkeeping.
	mu sync.Mutex
}
