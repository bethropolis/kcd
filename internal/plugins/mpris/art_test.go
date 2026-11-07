package mpris

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthorizeOutboundArt(t *testing.T) {
	tmpDir := t.TempDir()
	validCover := filepath.Join(tmpDir, "valid-cover.jpg")
	if err := os.WriteFile(validCover, []byte("fake-cover"), 0600); err != nil {
		t.Fatal(err)
	}
	privateKey := filepath.Join(tmpDir, "id_rsa")
	if err := os.WriteFile(privateKey, []byte("private-key"), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkFile := filepath.Join(tmpDir, "symlink-cover.jpg")
	if err := os.Symlink(validCover, symlinkFile); err != nil {
		t.Fatal(err)
	}

	p := &MPRISPlugin{
		players: map[string]*trackedPlayer{
			"Spotify": {displayName: "Spotify", shortName: "spotify"},
		},
		lastStates: map[string]*NowPlaying{
			"Spotify": {Player: "Spotify", AlbumArtUrl: "file://" + validCover + "?t=123456"},
		},
	}

	tests := []struct {
		name        string
		player      string
		reqURL      string
		expectError bool
	}{
		{"exact advertised cover allowed", "Spotify", "file://" + validCover, false},
		{"exact advertised cover with timestamp allowed", "Spotify", "file://" + validCover + "?t=123456", false},
		{"shortName player lookup matches", "spotify", "file://" + validCover, false},
		{"arbitrary private file rejected", "Spotify", "file://" + privateKey, true},
		{"path traversal attack rejected", "Spotify", "file://" + validCover + "/../../id_rsa", true},
		{"system file rejected", "Spotify", "file:///etc/passwd", true},
		{"unknown player rejected", "VLC", "file://" + validCover, true},
		{"non-file url scheme rejected", "Spotify", "http://example.com/cover.jpg", true},
		{"symlink rejected", "Spotify", "file://" + symlinkFile, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, err := p.authorizeOutboundArt(test.player, test.reqURL)
			if test.expectError && err == nil {
				t.Fatalf("expected error, got authorized path: %s", path)
			}
			if !test.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !test.expectError && path != validCover {
				t.Fatalf("expected %s, got %s", validCover, path)
			}
		})
	}
}
