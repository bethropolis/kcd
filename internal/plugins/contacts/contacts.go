package contacts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
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

// uidSafeChars restricts phone-provided contact UIDs (Android LOOKUP_KEYs)
// to filename-safe characters so they can't escape the cache directory.
var uidSafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// sanitizeUID strips everything but alphanumerics, dot, underscore and
// hyphen. Empty results are rejected by the caller.
func sanitizeUID(uid string) string {
	return uidSafeChars.ReplaceAllString(uid, "_")
}

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
	logger  *zap.Logger
	baseDir string

	// mu serializes sync processing across devices. Syncs are rare;
	// one lock avoids per-device lock bookkeeping.
	mu sync.Mutex
}

// NewContactsPlugin creates a contacts plugin caching under
// $XDG_DATA_HOME/kcd/contacts (0600 files, 0700 dirs).
func NewContactsPlugin(bus *events.Bus, logger *zap.Logger) *ContactsPlugin {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	baseDir := filepath.Join(dataHome, "kcd", "contacts")
	_ = os.MkdirAll(baseDir, 0700)

	return &ContactsPlugin{
		bus:     bus,
		logger:  logger.With(zap.String("plugin", "contacts")),
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
		p.logger.Debug("contacts: initial sync request failed", zap.Error(err))
	}
}

func (p *ContactsPlugin) OnDisconnect(dev device.Sender) {}

// Handle routes response packets; all parsing and disk I/O runs in a
// worker goroutine so Handle returns immediately (rule 9).
func (p *ContactsPlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	switch pkt.Type {
	case PacketTypeContactsResponseUIDs:
		body := append([]byte(nil), pkt.Body...)
		go p.handleUIDsResponse(dev, body)
		return nil
	case PacketTypeContactsResponseVCards:
		body := append([]byte(nil), pkt.Body...)
		devID := dev.ID()
		go p.handleVCardsResponse(devID, body)
		return nil
	default:
		return nil
	}
}

// RequestSync asks the phone for all contact UIDs and timestamps, which
// starts the sync round trips. Responses arrive async via Handle.
//
// The body must be an empty object, not null: stock implementations send
// `"body":{}` for bodyless requests, and at least one phone build aborts
// the whole link on an explicit null body (observed as an immediate RST
// after every connect that carried `"body":null`).
func (p *ContactsPlugin) RequestSync(dev device.Sender) error {
	pkt, err := protocol.NewPacket(PacketTypeContactsRequestUIDs, map[string]any{})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// requestVCards asks for vCards of the given UIDs, chunked to bound
// outbound packet size.
func (p *ContactsPlugin) requestVCards(dev device.Sender, uids []string) error {
	for _, chunk := range chunkUIDs(uids, vcardChunkSize) {
		body := map[string]any{"uids": chunk}
		pkt, err := protocol.NewPacket(PacketTypeContactsRequestVCards, body)
		if err != nil {
			return err
		}
		if err := dev.Send(pkt); err != nil {
			return err
		}
	}
	return nil
}

func chunkUIDs(uids []string, size int) [][]string {
	var chunks [][]string
	for len(uids) > 0 {
		n := size
		if len(uids) < n {
			n = len(uids)
		}
		chunks = append(chunks, uids[:n])
		uids = uids[n:]
	}
	return chunks
}

// cacheDir resolves the cache dir for a device without creating it.
// The device ID comes from our own registry, but confine defensively.
func (p *ContactsPlugin) cacheDir(deviceID string) (string, error) {
	safe := sanitizeUID(deviceID)
	if safe == "" {
		return "", fmt.Errorf("contacts: unusable device id")
	}
	dir := filepath.Join(p.baseDir, safe)
	if rel, err := filepath.Rel(p.baseDir, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("contacts: device dir escapes cache")
	}
	return dir, nil
}

// deviceDir returns the cache dir for a device, creating it (0700).
func (p *ContactsPlugin) deviceDir(deviceID string) (string, error) {
	dir, err := p.cacheDir(deviceID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("contacts: create dir: %w", err)
	}
	return dir, nil
}

// contactPath resolves the vcf file for a UID inside dir, confined to dir.
func contactPath(dir, uid string) (string, error) {
	safe := sanitizeUID(uid)
	if safe == "" {
		return "", fmt.Errorf("contacts: unusable uid")
	}
	path := filepath.Join(dir, safe+".vcf")
	if rel, err := filepath.Rel(dir, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("contacts: uid escapes cache")
	}
	return path, nil
}

// loadIndex reads the sidecar index (missing file = empty cache, not error).
func loadIndex(dir string) map[string]indexEntry {
	idx := make(map[string]indexEntry)
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return idx
	}
	_ = json.Unmarshal(data, &idx)
	if idx == nil {
		idx = make(map[string]indexEntry)
	}
	return idx
}

