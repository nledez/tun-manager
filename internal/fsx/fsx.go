// Package fsx holds the filesystem calls this program makes, as variables.
//
// Every one of them is a call whose failure the caller handles and whose
// failure no test can arrange on a working machine: a stat of a file that was
// there a moment ago, a chmod of a directory this process has just made, a
// write to a descriptor it has just opened. Those branches are the ones that
// keep a permission check from reporting something false, so they are worth
// having and worth testing — and the only way to test them is to be able to
// make the call fail on purpose.
//
// The names are the standard library's, so a reader who knows os.Stat knows
// what fsx.Stat does. Swapped in tests, and assigned nowhere else.
package fsx

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// The filesystem, as this program reaches it.
var (
	Stat         = os.Stat
	Lstat        = os.Lstat
	Chmod        = os.Chmod
	Chown        = os.Chown
	Remove       = os.Remove
	Rename       = os.Rename
	MkdirAll     = os.MkdirAll
	ReadDir      = os.ReadDir
	OpenFile     = os.OpenFile
	EvalSymlinks = filepath.EvalSymlinks
	// StatFile is (*os.File).Stat, which is a method and so cannot be replaced
	// the way the calls above can. It is here because the checks that matter
	// are made on an open descriptor rather than on a path — checking a name
	// and then opening it are two lookups of something that can change in
	// between.
	StatFile = func(f *os.File) (os.FileInfo, error) { return f.Stat() }
	// CloseFile is (*os.File).Close, a method for the same reason StatFile is
	// one. A write is only really done once the close says so, and on a
	// filesystem that buffers, the close is where the failure arrives.
	CloseFile = func(f *os.File) error { return f.Close() }
	// WriteString is io.WriteString, for the writes whose failure a caller
	// reports rather than ignores.
	WriteString = io.WriteString
	// Dir is filepath.Dir. It is here because the walk up a path stops when a
	// directory is its own parent, and a test needs to be able to break that.
	Dir = filepath.Dir
)

// Root is the uid every file this program trusts is meant to have.
const Root = 0

// Owner reports the uid and gid behind a FileInfo.
//
// A variable like the rest, and for one more reason: a fixture is owned by
// whoever runs the suite, and making one root-owned would mean running the
// suite as root — a suite that only proves itself under sudo proves nothing on
// anybody else's machine.
var Owner func(path string, info os.FileInfo) (uid, gid int) = realOwner

// realOwner reads the uid and gid out of the stat behind a FileInfo. darwin
// only, like the rest of this program.
func realOwner(_ string, info os.FileInfo) (uid, gid int) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Not reachable through os.Stat on darwin, which always yields a
		// *syscall.Stat_t. It guards an io/fs implementation that does not —
		// and something that answers "nobody owns this" is safer than a panic
		// inside a permission check.
		return -1, -1
	}
	return int(stat.Uid), int(stat.Gid)
}
