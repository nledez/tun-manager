// Package privdrop recovers the identity of the user who ran `sudo tun-manager`.
//
// The whole program runs as root, which breaks two things: HOME points at
// /var/root instead of the real user's home, so the configuration would be read
// from the wrong place, and anything written under that home would belong to
// root rather than to the person who has to edit it next. Both are fixed by
// looking at SUDO_USER.
//
// It starts no processes. It used to run one - osascript, demoted, to post a
// notification - and that went with the notifications themselves: a program
// running as root starting a GUI process under somebody else's identity is a
// lot of machinery to keep for a banner, and Tun Manager.app posts them from a
// session that is already the right one.
package privdrop

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"ledez.net/tun-manager/internal/fsx"
)

// User is the pre-sudo identity.
type User struct {
	Username string
	UID      int
	GID      int
	HomeDir  string
	// Demotable reports whether commands can meaningfully be run as this user.
	// It is false when the program was not started through sudo, or was started
	// from a root shell.
	Demotable bool
}

// LookupFunc mirrors os/user.Lookup, so tests can inject a fake directory.
type LookupFunc func(username string) (*user.User, error)

// Current resolves the pre-sudo identity from the process environment.
func Current() (User, error) {
	return Resolve(os.Getenv, user.Lookup)
}

// Resolve reads SUDO_USER, falling back to HOME when the program was not
// started through sudo.
func Resolve(getenv func(string) string, lookup LookupFunc) (User, error) {
	name := getenv("SUDO_USER")
	if name == "" {
		return User{HomeDir: getenv("HOME")}, nil
	}

	u, err := lookup(name)
	if err != nil {
		return User{}, fmt.Errorf("resolve SUDO_USER=%q: %w", name, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return User{}, fmt.Errorf("resolve SUDO_USER=%q: bad uid %q", name, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return User{}, fmt.Errorf("resolve SUDO_USER=%q: bad gid %q", name, u.Gid)
	}

	return User{
		Username:  u.Username,
		UID:       uid,
		GID:       gid,
		HomeDir:   u.HomeDir,
		Demotable: uid != 0,
	}, nil
}

// ConfigDir returns the per-application configuration directory of the user.
func (u User) ConfigDir(app string) string {
	return filepath.Join(u.HomeDir, ".config", app)
}

// Chown hands a file created by the root process back to the real user, so the
// config and log files stay editable outside of sudo.
//
// Lchown rather than chown: it changes the owner of a symbolic link rather than
// of what the link points at. Every path here is under the user's own home,
// where that user can replace any name with a link at any moment — and root
// chowning the far end of one is how a file that was never theirs becomes
// theirs.
func (u User) Chown(path string) error {
	if !u.Demotable {
		return nil
	}
	return fsx.Lchown(path, u.UID, u.GID)
}

// WriteFile writes a file under the user's home and hands it back to them.
//
// Root writing "into ~/.cache/tun-manager" is root writing wherever that name
// points, and the name belongs to whoever owns the home directory. So no
// symbolic link is followed on the way, none at the path itself, and the owner
// is set on the open descriptor rather than on the path — there is no name in a
// descriptor to swap between the write and the chown.
func (u User) WriteFile(path string, data []byte, mode os.FileMode) error {
	// Through a file that did not exist a moment ago, under a name nobody could
	// have guessed, and then moved into place.
	//
	// Writing to the name directly was the hole. O_NOFOLLOW refuses a symbolic
	// link at it, and says nothing about a hard one - there is nothing to
	// follow, the name simply is the file - and on darwin a plain user can make
	// a hard link to a root-owned file they can reach. Left at this name, root
	// truncated that file, wrote into it, and then handed it to the user: a
	// file root owned became one they could fill in with anything. The rename
	// below replaces the *name*, so whatever was there keeps its contents and
	// its owner.
	suffix, err := fsx.TempName()
	if err != nil {
		return err
	}
	tmp := path + "." + suffix + ".tmp"
	f, err := fsx.CreateFresh(u.HomeDir, tmp, mode)
	if err != nil {
		return err
	}
	defer fsx.Remove(tmp) //nolint:errcheck // already gone once the rename succeeded

	if u.Demotable {
		if err := fsx.FchownFile(f, u.UID, u.GID); err != nil {
			f.Close() //nolint:errcheck // the chown is the failure being reported
			return fmt.Errorf("hand %s to %s: %w", path, u.Username, err)
		}
	}
	if _, err := f.Write(data); err != nil {
		f.Close() //nolint:errcheck // likewise
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := fsx.CloseFile(f); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return fsx.Rename(tmp, path)
}
