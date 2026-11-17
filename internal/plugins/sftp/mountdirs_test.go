package sftp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseUserDirsLine(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	tests := []struct {
		name      string
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{name: "comment ignored", line: "# generated", wantOK: false},
		{name: "blank ignored", line: "   ", wantOK: false},
		{name: "no equals sign ignored", line: "garbage", wantOK: false},
		{name: "empty value ignored", line: `XDG_DESKTOP_DIR=""`, wantOK: false},
		{
			name:    "double quoted with $HOME",
			line:    `XDG_DOWNLOAD_DIR="$HOME/Descargas"`,
			wantKey: "XDG_DOWNLOAD_DIR", wantValue: filepath.Join(home, "Descargas"), wantOK: true,
		},
		{
			name:    "single quoted with $HOME",
			line:    `XDG_DOCUMENTS_DIR='$HOME/Dokumente'`,
			wantKey: "XDG_DOCUMENTS_DIR", wantValue: filepath.Join(home, "Dokumente"), wantOK: true,
		},
		{
			name:    "unquoted value",
			line:    `XDG_DESKTOP_DIR=/srv/desktop`,
			wantKey: "XDG_DESKTOP_DIR", wantValue: "/srv/desktop", wantOK: true,
		},
		{
			name:    "spaces around the equals sign",
			line:    `XDG_DOWNLOAD_DIR = "$HOME/Downloads"`,
			wantKey: "XDG_DOWNLOAD_DIR", wantValue: filepath.Join(home, "Downloads"), wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, value, ok := parseUserDirsLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if key != tc.wantKey || value != tc.wantValue {
				t.Errorf("got (%q, %q), want (%q, %q)", key, value, tc.wantKey, tc.wantValue)
			}
		})
	}
}

// A relocated Downloads must still be recognised, or the check misses exactly
// the setups where a user has customised their folders.
func TestUserBulkDeletableDirs_HonorsUserDirsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user-dirs.dirs")
	body := `XDG_DOWNLOAD_DIR="$HOME/Descargas"` + "\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := userDirsFile
	userDirsFile = file
	t.Cleanup(func() { userDirsFile = orig })

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	want := filepath.Join(home, "Descargas")

	if got := containingBulkDeletableDir(filepath.Join(want, "kcd", "mnt", "kcd-sftp-dev1")); got != want {
		t.Errorf("containingDocumentDir = %q, want %q", got, want)
	}
	// The conventional name must no longer match once it has been relocated.
	if got := containingBulkDeletableDir(filepath.Join(home, "Downloads", "kcd")); got != "" {
		t.Errorf("still matched the relocated-away Downloads: %q", got)
	}
}

func TestContainingBulkDeletableDir(t *testing.T) {
	orig := userDirsFile
	userDirsFile = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { userDirsFile = orig })

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{"inside Downloads", filepath.Join(home, "Downloads", "kcd", "mnt"), filepath.Join(home, "Downloads")},
		{"Downloads itself", filepath.Join(home, "Downloads"), filepath.Join(home, "Downloads")},
		{"inside Documents", filepath.Join(home, "Documents", "x"), filepath.Join(home, "Documents")},
		{"inside Desktop", filepath.Join(home, "Desktop", "x"), filepath.Join(home, "Desktop")},
		// Clearing old photos is at least as likely as clearing downloads, so
		// narrowing this to three folders would be arbitrary.
		{"inside Pictures", filepath.Join(home, "Pictures", "x"), filepath.Join(home, "Pictures")},
		{"inside Music", filepath.Join(home, "Music", "x"), filepath.Join(home, "Music")},
		{"inside Videos", filepath.Join(home, "Videos", "x"), filepath.Join(home, "Videos")},
		{"home itself is not a bulk-deletable dir", home, ""},
		{"runtime dir is not a bulk-deletable dir", "/run/user/1000/kcd/mnt", ""},
		{"sibling with a shared prefix", filepath.Join(home, "Downloads-old", "kcd"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := containingBulkDeletableDir(tc.path); got != tc.want {
				t.Errorf("containingBulkDeletableDir(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// The default location must never trip the warning; that is the point of it.
func TestDefaultMountDir_DoesNotWarn(t *testing.T) {
	p := newTestPlugin(t, t.TempDir())
	if owner := containingBulkDeletableDir(p.mountPointFor("dev1")); owner != "" {
		t.Errorf("default mount point flagged as inside %q", owner)
	}
	p.warnIfInBulkDeletableDir(p.mountPointFor("dev1")) // must not panic
}

// The warning fires for a user who pinned a hazardous location, and only once.
func TestWarnIfInBulkDeletableDir_FiresOncePerDir(t *testing.T) {
	orig := userDirsFile
	userDirsFile = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { userDirsFile = orig })

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	p := newTestPlugin(t, t.TempDir())
	hazardous := filepath.Join(home, "Downloads", "kcd", "mnt", "kcd-sftp-dev1")

	p.warnIfInBulkDeletableDir(hazardous)
	if !p.warnedDirs[hazardous] {
		t.Fatal("expected the hazardous directory to be recorded as warned")
	}
	p.warnIfInBulkDeletableDir(hazardous) // second time: no new warning
}

// The warning must reach a user who reuses an existing mount, not only one
// creating a new one -- otherwise the location is never mentioned to them.
func TestWarnFiresOnReusedMount(t *testing.T) {
	orig := userDirsFile
	userDirsFile = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { userDirsFile = orig })

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	p := newTestPlugin(t, filepath.Join(home, "Pictures", "kcd", "mnt"))
	hazardous := p.mountPointFor("dev1")
	useFakeMountTable(t, "phone:/storage/emulated/0 "+hazardous+" fuse.sshfs rw 0 0\n")
	p.mountPoints["dev1"] = hazardous

	// Already mounted, so mountWithBody takes the reuse path.
	if _, err := p.mountWithBody(context.Background(), "dev1", SftpBody{}, "", false); err != nil {
		t.Fatalf("reuse path: %v", err)
	}
	if !p.warnedDirs[hazardous] {
		t.Error("hazardous location not warned about on the reuse path")
	}
}
