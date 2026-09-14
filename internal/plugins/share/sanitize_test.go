package share

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"cat.jpg", "cat.jpg"},
		{"/etc/shadow", "shadow"},
		{"../../../etc/passwd", "passwd"},
		{"..\\..\\windows\\system32.dll", "system32.dll"},
		{".", "downloaded_file"},
		{"", "downloaded_file"},
		{"foo/bar/baz.png", "baz.png"},
	}

	for _, tt := range tests {
		actual := SanitizeFilename(tt.input)
		if actual != tt.expected {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, actual, tt.expected)
		}
	}
}

func TestIsOpenableURL(t *testing.T) {
	open := []string{
		"https://example.com",
		"http://example.com/path?q=1",
		"HTTPS://EXAMPLE.COM",
		"  https://example.com/pad  ",
	}
	for _, u := range open {
		if !isOpenableURL(u) {
			t.Errorf("isOpenableURL(%q) = false, want true", u)
		}
	}
	blocked := []string{
		"",
		"file:///etc/passwd",
		"ftp://example.com/x",
		"smb://server/share",
		"mailto:foo@example.com",
		"javascript:alert(1)",
		"data:text/html,hi",
		"kdeconnect:/something",
		"myapp://action",
		"http://",
		"https://",
		"example.com/no-scheme",
		"/just/a/path",
		"http:noslashes",
	}
	for _, u := range blocked {
		if isOpenableURL(u) {
			t.Errorf("isOpenableURL(%q) = true, want false", u)
		}
	}
}

func TestAutoOpenBlocked(t *testing.T) {
	blocked := []string{
		"evil.desktop", "run.sh", "payload.bin", "setup.run", "app.jar",
		"x.py", "X.PY", "s.pl", "r.rb", "p.php", "a.exe", "b.msi",
		"c.bat", "d.cmd", "e.com", "f.scr", "g.ps1", "h.vbs", "i.lnk",
		"EVIL.Desktop",
	}
	for _, n := range blocked {
		if !autoOpenBlocked(n) {
			t.Errorf("autoOpenBlocked(%q) = false, want true", n)
		}
	}
	allowed := []string{"photo.jpg", "song.mp3", "doc.pdf", "movie.mp4", "notes.txt", "archive.zip", "noext"}
	for _, n := range allowed {
		if autoOpenBlocked(n) {
			t.Errorf("autoOpenBlocked(%q) = true, want false", n)
		}
	}
}

func TestEnsureUnique(t *testing.T) {
	dir := t.TempDir()
	name := "test.txt"

	p1, err := EnsureUnique(dir, name)
	if err != nil {
		t.Fatalf("EnsureUnique failed: %v", err)
	}
	if filepath.Base(p1) != "test.txt" {
		t.Errorf("expected test.txt, got %s", p1)
	}

	// Create the file
	os.WriteFile(p1, []byte("hello"), 0644)

	p2, err := EnsureUnique(dir, name)
	if err != nil {
		t.Fatalf("EnsureUnique failed: %v", err)
	}
	if filepath.Base(p2) != "test_1.txt" {
		t.Errorf("expected test_1.txt, got %s", p2)
	}

	os.WriteFile(p2, []byte("hello"), 0644)
	p3, err := EnsureUnique(dir, name)
	if err != nil {
		t.Fatalf("EnsureUnique failed: %v", err)
	}
	if filepath.Base(p3) != "test_2.txt" {
		t.Errorf("expected test_2.txt, got %s", p3)
	}
}
