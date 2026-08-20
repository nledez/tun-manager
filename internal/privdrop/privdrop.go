// Package privdrop recovers the identity of the user who ran `sudo tun-manager`.
//
// The whole program runs as root, which breaks two things: HOME points at
// /var/root instead of the real user's home, and GUI-facing commands such as
// osascript cannot reach the user's session. Both are fixed by looking at
// SUDO_USER.
package privdrop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

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

// CacheDir returns the per-application cache directory of the user, for things
// the program can regenerate.
func (u User) CacheDir(app string) string {
	return filepath.Join(u.HomeDir, ".cache", app)
}

// CommandContext builds a command that runs as the pre-sudo user when possible,
// so that GUI-facing tools reach the right session. Without a demotable user
// the command is returned unchanged and simply runs as root.
//
// The context matters: a GUI tool with nobody to talk to can hang, and the
// caller must be able to give up on it.
func (u User) CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if !u.Demotable {
		return cmd
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID)},
	}
	cmd.Env = append(os.Environ(), "HOME="+u.HomeDir, "USER="+u.Username)
	return cmd
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
	f, err := fsx.CreateNoFollow(u.HomeDir, path, mode)
	if err != nil {
		return err
	}
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
	return f.Close()
}

// MkdirAll makes a directory under the user's home and hands it back to them,
// refusing to walk through a symbolic link on the way.
func (u User) MkdirAll(dir string, mode os.FileMode) error {
	if err := fsx.MkdirAllNoFollow(u.HomeDir, dir, mode); err != nil {
		return err
	}
	return u.Chown(dir)
}
