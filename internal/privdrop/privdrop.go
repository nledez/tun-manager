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

	// chown is the call Chown makes. It is a field because a real chown can
	// only ever be made to the identity the test process already has, which
	// proves nothing about whether the right one was chosen - and asking for
	// any other identity fails, unless the suite happens to run under sudo, in
	// which case it succeeds instead. Neither outcome is a test.
	chown func(path string, uid, gid int) error
}

func (u User) chownFn() func(string, int, int) error {
	if u.chown != nil {
		return u.chown
	}
	return os.Chown
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
func (u User) Chown(path string) error {
	if !u.Demotable {
		return nil
	}
	return u.chownFn()(path, u.UID, u.GID)
}
