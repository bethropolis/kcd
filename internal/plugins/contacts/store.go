package contacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
