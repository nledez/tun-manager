package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerReadsTheOwnerOfARealFile(t *testing.T) {
	// Whoever runs the suite owns what the suite creates. That is the whole
	// assertion available without root, and it is enough to prove the stat is
	// being read rather than invented.
	path := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	uid, gid := Owner(path, info)

	if uid != os.Getuid() || gid != os.Getgid() {
		t.Errorf("Owner = %d:%d, want %d:%d", uid, gid, os.Getuid(), os.Getgid())
	}
}
