package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireStateLockExcludesSecondOwner(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "devices.json")
	first, err := AcquireStateLock(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := AcquireStateLock(statePath)
	if !errors.Is(err, ErrStateLocked) {
		t.Fatalf("second lock error = %v, want ErrStateLocked", err)
	}
	if second != nil {
		t.Fatal("second lock unexpectedly acquired")
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireStateLock(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}
