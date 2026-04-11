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
