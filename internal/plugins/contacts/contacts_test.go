package contacts

import (
	"crypto/x509"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// fakeSender captures outbound packets for round-trip tests.
type fakeSender struct {
	id   string
	sent []*protocol.Packet
}

func (f *fakeSender) ID() string                         { return f.id }
func (f *fakeSender) Name() string                       { return "Fake" }
func (f *fakeSender) SetName(name string)                {}
func (f *fakeSender) State() device.PairingState         { return device.StatePaired }
func (f *fakeSender) SetState(state device.PairingState) {}
func (f *fakeSender) Send(p *protocol.Packet) error      { f.sent = append(f.sent, p); return nil }
func (f *fakeSender) IsConnected() bool                  { return true }
func (f *fakeSender) RemoteIP() net.IP                   { return nil }
func (f *fakeSender) PeerCert() *x509.Certificate        { return nil }
func (f *fakeSender) HasCapability(cap string) bool      { return false }
func (f *fakeSender) UpdateBattery(charge int, ch bool)  {}
func (f *fakeSender) GetBattery() (int, bool)            { return 0, false }

func testPlugin(t *testing.T) *ContactsPlugin {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	return NewContactsPlugin(nil, log.NewTest(t))
}

func TestParseVCard(t *testing.T) {
	vcard := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ada Lovelace\r\nTEL;TYPE=CELL:+1-555-0100\r\nTEL;TYPE=HOME:+1-555-0101\r\nEMAIL:ada@example.com\r\nREV:973486597\r\nEND:VCARD"
	name, phones, emails := parseVCard(vcard)
	if name != "Ada Lovelace" {
		t.Errorf("name = %q", name)
	}
	if len(phones) != 2 || phones[0] != "+1-555-0100" {
		t.Errorf("phones = %v", phones)
	}
	if len(emails) != 1 || emails[0] != "ada@example.com" {
		t.Errorf("emails = %v", emails)
	}
}

func TestParseVCardFoldingAndControls(t *testing.T) {
	// Folded FN line + control char + overlong value.
	vcard := "BEGIN:VCARD\nFN:John\x00 Smi\n th\nTEL:123\nEND:VCARD"
	name, phones, _ := parseVCard(vcard)
	if name != "John Smith" {
		t.Errorf("folded/controls name = %q", name)
	}
	if len(phones) != 1 {
		t.Errorf("phones = %v", phones)
	}

	long := "FN:" + strings.Repeat("x", 500)
	name, _, _ = parseVCard("BEGIN:VCARD\n" + long + "\nEND:VCARD")
	if len(name) != maxDisplayLen {
		t.Errorf("name not truncated: len=%d", len(name))
	}
}

func TestCoerceTimestamp(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"string", `"973486597"`, 973486597},
		{"string spaces", `"  42 "`, 42},
		{"int", `973486597`, 973486597},
		{"garbage string", `"soon"`, 0},
		{"bool", `true`, 0},
		{"null", `null`, 0},
		{"negative float", `-5`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := coerceTimestamp(json.RawMessage(tc.raw)); got != tc.want {
				t.Errorf("coerceTimestamp(%s) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSanitizeUID(t *testing.T) {
	if got := sanitizeUID("1234iabc"); got != "1234iabc" {
		t.Errorf("plain uid mangled: %q", got)
	}
	if got := sanitizeUID("../../etc"); strings.ContainsAny(got, "/\\") {
		t.Errorf("traversal not neutralized: %q", got)
	}
	if sanitizeUID("") != "" {
		t.Error("empty uid must stay empty (caller rejects)")
	}
}

func TestContactPathConfined(t *testing.T) {
	dir := t.TempDir()
	if _, err := contactPath(dir, "../evil"); err == nil {
		// sanitized to plain underscores — must still resolve inside dir
		t.Log("traversal neutralized by sanitizer")
	}
	p, err := contactPath(dir, "abc123")
	if err != nil {
		t.Fatalf("valid uid rejected: %v", err)
	}
	if filepath.Dir(p) != dir {
		t.Errorf("path escapes dir: %q", p)
	}
	if _, err := contactPath(dir, ""); err == nil {
		t.Error("empty uid must be rejected")
	}
}

func TestChunkUIDs(t *testing.T) {
	var uids []string
	for i := 0; i < 250; i++ {
		uids = append(uids, string(rune('a'+i%26))+strings.Repeat("x", i%5))
	}
	chunks := chunkUIDs(uids, vcardChunkSize)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len(c) > vcardChunkSize {
			t.Errorf("chunk exceeds size: %d", len(c))
		}
	}
	if len(chunkUIDs(nil, vcardChunkSize)) != 0 {
		t.Error("nil must yield no chunks")
	}
}

// Full round trip: uids response -> vcard request emitted -> vcards
// response -> files + index + List output.
func TestSyncRoundTrip(t *testing.T) {
	p := testPlugin(t)
	dev := &fakeSender{id: "dev1"}

	uidsBody, _ := json.Marshal(map[string]any{
		"uids": []string{"u1", "u2"},
		"u1":   "100",
		"u2":   200,
	})
	p.handleUIDsResponse(dev, uidsBody)

	if len(dev.sent) != 1 {
		t.Fatalf("expected 1 vcard request, got %d", len(dev.sent))
	}
	if dev.sent[0].Type != PacketTypeContactsRequestVCards {
		t.Errorf("wrong request type: %s", dev.sent[0].Type)
	}

	vcardsBody, _ := json.Marshal(map[string]any{
		"uids": []string{"u1", "u2"},
		"u1":   "BEGIN:VCARD\nFN:Alice\nTEL:111\nEND:VCARD",
		"u2":   "BEGIN:VCARD\nFN:Bob\nEMAIL:bob@x.y\nEND:VCARD",
	})
	p.handleVCardsResponse("dev1", vcardsBody)

	list := p.List("dev1")
	if len(list) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(list))
	}
	// Sorted by name.
	if list[0].Name != "Alice" || list[1].Name != "Bob" {
		t.Errorf("bad sort/content: %+v", list)
	}
	if len(list[0].Phones) != 1 || list[0].Phones[0] != "111" {
		t.Errorf("phones wrong: %+v", list[0])
	}
	// Timestamps preserved from the uids round (string and int forms).
	if list[0].Timestamp != 100 || list[1].Timestamp != 200 {
		t.Errorf("timestamps wrong: %+v", list)
	}

	// Files on disk, 0600.
	for _, uid := range []string{"u1", "u2"} {
		path, err := contactPath(filepath.Join(p.baseDir, "dev1"), uid)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing vcf for %s: %v", uid, err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Errorf("vcf perms = %o, want 600", fi.Mode().Perm())
		}
	}
}

