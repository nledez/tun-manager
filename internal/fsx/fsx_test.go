package fsx

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestOwnerOfSomethingWithNoStatBehindItOwnsNothing(t *testing.T) {
	// An io/fs implementation whose Sys() is not a *syscall.Stat_t. Answering
	// "nobody owns this" is what keeps a permission check from panicking, and
	// -1 matches no uid, so nothing is accepted on the strength of it.
	uid, gid := Owner("anywhere", statless{})

	if uid != -1 || gid != -1 {
		t.Errorf("Owner = %d:%d, want -1:-1", uid, gid)
	}
}

// statless is a FileInfo with nothing underneath it, which is what an io/fs
// implementation other than the operating system's hands back.
type statless struct{}

func (statless) Name() string       { return "anywhere" }
func (statless) Size() int64        { return 0 }
func (statless) Mode() fs.FileMode  { return 0 }
func (statless) ModTime() time.Time { return time.Time{} }
func (statless) IsDir() bool        { return false }
func (statless) Sys() any           { return nil }