// saveIndex persists the sidecar index (0600).
func saveIndex(dir string, idx map[string]indexEntry) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), data, 0600)
}

// coerceTimestamp parses Android's string-encoded timestamps as well as
// plain JSON numbers (the header doc shows ints). Garbage yields 0, which
// forces a re-fetch — the safe direction, never a false "unchanged".
func coerceTimestamp(raw json.RawMessage) int64 {
	var asAny any
	if err := json.Unmarshal(raw, &asAny); err != nil {
		return 0
	}
	switch v := asAny.(type) {
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0
		}
		return n
	case float64:
		if v < 0 {
			return 0
		}
		return int64(v)
	default:
		return 0
	}
}

// handleUIDsResponse diffs the phone's UID/timestamp list against the local
// cache, deletes stale entries, and requests vCards for new/changed ones.
// dev is the live sender (device.Send is channel-safe across goroutines).
func (p *ContactsPlugin) handleUIDsResponse(dev device.Sender, body []byte) {
	deviceID := dev.ID()

	// Cache diff under lock; the vCard request round below does network
	// I/O and must not hold it.
	toFetch, added, updated, deleted := func() ([]string, int, int, int) {
		p.mu.Lock()
		defer p.mu.Unlock()

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			p.logger.Debug("contacts: malformed uids response", zap.Error(err))
			return nil, 0, 0, 0
		}
		uidsRaw, ok := raw["uids"]
		if !ok {
			p.logger.Debug("contacts: uids response without uids key")
			return nil, 0, 0, 0
		}
		var uids []string
		if err := json.Unmarshal(uidsRaw, &uids); err != nil {
			p.logger.Debug("contacts: malformed uids list", zap.Error(err))
			return nil, 0, 0, 0
		}
		if len(uids) > maxContactUIDs {
			p.logger.Warn("contacts: uids response exceeds cap, refusing",
				zap.Int("count", len(uids)))
			return nil, 0, 0, 0
		}

		dir, err := p.deviceDir(deviceID)
		if err != nil {
			p.logger.Warn("contacts: bad device dir", zap.Error(err))
			return nil, 0, 0, 0
		}
		idx := loadIndex(dir)

		seen := make(map[string]bool, len(uids))
		var toFetch []string
		var added, updated int
		for _, uid := range uids {
			if uid == "" {
				continue
			}
			seen[uid] = true
			ts := coerceTimestamp(raw[uid])
			entry, known := idx[uid]
			if !known {
				added++
				toFetch = append(toFetch, uid)
			} else if entry.Timestamp != ts {
				updated++
				toFetch = append(toFetch, uid)
			}
			// Record the authoritative timestamp now (the vCards round
			// carries no stamps); the summary fields arrive with the vCard.
			entry.Timestamp = ts
			idx[uid] = entry
		}

		// Delete locally-known contacts the phone no longer reports — but
		// never on an empty list: a buggy/empty response must not wipe
		// the cache.
		var deleted int
		if len(uids) > 0 {
			for uid := range idx {
				if !seen[uid] {
					if path, err := contactPath(dir, uid); err == nil {
						_ = os.Remove(path)
					}
					delete(idx, uid)
					deleted++
				}
			}
		}
		if err := saveIndex(dir, idx); err != nil {
			p.logger.Warn("contacts: failed to save index", zap.Error(err))
		}
		return toFetch, added, updated, deleted
	}()

	if len(toFetch) > 0 {
		if err := p.requestVCards(dev, toFetch); err != nil {
			p.logger.Warn("contacts: failed to request vcards",
				zap.String("device_id", deviceID),
				zap.Error(err))
		}
	}
	p.emit(deviceID, map[string]any{
		"phase":   "uids",
		"added":   added,
		"updated": updated,
		"deleted": deleted,
		"pending": len(toFetch),
	})
}

