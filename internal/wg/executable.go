package wg

import (
	"fmt"
	"os"

	"ledez.net/tun-manager/internal/fsx"
)

// Strict names the two rules that cannot be defaults.
//
// Both are off unless somebody asks for them, and what they are worth depends
// on how wg-quick was installed. `brew install wireguard-tools` leaves
// /opt/homebrew/bin/wg-quick as a link into ../Cellar, both owned by the user
// who ran brew: turning either on would refuse the installation the README
// documents. An installation put somewhere root owns end to end - copied into
// /usr/local/sbin, chowned to root - can have both, and should.
//
// They live in the root-only half of the configuration, like everything else
// that decides what root will run.
type Strict struct {
	// RootOwner refuses a wg-quick that root does not own, or that sits under
	// a directory root does not own, or that a group can write. It is the only
	// rule that closes the hole a package manager leaves: the owner of a file
	// can replace it whatever its mode says.
	RootOwner bool
	// NoSymlink refuses a wg-quick reached through a symbolic link, rather
	// than following it and checking what it points at.
	NoSymlink bool
}

// CheckExecutable refuses a binary that root must not be asked to run.
//
// wg-quick is executed as root, with a .conf as its argument. Whoever can write
// that file, or any directory on the way to it, chooses what root does at the
// next `sudo tun-manager up` — which is a longer reach than anything else in
// this program grants.
//
// What it refuses is what can be refused without refusing the documented
// installation:
//
//   - anything that is not there, or is not a regular file once the links are
//     followed;
//   - anything with no execute bit, which is a path that was mistyped or a
//     download that was never chmodded;
//   - a binary, or a directory on the way to it, that anybody at all can write.
//
// What it does not refuse is a binary owned by somebody other than root.
// Homebrew installs under the user who ran it — /opt/homebrew/bin/wg-quick is a
// symbolic link into ../Cellar, and both are that user's — so refusing it would
// refuse the installation the README documents. It is a real weakness and it
// cannot be closed here: a process running as that user can replace what root
// executes, whatever the mode says. `doctor` reports it, in the one place that
// can explain what it means and what to do about it.
//
// Symbolic links are followed rather than refused, for the same reason, and
// what they point at is checked as well: a link in a sound directory pointing
// into one anybody can write is exactly the case worth catching.
func CheckExecutable(path string, strict Strict) error {
	// EvalSymlinks resolves every link on the way, so what is checked below is
	// the file that will actually run rather than the name it was reached by.
	resolved, err := fsx.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("wg_quick %s cannot be read: %w", path, err)
	}
	if strict.NoSymlink && resolved != path {
		return fmt.Errorf(
			"wg_quick %s is reached through a symbolic link, and wg_quick_no_symlink is set: "+
				"point wg_quick at %s, or unset the rule", named(path, resolved), resolved)
	}

	info, err := fsx.Stat(resolved)
	if err != nil {
		// EvalSymlinks walked to this file a moment ago; something moved it in
		// between, and what root would run is no longer what was checked.
		return fmt.Errorf("wg_quick %s cannot be read: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("wg_quick %s is not a file", named(path, resolved))
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("wg_quick %s is %04o and not executable", named(path, resolved), info.Mode().Perm())
	}
	if info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf(
			"wg_quick %s is %04o, which anybody on this machine can write: it is run as root, "+
				"so whoever writes it chooses what root does. `sudo chmod o-w %s`",
			named(path, resolved), info.Mode().Perm(), resolved)
	}

	// Both names, because they can be different directories: a link in a sound
	// place pointing into one anybody can write is the case that reads as safe
	// and is not.
	for _, name := range []string{path, resolved} {
		if err := checkPathToExecutable(name, strict); err != nil {
			return err
		}
	}
	if strict.RootOwner {
		return checkOwnedByRoot(path, resolved)
	}
	return nil
}

// checkOwnedByRoot refuses anything on the way to the binary that somebody
// other than root could change.
//
// This is the rule the mode bits cannot express. A file belongs to whoever owns
// it: they can chmod it, replace it, or rename another one over it, and no
// permission on it says otherwise. Asking for it means asking that root be the
// only one who can.
func checkOwnedByRoot(path, resolved string) error {
	for _, start := range []string{path, resolved} {
		for name := start; ; name = fsx.Dir(name) {
			info, err := fsx.Lstat(name)
			if err != nil {
				return fmt.Errorf("wg_quick: %s cannot be read: %w", name, err)
			}
			if uid, _ := fsx.Owner(name, info); uid != fsx.Root {
				return fmt.Errorf(
					"wg_quick: %s is owned by uid %d rather than root, and wg_quick_root_owned is "+
						"set: that user can replace what root runs. `sudo chown 0:0 %s`, or move "+
						"wg-quick somewhere root owns",
					name, uid, name)
			}
			if info.Mode().Perm()&0o020 != 0 {
				return fmt.Errorf(
					"wg_quick: %s is %04o and its group can write it, and wg_quick_root_owned is "+
						"set: `sudo chmod g-w %s`", name, info.Mode().Perm(), name)
			}
			if name == fsx.Dir(name) {
				break
			}
		}
	}
	return nil
}

// checkPathToExecutable walks up from a binary, refusing any directory anybody
// can write.
//
// A directory that is world-writable *and* sticky is left alone: the sticky bit
// is what stops one user renaming another's file out of the way, which is the
// attack this is looking for. /tmp is 1777, and a stub kept there for a demo is
// not a finding.
func checkPathToExecutable(path string, strict Strict) error {
	for dir := fsx.Dir(path); ; dir = fsx.Dir(dir) {
		info, err := fsx.Stat(dir)
		if err != nil {
			return fmt.Errorf("wg_quick: %s cannot be read: %w", dir, err)
		}
		if info.Mode().Perm()&0o002 != 0 && (strict.RootOwner || info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf(
				"wg_quick: %s is %04o, which anybody on this machine can write, so what is in it "+
					"can be replaced by anybody: `sudo chmod o-w %s`",
				dir, info.Mode().Perm(), dir)
		}
		if dir == fsx.Dir(dir) {
			return nil
		}
	}
}

// named renders a path, saying what it resolved to when that is somewhere else.
// Reading "wg_quick /opt/homebrew/bin/wg-quick is not a file" without the ".. ->
// ../Cellar/.." is reading about the wrong file.
func named(path, resolved string) string {
	if path == resolved {
		return path
	}
	return path + " (" + resolved + ")"
}
