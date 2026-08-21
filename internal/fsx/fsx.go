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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	ReadFile     = os.ReadFile
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

// Umask sets the bits the kernel takes out of the mode of everything this
// process creates, and returns what it was. Process-wide, which is why the one
// caller holds it for as long as it takes to bind a socket and no longer.
var Umask = syscall.Umask

// Lchown is os.Lchown: it changes the owner of a symbolic link rather than of
// what the link points at. os.Chown is never used in this program. Root
// changing the owner of a path that somebody else can replace with a link is
// the shape of a dozen local privilege escalations, and the difference between
// the two calls is the whole of it.
var Lchown = os.Lchown

// FchownFile changes the owner of an open descriptor. There is no path in it to
// swap, which is what makes it the safe half of the pair above.
var FchownFile = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }

// NoSymlinksUnder refuses a path reached through a symbolic link somewhere
// below under.
//
// It lstats every step from the path up to that boundary. What it is looking
// for is a directory somebody else has replaced with a link: root writing "into
// ~/.cache/tun-manager" is root writing wherever that name points, and every
// name under a home directory belongs to whoever owns it.
//
// It stops at the boundary rather than walking to the root, because on darwin
// the way to almost anything crosses one: /var is a link to /private/var, /tmp
// to /private/tmp, and /etc to /private/etc. Those are the system's, set at
// install and not something a user can move; the ones worth refusing are the
// ones below a directory somebody can write.
func NoSymlinksUnder(under, path string) error {
	for name := path; under == "" || isUnder(under, name); name = Dir(name) {
		info, err := Lstat(name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Nothing there yet: whatever creates it will be made, not followed.
		case err != nil:
			return fmt.Errorf("look at %s: %w", name, err)
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf(
				"%s is a symbolic link: tun-manager will not follow one to write as root, "+
					"because what it points at is not its to choose", name)
		}
		// An empty boundary means "this name and no further": the caller does
		// not know where the writable part of the path begins, so only the
		// component being written is judged.
		if under == "" || name == Dir(name) {
			return nil
		}
	}
	return nil
}

// isUnder reports whether name sits inside dir, which is what bounds the walk.
func isUnder(dir, name string) bool {
	return name == dir || strings.HasPrefix(name, strings.TrimSuffix(dir, "/")+"/")
}

// CreateNoFollow opens a file for writing without following a symbolic link at
// the path, and without following one on the way to it from under.
//
// O_NOFOLLOW covers the last component; NoSymlinksUnder covers the rest. The
// caller closes what it gets.
func CreateNoFollow(under, path string, mode os.FileMode) (*os.File, error) {
	if err := NoSymlinksUnder(under, Dir(path)); err != nil {
		return nil, err
	}
	return OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
}

// randRead is where the unguessable part of a temporary name comes from. A
// variable so a test can make the draw fail: crypto/rand does not fail on
// darwin, and a name drawn from a source that had run out would be a
// predictable name, which is the one thing this must never be.
var randRead = rand.Read

// TempName draws the part of a temporary file name that nobody can guess.
//
// A variable so a test can pin it and plant something at the name it returns.
// The error is not swallowed into a fixed fallback: a predictable name in a
// directory somebody else can write is exactly the hole this exists to close,
// so not writing at all is the better failure.
var TempName = func() (string, error) {
	var raw [8]byte
	if _, err := randRead(raw[:]); err != nil {
		return "", fmt.Errorf("draw a temporary name: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// CreateFresh creates a file that did not exist a moment ago, and refuses
// anything that did.
//
// O_EXCL, which is the part O_NOFOLLOW cannot cover. A symbolic link at the
// name is refused by O_NOFOLLOW; a *hard* link is not, because there is nothing
// to follow - the name simply is the file. On darwin a plain user can make one
// to a root-owned file they can reach, so a name root is about to create in a
// directory that user can write is a name that may already be somebody else's
// file, and O_TRUNC on it is root emptying that file and writing this one's
// contents into it.
func CreateFresh(under, path string, mode os.FileMode) (*os.File, error) {
	if err := NoSymlinksUnder(under, Dir(path)); err != nil {
		return nil, err
	}
	return OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
}

// MkdirAllNoFollow makes a directory and its parents, refusing to walk through
// a symbolic link below under.
//
// os.MkdirAll follows one without a word, which is how a directory somebody
// replaced with a link becomes somewhere root writes.
func MkdirAllNoFollow(under, dir string, mode os.FileMode) error {
	if err := NoSymlinksUnder(under, dir); err != nil {
		return err
	}
	return MkdirAll(dir, mode)
}
