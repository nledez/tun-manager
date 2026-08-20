package wg

import (
	"fmt"
	"os"
	"path/filepath"
)

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
func CheckExecutable(path string) error {
	// EvalSymlinks resolves every link on the way, so what is checked below is
	// the file that will actually run rather than the name it was reached by.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("wg_quick %s cannot be read: %w", path, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		// NOT TESTED: EvalSymlinks has just walked to this file, so it was
		// there a moment ago.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
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
		if err := checkPathToExecutable(name); err != nil {
			return err
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
func checkPathToExecutable(path string) error {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			// NOT TESTED: these are the directories the stat above walked
			// through to reach the file.
			// See docs/coverage-gaps.md, "filesystem races in the permission code".
			return fmt.Errorf("wg_quick: %s cannot be read: %w", dir, err)
		}
		if info.Mode().Perm()&0o002 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf(
				"wg_quick: %s is %04o, which anybody on this machine can write, so what is in it "+
					"can be replaced by anybody: `sudo chmod o-w %s`",
				dir, info.Mode().Perm(), dir)
		}
		if dir == filepath.Dir(dir) {
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