// Second sync with changed timestamp refetches; unchanged contacts don't.
func TestSyncDiff(t *testing.T) {
	p := testPlugin(t)
	dev := &fakeSender{id: "dev1"}

	first, _ := json.Marshal(map[string]any{"uids": []string{"u1"}, "u1": "100"})
	p.handleUIDsResponse(dev, first)
	if len(dev.sent) != 1 {
		t.Fatalf("first sync must request vcard, sent=%d", len(dev.sent))
	}
	vcards, _ := json.Marshal(map[string]any{
		"uids": []string{"u1"},
		"u1":   "BEGIN:VCARD\nFN:Alice\nEND:VCARD",
	})
	p.handleVCardsResponse("dev1", vcards)

	dev.sent = nil
	same, _ := json.Marshal(map[string]any{"uids": []string{"u1"}, "u1": "100"})
	p.handleUIDsResponse(dev, same)
	if len(dev.sent) != 0 {
		t.Error("unchanged contact must not be refetched")
	}

	dev.sent = nil
	changed, _ := json.Marshal(map[string]any{"uids": []string{"u1"}, "u1": "101"})
	p.handleUIDsResponse(dev, changed)
	if len(dev.sent) != 1 {
		t.Error("changed timestamp must trigger refetch")
	}
}

// Stale entries are deleted on non-empty responses, never on empty ones.
func TestSyncDeleteGuard(t *testing.T) {
	p := testPlugin(t)
	dev := &fakeSender{id: "dev1"}

	seed, _ := json.Marshal(map[string]any{"uids": []string{"u1"}, "u1": "1"})
	p.handleUIDsResponse(dev, seed)
	vcards, _ := json.Marshal(map[string]any{
		"uids": []string{"u1"},
		"u1":   "BEGIN:VCARD\nFN:Gone\nEND:VCARD",
	})
	p.handleVCardsResponse("dev1", vcards)
	if len(p.List("dev1")) != 1 {
		t.Fatal("seed failed")
	}

	// Empty response must not wipe.
	empty, _ := json.Marshal(map[string]any{"uids": []string{}})
	p.handleUIDsResponse(dev, empty)
	if len(p.List("dev1")) != 1 {
		t.Error("empty uids response must not delete cache")
	}

	// Non-empty response without u1 deletes it.
	without, _ := json.Marshal(map[string]any{"uids": []string{"u2"}, "u2": "2"})
	p.handleUIDsResponse(dev, without)
	for _, ct := range p.List("dev1") {
		if ct.UID == "u1" {
			t.Error("stale contact must be deleted on non-empty response")
		}
	}
}

func TestForgetDevice(t *testing.T) {
	p := testPlugin(t)
	dev := &fakeSender{id: "dev1"}

	seed, _ := json.Marshal(map[string]any{"uids": []string{"u1"}, "u1": "1"})
	p.handleUIDsResponse(dev, seed)
	vcards, _ := json.Marshal(map[string]any{
		"uids": []string{"u1"},
		"u1":   "BEGIN:VCARD\nFN:Gone\nEND:VCARD",
	})
	p.handleVCardsResponse("dev1", vcards)
	if len(p.List("dev1")) != 1 {
		t.Fatal("seed failed")
	}

	if err := p.ForgetDevice("dev1"); err != nil {
		t.Fatalf("ForgetDevice failed: %v", err)
	}
	if len(p.List("dev1")) != 0 {
		t.Error("cache must be empty after forget")
	}
	if _, err := os.Stat(filepath.Join(p.baseDir, "dev1")); !os.IsNotExist(err) {
		t.Error("device dir must be removed")
	}
}

func TestListEmptyWhenNeverSynced(t *testing.T) {
	p := testPlugin(t)
	if list := p.List("ghost"); len(list) != 0 {
		t.Errorf("never-synced device must list empty, got %v", list)
	}
}
