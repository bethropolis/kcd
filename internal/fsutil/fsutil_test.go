package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain", "hello.txt", "hello.txt"},
		{"unix path traversal", "../../../etc/passwd", "passwd"},
		{"windows path traversal", "..\\..\\windows\\system32\\cmd.exe", "cmd.exe"},
		{"nested backslash", "foo\\bar\\baz.jpg", "baz.jpg"},
		{"empty", "", "downloaded_file"},
		{"dot", ".", "downloaded_file"},
		{"double dot", "..", "downloaded_file"},
		{"multiple leading dots", "...hidden.txt", "hidden.txt"},
		{"leading dot", ".bashrc", "bashrc"},
		{"control characters", "foo\x00\x01\x1f\nbar.txt", "foobar.txt"},
		{"whitespace trim", "  spaced.txt  ", "spaced.txt"},
		{"unicode valid", "foto_ñoño.png", "foto_ñoño.png"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeFilename(tc.input)
			if got != tc.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestSanitizeFilenameCustomFallback(t *testing.T) {
	got := SanitizeFilename("../..", "attachment_file")
	if got != "attachment_file" {
		t.Fatalf("expected custom fallback, got %q", got)
	}
}

func TestSanitizeFilenameLengthCap(t *testing.T) {
	longName := strings.Repeat("a", 300) + ".txt"
	sanitized := SanitizeFilename(longName)
	if len(sanitized) > MaxFilenameBytes {
		t.Fatalf("len = %d, want <= %d", len(sanitized), MaxFilenameBytes)
	}
}

func TestCreateUniqueNoCollision(t *testing.T) {
	dir := t.TempDir()
	f1, path1, err := CreateUnique(dir, "report.pdf")
	if err != nil {
		t.Fatalf("CreateUnique 1 failed: %v", err)
	}
	defer f1.Close()

	if filepath.Base(path1) != "report.pdf" {
		t.Errorf("expected report.pdf, got %s", filepath.Base(path1))
	}

	f2, path2, err := CreateUnique(dir, "report.pdf")
	if err != nil {
		t.Fatalf("CreateUnique 2 failed: %v", err)
	}
	defer f2.Close()

	if filepath.Base(path2) != "report_1.pdf" {
		t.Errorf("expected report_1.pdf, got %s", filepath.Base(path2))
	}
}

func TestCreateUniqueConcurrent(t *testing.T) {
	dir := t.TempDir()
	const count = 20
	paths := make([]string, count)
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			f, p, err := CreateUnique(dir, "concurrent.dat")
			if err != nil {
				t.Errorf("concurrent CreateUnique failed: %v", err)
				return
			}
			defer f.Close()
			paths[idx] = p
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, p := range paths {
		if p == "" {
			continue
		}
		if seen[p] {
			t.Errorf("duplicate path generated concurrently: %s", p)
		}
		seen[p] = true
	}
	if len(seen) != count {
		t.Errorf("expected %d unique files, got %d", count, len(seen))
	}
}

func FuzzSanitizeFilename(f *testing.F) {
	seeds := []string{
		"", "hello.txt", "foo/bar/baz.txt", "foo\\bar\\baz.txt",
		"..", ".", "../../../etc/passwd", "..\\windows\\system32",
		"\x00\x01\x1f\n\r", "   ", strings.Repeat("x", 400),
		"日本語ファイル名.pdf", "emoji_🎉_test.png",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SanitizeFilename(in)
		if out == "" {
			t.Errorf("SanitizeFilename(%q) returned empty", in)
		}
		if len(out) > MaxFilenameBytes {
			t.Errorf("SanitizeFilename(%q) exceeded MaxFilenameBytes: len %d", in, len(out))
		}
		if strings.ContainsAny(out, "/\\\x00") {
			t.Errorf("SanitizeFilename(%q) = %q contains invalid characters", in, out)
		}
		if strings.HasPrefix(out, ".") {
			t.Errorf("SanitizeFilename(%q) = %q has leading dot", in, out)
		}
		if _, err := os.Stat(filepath.Join(os.TempDir(), out)); err != nil && !os.IsNotExist(err) {
			t.Errorf("SanitizeFilename(%q) = %q invalid path: %v", in, out, err)
		}
	})
}
