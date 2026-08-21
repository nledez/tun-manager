package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// MARK: refusing to write where somebody else pointed

func TestNoSymlinksUnderAcceptsARealPath(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".cache", "tun-manager")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := NoSymlinksUnder(home, dir); err != nil {
		t.Errorf("NoSymlinksUnder on a real path: %v", err)
	}
}

func TestNoSymlinksUnderRefusesALinkOnTheWay(t *testing.T) {
	// The attack this exists for: a directory somebody replaced with a link, so
	// that root writing "into ~/.cache/tun-manager" writes wherever they said.
	home := t.TempDir()
	elsewhere := t.TempDir()
	cache := filepath.Join(home, ".cache")
	if err := os.Symlink(elsewhere, cache); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := NoSymlinksUnder(home, filepath.Join(cache, "tun-manager"))

	if err == nil {
		t.Fatal("NoSymlinksUnder walked through a symbolic link")
	}
	if !strings.Contains(err.Error(), cache) {
		t.Errorf("error %q does not name the link", err)
	}
}

func TestNoSymlinksUnderStopsAtTheBoundary(t *testing.T) {
	// On darwin the way to almost anything crosses a link: /var points at
	// /private/var, and t.TempDir() lives under it. Walking past the boundary
	// would refuse every path this program writes.
	home := t.TempDir()
	dir := filepath.Join(home, "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := NoSymlinksUnder(home, dir); err != nil {
		t.Errorf("the walk went past the boundary: %v", err)
	}
}

func TestNoSymlinksUnderWithNoBoundaryJudgesOneName(t *testing.T) {
	// For a caller that does not know where the writable part of the path
	// begins: the component being written, and nothing above it.
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := NoSymlinksUnder("", link); err == nil {
		t.Error("a link at the name itself was accepted")
	}
	if err := NoSymlinksUnder("", dir); err != nil {
		t.Errorf("a real name was refused: %v", err)
	}
}

func TestNoSymlinksUnderAcceptsWhatIsNotThereYet(t *testing.T) {
	// The file about to be created. Whatever makes it will make it, not follow
	// something already at the name.
	home := t.TempDir()

	if err := NoSymlinksUnder(home, filepath.Join(home, "not-yet")); err != nil {
		t.Errorf("NoSymlinksUnder on a path that does not exist: %v", err)
	}
}

func TestNoSymlinksUnderReportsWhatItCannotLookAt(t *testing.T) {
	boom := errors.New("input/output error")
	previous := Lstat
	Lstat = func(string) (os.FileInfo, error) { return nil, boom }
	t.Cleanup(func() { Lstat = previous })

	home := t.TempDir()
	if err := NoSymlinksUnder(home, filepath.Join(home, "x")); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure to look", err)
	}
}

func TestCreateNoFollowRefusesALinkAtThePath(t *testing.T) {
	// O_NOFOLLOW covers the last component, which is the one somebody plants
	// while root is between two runs.
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "somebody-elses-file")
	if err := os.WriteFile(target, []byte("theirs"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := filepath.Join(home, "icon.png")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	f, err := CreateNoFollow(home, path, 0o644)
	if err == nil {
		f.Close() //nolint:errcheck
		t.Fatal("CreateNoFollow followed a symbolic link")
	}

	body, readErr := os.ReadFile(target)
	if readErr != nil || string(body) != "theirs" {
		t.Errorf("what the link pointed at was written through: %q, %v", body, readErr)
	}
}

func TestCreateNoFollowWritesARealFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "icon.png")

	f, err := CreateNoFollow(home, path, 0o644)
	if err != nil {
		t.Fatalf("CreateNoFollow: %v", err)
	}
	if _, writeErr := f.WriteString("a picture"); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	body, err := os.ReadFile(path)
	if err != nil || string(body) != "a picture" {
		t.Errorf("read back %q, %v", body, err)
	}
}

func TestMkdirAllNoFollowRefusesALinkOnTheWay(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	cache := filepath.Join(home, ".cache")
	if err := os.Symlink(elsewhere, cache); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := MkdirAllNoFollow(home, filepath.Join(cache, "tun-manager"), 0o755); err == nil {
		t.Fatal("MkdirAllNoFollow made a directory through a link")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "tun-manager")); !os.IsNotExist(err) {
		t.Error("it was made on the other side of the link")
	}
}

func TestMkdirAllNoFollowMakesRealDirectories(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".cache", "tun-manager")

	if err := MkdirAllNoFollow(home, dir, 0o755); err != nil {
		t.Fatalf("MkdirAllNoFollow: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Errorf("stat = %v, %v", info, err)
	}
}

func TestCreateNoFollowRefusesALinkOnTheWayToThePath(t *testing.T) {
	// Not the last component but the one above it, which O_NOFOLLOW says
	// nothing about.
	home := t.TempDir()
	elsewhere := t.TempDir()
	cache := filepath.Join(home, ".cache")
	if err := os.Symlink(elsewhere, cache); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	f, err := CreateNoFollow(home, filepath.Join(cache, "icon.png"), 0o644)

	if err == nil {
		f.Close() //nolint:errcheck
		t.Fatal("CreateNoFollow wrote through a link on the way")
	}
	if _, statErr := os.Stat(filepath.Join(elsewhere, "icon.png")); !os.IsNotExist(statErr) {
		t.Error("the file was written on the other side of the link")
	}
}

func TestCreateFreshRefusesAFileThatIsAlreadyThere(t *testing.T) {
	// The point of it. O_NOFOLLOW covers a symbolic link at the name and says
	// nothing about a hard link, because there is nothing to follow - the name
	// simply is the file, and on darwin a plain user can make one to a
	// root-owned file they can reach.
	dir := t.TempDir()
	path := filepath.Join(dir, "taken")
	if err := os.WriteFile(path, []byte("somebody else's"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if f, err := CreateFresh("", path, 0o600); err == nil {
		f.Close()
		t.Error("CreateFresh opened a file that was already there")
	}

	body, err := os.ReadFile(path)
	if err != nil || string(body) != "somebody else's" {
		t.Errorf("the file was touched: %q, %v", body, err)
	}
}

func TestCreateFreshWritesWhereNothingWasThere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new")

	f, err := CreateFresh("", path, 0o600)
	if err != nil {
		t.Fatalf("CreateFresh: %v", err)
	}
	if _, err := f.WriteString("mine"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if body, err := os.ReadFile(path); err != nil || string(body) != "mine" {
		t.Errorf("read back %q, %v", body, err)
	}
}

func TestCreateFreshWillNotWalkThroughALink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "elsewhere")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if f, err := CreateFresh(dir, filepath.Join(dir, "elsewhere", "new"), 0o600); err == nil {
		f.Close()
		t.Error("CreateFresh created a file through a symbolic link")
	}
}
