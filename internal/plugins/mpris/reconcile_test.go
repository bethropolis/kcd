package mpris

import (
	"testing"
)

func TestDiffTrackedAddsMissing(t *testing.T) {
	entries := []playerEntry{
		{busName: "org.mpris.MediaPlayer2.firefox.instance_1_99", shortName: "firefox", identity: "Mozilla firefox"},
		{busName: "org.mpris.MediaPlayer2.spotify", shortName: "spotify", identity: "Spotify"},
	}
	add, drop := diffTracked(entries, map[string]string{})
	if len(add) != 2 || len(drop) != 0 {
		t.Fatalf("add=%d drop=%d, want add=2 drop=0", len(add), len(drop))
	}
}

func TestDiffTrackedSteadyState(t *testing.T) {
	entries := []playerEntry{
		{busName: "org.mpris.MediaPlayer2.spotify", shortName: "spotify", identity: "Spotify"},
	}
	tracked := map[string]string{"org.mpris.MediaPlayer2.spotify": "Spotify"}
	add, drop := diffTracked(entries, tracked)
	if len(add) != 0 || len(drop) != 0 {
		t.Fatalf("add=%d drop=%d, want no-op", len(add), len(drop))
	}
}

func TestDiffTrackedDropsReleased(t *testing.T) {
	tracked := map[string]string{"org.mpris.MediaPlayer2.spotify": "Spotify"}
	add, drop := diffTracked(nil, tracked)
	if len(add) != 0 || len(drop) != 1 || drop[0] != "org.mpris.MediaPlayer2.spotify" {
		t.Fatalf("add=%v drop=%v, want drop=[spotify bus]", add, drop)
	}
}

// Instance churn: old Firefox instance released, new one acquired.
// Both directions must be reported so the map converges.
func TestDiffTrackedInstanceChurn(t *testing.T) {
	entries := []playerEntry{
		{busName: "org.mpris.MediaPlayer2.firefox.instance_1_99", shortName: "firefox", identity: "Mozilla firefox"},
	}
	tracked := map[string]string{"org.mpris.MediaPlayer2.firefox.instance_1_98": "Mozilla firefox"}
	add, drop := diffTracked(entries, tracked)
	if len(add) != 1 || add[0].busName != "org.mpris.MediaPlayer2.firefox.instance_1_99" {
		t.Fatalf("add=%v, want [instance_1_99]", add)
	}
	if len(drop) != 1 || drop[0] != "org.mpris.MediaPlayer2.firefox.instance_1_98" {
		t.Fatalf("drop=%v, want [instance_1_98]", drop)
	}
}

// Two live players sharing one Identity are tracked independently by bus
// name, so evicting one never implies evicting the other.
func TestDiffTrackedDuplicateIdentity(t *testing.T) {
	entries := []playerEntry{
		{busName: "org.mpris.MediaPlayer2.firefox.instance_1_99", shortName: "firefox", identity: "Mozilla firefox"},
	}
	tracked := map[string]string{
		"org.mpris.MediaPlayer2.firefox.instance_1_98": "Mozilla firefox",
		"org.mpris.MediaPlayer2.firefox.instance_1_99": "Mozilla firefox",
	}
	add, drop := diffTracked(entries, tracked)
	if len(add) != 0 {
		t.Fatalf("add=%v, want none (1_99 already tracked)", add)
	}
	if len(drop) != 1 || drop[0] != "org.mpris.MediaPlayer2.firefox.instance_1_98" {
		t.Fatalf("drop=%v, want only [instance_1_98]", drop)
	}
}
