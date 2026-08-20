package cli

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"ledez.net/tun-manager/internal/feed"
)

// privilegedTemplate is what init-privileged writes: the settings that decide
// what root does, with the reasoning for each one beside it.
//
// Carried in the binary rather than read from a file, so a freshly installed
// tun-manager can lay out its own configuration with nothing else on the
// machine. configs/tun-manager.example.yaml is a copy of this, kept so it can
// be read on the forge; a test fails when the two drift.
//
//go:embed privileged.yaml
var privilegedTemplate string

// emptyFeedKey is the line the generated key replaces. The template ships with
// it empty, because a key shipped in an example is a key everybody has.
const emptyFeedKey = `feed_key: ""`

// initBackupSuffix names the copy --force keeps. Replacing the file replaces
// the feed key, and the menu bar has pinned the old one: somebody who did that
// by accident needs it back.
const initBackupSuffix = ".before-init"

// InitPrivileged lays out the root-only side of the configuration: the
// directories, the file, and the key the feed signs with.
//
// Everything it creates is created for root alone. The directories are 0700 and
// the file is 0600, and an existing directory that is looser is tightened
// rather than accepted: an installation that predates this command, or a mkdir
// done by hand under the wrong umask, is exactly the case worth fixing.
func InitPrivileged(w io.Writer, path string, force bool) error {
	configDir := filepath.Dir(path)
	parent := filepath.Dir(configDir)

	for _, dir := range []string{parent, configDir} {
		if err := makePrivateDir(dir); err != nil {
			return err
		}
	}

	seed, err := feed.GenerateSeed(nil)
	if err != nil {
		// NOT TESTED: this reads crypto/rand, which does not fail on darwin -
		// the failing-reader case is covered where it can be injected, in
		// internal/feed.
		// See docs/coverage-gaps.md, "the feed key round trip".
		return err
	}
	fingerprint, err := feed.FingerprintOfSeed(seed)
	if err != nil {
		// NOT TESTED: the seed was produced by GenerateSeed one line above, so
		// it is the right length and is base64 by construction. Reaching this
		// means those two disagree, which no input can arrange.
		// See docs/coverage-gaps.md, "the feed key round trip".
		return err
	}

	saved, err := keepPrevious(path, force)
	if err != nil {
		return err
	}

	// O_EXCL rather than a prior stat: between a check and a create, somebody
	// who can write the directory can put a file there. O_NOFOLLOW for the same
	// reason a symbolic link is refused above - what is written here is a key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, TunnelFileMode)
	if err != nil {
		// NOT TESTED: the directory was made 0700 and owned by this process a
		// few lines above, and whatever was at the path was moved aside or
		// refused. Reaching this means something claimed the name in between,
		// which is the race this open is written to lose safely rather than one
		// a test can arrange.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		return fmt.Errorf("create %s: %w", path, err)
	}
	body := strings.Replace(privilegedTemplate, emptyFeedKey, `feed_key: "`+seed+`"`, 1)
	if _, writeErr := io.WriteString(f, body); writeErr != nil {
		// NOT TESTED: a write to a file this process has just created, on a
		// filesystem with room on it. Arranging a failure means filling the
		// disk or unmounting it underneath the descriptor.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		f.Close() //nolint:errcheck // the create is the failure being reported
		return fmt.Errorf("write %s: %w", path, writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		// NOT TESTED: same window as the write above, and closed to a test for
		// the same reason.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		return fmt.Errorf("write %s: %w", path, closeErr)
	}

	report := fmt.Sprintf("created %s\n  mode      %04o, root only\n  feed key  %s\n",
		path, TunnelFileMode.Perm(), fingerprint)
	if saved != "" {
		report += "  previous  " + saved + "\n"
	}
	report += "\nThe menu bar application pins that fingerprint the first time it connects.\n" +
		"`sudo tun-manager feed-key` prints it again.\n"
	_, err = io.WriteString(w, report)
	return err
}

// makePrivateDir creates a directory root alone can read, or tightens one that
// is already there.
//
// The mode is set rather than left to MkdirAll, which cuts it down by the umask
// when it creates and does nothing at all when the directory already exists.
func makePrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if mkErr := os.MkdirAll(dir, WireGuardDirMode); mkErr != nil {
			return fmt.Errorf("create %s: %w", dir, mkErr)
		}
	case err != nil:
		return fmt.Errorf("stat %s: %w", dir, err)
	case info.Mode()&os.ModeSymlink != 0:
		// Somebody who can write the parent can point this elsewhere, and root
		// would then write a file holding a key wherever it points.
		return fmt.Errorf(
			"%s is a symbolic link: tun-manager will not follow one to lay out the configuration "+
				"it reads as root. Replace it with a real directory owned by root", dir)
	case !info.IsDir():
		return fmt.Errorf("%s exists and is not a directory", dir)
	}

	if err := os.Chmod(dir, WireGuardDirMode); err != nil {
		// NOT TESTED: chmod on a directory this process has just created, or
		// already owns as root. Arranging a refusal needs a directory owned by
		// somebody else, which needs the suite to run as root to set up.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// keepPrevious moves an existing configuration aside, and returns where it
// went. It refuses instead when force is not set.
//
// Rename rather than copy: it moves the link itself when the path is a symbolic
// link, so what is at the path afterwards is nothing at all, and the O_EXCL
// create below lands on a name nobody else has claimed.
func keepPrevious(path string, force bool) (string, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if !force {
		return "", fmt.Errorf(
			"%s already exists: it holds the feed key the menu bar application has pinned, "+
				"so it is not replaced by accident. Pass --force to replace it, and the old one "+
				"is kept beside it as %s",
			path, filepath.Base(path)+initBackupSuffix)
	}

	saved := path + initBackupSuffix
	if err := os.Rename(path, saved); err != nil {
		// NOT TESTED: a rename within one directory, which this process owns as
		// root and has just been shown to contain the file.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		return "", fmt.Errorf("keep %s as %s: %w", path, saved, err)
	}
	return saved, nil
}
