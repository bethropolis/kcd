package contacts

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/bethropolis/kcd/internal/device"
	"go.uber.org/zap"
)

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