// handleVCardsResponse stores one .vcf per UID, refreshes the index, and
// reports counts (never contact content) on the event bus.
func (p *ContactsPlugin) handleVCardsResponse(deviceID string, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		p.logger.Debug("contacts: malformed vcards response", zap.Error(err))
		return
	}
	uidsRaw, ok := raw["uids"]
	if !ok {
		p.logger.Debug("contacts: vcards response without uids key")
		return
	}
	var uids []string
	if err := json.Unmarshal(uidsRaw, &uids); err != nil {
		p.logger.Debug("contacts: malformed vcards uids list", zap.Error(err))
		return
	}
	if len(uids) > maxContactUIDs {
		p.logger.Warn("contacts: vcards response exceeds cap, refusing",
			zap.Int("count", len(uids)))
		return
	}

	dir, err := p.deviceDir(deviceID)
	if err != nil {
		p.logger.Warn("contacts: bad device dir", zap.Error(err))
		return
	}
	idx := loadIndex(dir)

	var stored, skipped int
	var total int64
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		vRaw, ok := raw[uid]
		if !ok {
			continue
		}
		var vcard string
		if err := json.Unmarshal(vRaw, &vcard); err != nil {
			p.logger.Debug("contacts: non-string vcard, skipping",
				zap.String("uid", sanitizeUID(uid)))
			skipped++
			continue
		}
		if len(vcard) > maxVCardBytes {
			p.logger.Warn("contacts: oversized vcard, skipping",
				zap.String("uid", sanitizeUID(uid)),
				zap.Int("bytes", len(vcard)))
			skipped++
			continue
		}
		total += int64(len(vcard))
		if total > maxSyncBytes {
			p.logger.Warn("contacts: sync exceeds total cap, stopping")
			break
		}
		path, err := contactPath(dir, uid)
		if err != nil {
			skipped++
			continue
		}
		if err := os.WriteFile(path, []byte(vcard), 0600); err != nil {
			p.logger.Warn("contacts: failed to store vcard", zap.Error(err))
			skipped++
			continue
		}
		name, phones, emails := parseVCard(vcard)
		// The vCards round carries no per-UID stamps — keep the timestamp
		// established by the uids round and mark the entry fetched.
		entry := idx[uid]
		entry.Fetched = true
		entry.Name = name
		entry.Phones = phones
		entry.Emails = emails
		idx[uid] = entry
		stored++
	}
	if err := saveIndex(dir, idx); err != nil {
		p.logger.Warn("contacts: failed to save index", zap.Error(err))
	}

	p.emit(deviceID, map[string]any{
		"phase":   "vcards",
		"stored":  stored,
		"skipped": skipped,
	})
}

// List returns cached contact summaries sorted by name. Entries whose
// vCard hasn't arrived yet are skipped; empty when never synced — absent
// means unknown, never a fabricated entry.
func (p *ContactsPlugin) List(deviceID string) []ContactSummary {
	p.mu.Lock()
	defer p.mu.Unlock()

	dir, err := p.cacheDir(deviceID)
	if err != nil {
		return nil
	}
	idx := loadIndex(dir)
	out := make([]ContactSummary, 0, len(idx))
	for uid, entry := range idx {
		if !entry.Fetched {
			continue
		}
		out = append(out, ContactSummary{
			UID:       uid,
			Name:      entry.Name,
			Phones:    entry.Phones,
			Emails:    entry.Emails,
			Timestamp: entry.Timestamp,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UID < out[j].UID
	})
	return out
}

// ForgetDevice deletes a device's cached contacts. Called on unpair:
// revoked trust drops the address book; re-pair re-syncs from scratch.
func (p *ContactsPlugin) ForgetDevice(deviceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	dir, err := p.cacheDir(deviceID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("contacts: forget device: %w", err)
	}
	return nil
}

func (p *ContactsPlugin) emit(deviceID string, payload map[string]any) {
	if p.bus == nil {
		return
	}
	p.bus.Publish(events.TypeContactsUpdated, deviceID, payload)
}

// cleanDisplayValue strips control characters (terminal-escape injection
// via contact names is a classic) and truncates overlong fields.
func cleanDisplayValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxDisplayLen {
		s = s[:maxDisplayLen]
	}
	return s
}

// parseVCard extracts display fields from vCard 3.0 text with a stdlib
// line scan: unfold continuations, split Name:Value, drop parameters
// (TEL;TYPE=CELL:...). Returns the first FN and all TEL/EMAIL values.
func parseVCard(vcard string) (name string, phones, emails []string) {
	// Normalize newlines, then unfold continuation lines (leading SP/HT).
	raw := strings.ReplaceAll(vcard, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	for _, line := range lines {
		sep := strings.IndexByte(line, ':')
		if sep < 0 {
			continue
		}
		field := strings.ToUpper(line[:sep])
		if i := strings.IndexByte(field, ';'); i >= 0 {
			field = field[:i]
		}
		value := cleanDisplayValue(line[sep+1:])
		if value == "" {
			continue
		}
		switch field {
		case "FN":
			if name == "" {
				name = value
			}
		case "TEL":
			phones = append(phones, value)
		case "EMAIL":
			emails = append(emails, value)
		}
	}
	return name, phones, emails
}
