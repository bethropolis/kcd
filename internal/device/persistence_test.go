package device

import (
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/log"
)

func TestRegistrySaveLoadPreservesPersistentFieldsAndResetsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	lastSeen := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	reg := NewRegistry(nil)
	paired := NewDevice("paired", "Phone", "phone", log.Nop())
	paired.SetState(StatePaired)
	paired.CertFP = "fingerprint"
	paired.SetLastSeen(lastSeen)
	paired.SetLastIP(net.ParseIP("192.0.2.4"))
	paired.SetLastPort(1740)
	reg.Add(paired)
	pending := NewDevice("pending", "Tablet", "tablet", log.Nop())
	pending.SetState(StatePairRequestedByPeer)
	reg.Add(pending)
	if err := reg.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded := NewRegistry(nil)
	if err := loaded.Load(path, log.Nop()); err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.Get("paired")
	if !ok {
		t.Fatal("paired device not loaded")
	}
	info := got.Info()
	if info.State != StatePaired || info.CertFP != "fingerprint" || !info.LastSeen.Equal(lastSeen) || info.LastIP != "192.0.2.4" || info.LastPort != 1740 {
		t.Fatalf("persistent fields changed: %+v", info)
	}
	got, ok = loaded.Get("pending")
	if !ok || got.State() != StateUnpaired {
		t.Fatalf("pending device after load = %v, %v", got, ok)
	}
}

func TestRegistryConcurrentSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	reg := NewRegistry(nil)
	reg.Add(NewDevice("dev", "Phone", "phone", log.Nop()))
	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- reg.Save(path) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Save: %v", err)
		}
	}
	if _, err := LoadDevices(path); err != nil {
		t.Fatalf("saved JSON is invalid: %v", err)
	}
}
