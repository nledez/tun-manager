package wg

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/fsx"
)

// errSeam is the failure the seams below return: a filesystem that moved between
// two adjacent calls, which is what these branches are written for and what no
// working machine will produce on demand.
var errSeam = errors.New("no such file or directory")

func failingStatOf(t *testing.T, want string) {
	t.Helper()

	previous := fsx.Stat
	fsx.Stat = func(path string) (fs.FileInfo, error) {
		if want == "" || path == want {
			return nil, errSeam
		}
		return previous(path)
	}
	t.Cleanup(func() { fsx.Stat = previous })
}

func TestCheckExecutableReportsABinaryThatMovedUnderIt(t *testing.T) {
	// EvalSymlinks walked to it a moment ago. What root would run is no longer
	// what was checked, and running it on the strength of the earlier look is
	// exactly the race this whole check exists for.
	path := anExecutable(t)
	failingStatOf(t, path)

	err := CheckExecutable(path, Strict{})

	if !errors.Is(err, errSeam) {
		t.Fatalf("err = %v, want the stat failure", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("refusal %q does not name the binary", err)
	}
}

func TestCheckExecutableReportsADirectoryThatMovedUnderIt(t *testing.T) {
	// The walk up the path, rather than the file itself.
	path := anExecutable(t)
	failingStatOf(t, filepath.Dir(path))

	if err := CheckExecutable(path, Strict{}); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the stat failure", err)
	}
}

func TestTheOwnershipWalkReportsWhatItCannotRead(t *testing.T) {
	// Asked for wg_quick_root_owned and unable to say who owns something on the
	// way: the answer has to be a refusal. Anything else would accept a binary
	// nobody vouched for.
	path := anExecutable(t)
	ownedBy(t, 0)
	previous := fsx.Lstat
	fsx.Lstat = func(string) (fs.FileInfo, error) { return nil, errSeam }
	t.Cleanup(func() { fsx.Lstat = previous })

	err := CheckExecutable(path, Strict{RootOwner: true})

	if !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the lstat failure", err)
	}
}
