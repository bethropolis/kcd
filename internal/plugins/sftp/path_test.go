package sftp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
)

func TestMountPointForCannotEscapeBase(t *testing.T) {
	base := t.TempDir()
	p := NewSftpPlugin(config.SFTPConfig{MountDir: base}, nil, log.Nop())
	mount := p.mountPointFor("../../../../../../tmp/evil")
	if mount == "" {
		return
	}
	rel, err := filepath.Rel(base, mount)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("mount path escaped configured base: %q (relative %q, err %v)", mount, rel, err)
	}
}
