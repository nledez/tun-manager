package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/fsx"
	"ledez.net/tun-manager/internal/privdrop"
)

// errSeam is the failure every seam below is made to return. One error, compared
// with errors.Is, so a test proves the reason reached the caller rather than
// that something failed.
var errSeam = errors.New("input/output error")

// failing swaps one filesystem call for one that fails, for the length of a
// test. The calls it stands in for are the ones whose failure cannot be
// arranged on a working machine — a stat of a file that was there a moment ago,
// a chmod of a directory this process has just made — and whose handling is the
// difference between a permission check that is trustworthy and one that is
// merely reassuring.
func failingStat(t *testing.T, on string) {
	t.Helper()

	previous := fsx.Stat
	fsx.Stat = func(path string) (fs.FileInfo, error) {
		if on == "" || path == on {
			return nil, errSeam
		}
		return previous(path)
	}
	t.Cleanup(func() { fsx.Stat = previous })
}

func failingLstat(t *testing.T, on string) {
	t.Helper()

	previous := fsx.Lstat
	fsx.Lstat = func(path string) (fs.FileInfo, error) {
		if on == "" || path == on {
			return nil, errSeam
		}
		return previous(path)
	}
	t.Cleanup(func() { fsx.Lstat = previous })
}

func failingChmod(t *testing.T) {
	t.Helper()

	previous := fsx.Chmod
	fsx.Chmod = func(string, os.FileMode) error { return errSeam }
	t.Cleanup(func() { fsx.Chmod = previous })
}

func failingRename(t *testing.T) {
	t.Helper()

	previous := fsx.Rename
	fsx.Rename = func(string, string) error { return errSeam }
	t.Cleanup(func() { fsx.Rename = previous })
}

func failingOpenFile(t *testing.T) {
	t.Helper()

	previous := fsx.OpenFile
	fsx.OpenFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errSeam }
	t.Cleanup(func() { fsx.OpenFile = previous })
}

func failingWrite(t *testing.T) {
	t.Helper()

	previous := fsx.WriteString
	fsx.WriteString = func(io.Writer, string) (int, error) { return 0, errSeam }
	t.Cleanup(func() { fsx.WriteString = previous })
}

// vanishing lists a directory the way ReadDir does, and then loses the file:
// ReadDir does not stat, so an entry's Info can fail for something deleted
// between the listing and the read.
func vanishing(t *testing.T) {
	t.Helper()

	previous := fsx.ReadDir
	fsx.ReadDir = func(dir string) ([]os.DirEntry, error) {
		entries, err := previous(dir)
		if err != nil {
			return nil, err
		}
		for i, entry := range entries {
			entries[i] = goneEntry{entry}
		}
		return entries, nil
	}
	t.Cleanup(func() { fsx.ReadDir = previous })
}

// goneEntry is a directory entry whose file has been removed since the listing.
type goneEntry struct{ os.DirEntry }

func (goneEntry) Info() (fs.FileInfo, error) { return nil, errSeam }

// MARK: the branches those seams exist to reach

func TestAConfigDirectoryThatVanishesUnderTheCheckIsReported(t *testing.T) {
	// The parent is statted immediately after config_dir itself. Something
	// removing it in between is the only way here, and what matters is that the
	// check says so rather than passing on what it could not read.
	ownedBy(t, 0)
	cfg := aLayout(t)
	failingStat(t, filepath.Dir(cfg.ConfigDir))

	c := check(t, Permissions(cfg, operator), "config dir mode")

	if c.Status != Fail {
		t.Errorf("status = %v, want %v: %s", c.Status, Fail, c.Detail)
	}
	if !strings.Contains(c.Detail, filepath.Dir(cfg.ConfigDir)) {
		t.Errorf("detail %q does not name what could not be read", c.Detail)
	}
}

func TestATunnelThatVanishesBetweenTheListingAndTheReadIsReported(t *testing.T) {
	// ReadDir does not stat. A .conf removed between the listing and the read
	// leaves an entry whose Info fails, and a check that skipped it silently
	// would report "3 files 0600" having looked at two.
	ownedBy(t, 0)
	cfg := aLayout(t)
	vanishing(t)

	c := check(t, Permissions(cfg, operator), "tunnel files")

	if c.Status != Fail {
		t.Errorf("status = %v, want %v: %s", c.Status, Fail, c.Detail)
	}
	if !strings.Contains(c.Detail, "alpha.conf") {
		t.Errorf("detail %q does not name the file it could not read", c.Detail)
	}
}

func TestImportReportsADirectoryItCannotTighten(t *testing.T) {
	// import is the command that puts a key in that directory, so it is the one
	// that has to make sure the place is fit to hold it. A chmod it cannot make
	// is a reason to stop, not to write the key anyway.
	cfg, source := importEnv(t, importable)
	failingChmod(t)

	err := Import(&strings.Builder{}, Assumed(true), cfg, privdrop.User{}, "alpha", source)

	if !errors.Is(err, errSeam) {
		t.Fatalf("err = %v, want the chmod failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.ConfigDir, "alpha.conf")); !os.IsNotExist(statErr) {
		t.Error("the key was written into a directory that could not be tightened")
	}
}

func TestInitPrivilegedReportsAKeyItCannotDraw(t *testing.T) {
	// crypto/rand does not fail on darwin. A source that ran out would yield a
	// key with fewer bits than it claims, and nothing downstream could tell.
	previous := generateSeed
	generateSeed = func() (string, error) { return "", errSeam }
	t.Cleanup(func() { generateSeed = previous })

	err := InitPrivileged(&strings.Builder{}, initTarget(t), false)

	if !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the failure to draw a key", err)
	}
}

func TestInitPrivilegedReportsAKeyItCannotTakeAFingerprintOf(t *testing.T) {
	// The generator and the reader disagreeing about what a seed is. A key
	// nobody can take a fingerprint of is a key nobody can compare against the
	// menu bar, which is what the key is for.
	previous := generateSeed
	generateSeed = func() (string, error) { return "not a seed", nil }
	t.Cleanup(func() { generateSeed = previous })

	err := InitPrivileged(&strings.Builder{}, initTarget(t), false)

	if err == nil {
		t.Fatal("InitPrivileged wrote a key it could not read back")
	}
	if strings.Contains(err.Error(), "not a seed") {
		t.Errorf("error %q prints the seed", err)
	}
}

func TestInitPrivilegedReportsAFileItCannotCreate(t *testing.T) {
	failingOpenFile(t)

	if err := InitPrivileged(&strings.Builder{}, initTarget(t), false); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the create failure", err)
	}
}

func TestInitPrivilegedReportsAFileItCannotWrite(t *testing.T) {
	// A disk that filled up between the create and the write.
	path := initTarget(t)
	failingWrite(t)

	err := InitPrivileged(&strings.Builder{}, path, false)

	if !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the write failure", err)
	}
}

func TestInitPrivilegedReportsADirectoryItCannotTighten(t *testing.T) {
	failingChmod(t)

	if err := InitPrivileged(&strings.Builder{}, initTarget(t), false); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the chmod failure", err)
	}
}

func TestInitPrivilegedReportsAPreviousItCannotKeep(t *testing.T) {
	// --force moves the old configuration aside before writing. If it cannot,
	// the old key is about to be lost, and going on would be the one thing
	// worse than stopping.
	path := initTarget(t)
	initialised(t, path, false)
	failingRename(t)

	err := InitPrivileged(&strings.Builder{}, path, true)

	if !errors.Is(err, errSeam) {
		t.Fatalf("err = %v, want the rename failure", err)
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil || !strings.Contains(string(body), "feed_key") {
		t.Error("the previous configuration was lost anyway")
	}
}

func TestAskReportsAQuestionItCannotPut(t *testing.T) {
	// A question nobody saw must not be answered on their behalf.
	_, err := Ask(strings.NewReader("y\n"), failingWriter{err: errSeam})("import alpha?")

	if !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the write failure", err)
	}
}

func TestTheOwnershipWalkStopsWhereItCannotRead(t *testing.T) {
	// Something moved while the report was being written. What has been walked
	// so far is what can be said about it, and saying nothing is right: the
	// refusal above is where a real problem gets reported.
	cfg, priv, u := healthyEnv(t)
	ownedBy(t, 0)
	failingLstat(t, "")

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Pass {
		t.Errorf("status = %v, want %v: %s", c.Status, Pass, c.Detail)
	}
}

func TestInitPrivilegedReportsAFileItCannotClose(t *testing.T) {
	// On a filesystem that buffers, the close is where a full disk arrives. A
	// configuration whose last bytes never landed is not one to report as
	// written.
	previous := fsx.CloseFile
	fsx.CloseFile = func(f *os.File) error {
		_ = f.Close()
		return errSeam
	}
	t.Cleanup(func() { fsx.CloseFile = previous })

	if err := InitPrivileged(&strings.Builder{}, initTarget(t), false); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the close failure", err)
	}
}

func TestInitPrivilegedReportsADirectoryItCannotLookAt(t *testing.T) {
	// Not "it is not there" — something else: a filesystem that has gone away,
	// a parent that cannot be traversed. Treating that as absent would have it
	// try to create a directory over whatever is there.
	failingLstat(t, "")

	err := InitPrivileged(&strings.Builder{}, initTarget(t), false)

	if !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the stat failure", err)
	}
}

func TestTheOwnershipWalkFallsBackToTheNameItWasGiven(t *testing.T) {
	// The binary moved between the refusal check and the report. The name as
	// given is all there is left to walk, and walking it is better than saying
	// nothing about ownership at all.
	cfg, priv, u := healthyEnv(t)
	ownedPerPath(t, map[string]int{"wg-quick": 501}, 0)
	// The refusal above resolves it first and must go on working: what is being
	// covered is the report walking a path that moved since.
	previous := fsx.EvalSymlinks
	resolved := 0
	fsx.EvalSymlinks = func(path string) (string, error) {
		resolved++
		if resolved > 1 {
			return "", errSeam
		}
		return previous(path)
	}
	t.Cleanup(func() { fsx.EvalSymlinks = previous })

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Warn {
		t.Errorf("status = %v, want %v: %s", c.Status, Warn, c.Detail)
	}
}
