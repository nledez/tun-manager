package cli

import (
	"fmt"
	"io"

	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/fsx"
	"ledez.net/tun-manager/internal/profile"
)

// rotateBackupSuffix names the copy a rotation keeps. The old key is in it, and
// the old key is what every menu bar out there has pinned: somebody who rotated
// by mistake needs it back, and the only place it exists is that file.
const rotateBackupSuffix = ".before-rotate"

// keepCopy puts a copy of a file beside itself under a suffix, and returns
// where it went. Same mode: what is being copied here holds a key.
func keepCopy(path, suffix string) (string, error) {
	body, err := fsx.ReadFile(path)
	if err != nil {
		return "", err
	}

	dest := path + suffix
	f, err := fsx.CreateNoFollow("", dest, TunnelFileMode)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(body); err != nil {
		f.Close() //nolint:errcheck // the write is the failure being reported
		return "", fmt.Errorf("write %s: %w", dest, err)
	}
	if err := fsx.CloseFile(f); err != nil {
		return "", fmt.Errorf("write %s: %w", dest, err)
	}
	// O_CREATE leaves an existing file's mode alone, and this one may have been
	// written by an older rotation.
	if err := fsx.Chmod(dest, TunnelFileMode); err != nil {
		return "", fmt.Errorf("close %s to everybody else: %w", dest, err)
	}
	return dest, nil
}

// FeedKey prints the fingerprint of the key the feed signs with.
//
// It is what somebody compares against the menu bar application's About window
// when it says the publisher has changed. Only the fingerprint: it is derived
// from the public half, so it can be read out loud, pasted into an issue and
// left on a screen, which is the whole point of having one.
func FeedKey(w io.Writer, priv *profile.Privileged) error {
	seed := priv.FeedKey.Reveal()
	if seed == "" {
		return fmt.Errorf(
			"%s has no feed key: the menu bar application cannot tell this publisher from "+
				"another. `sudo tun-manager feed-key --rotate` writes one", priv.Path)
	}

	fingerprint, err := feed.FingerprintOfSeed(seed)
	if err != nil {
		return fmt.Errorf(
			"%s: %w. `sudo tun-manager feed-key --rotate` writes a new one", priv.Path, err)
	}
	_, err = fmt.Fprintf(w, "feed key  %s\n  in      %s\n", fingerprint, priv.Path)
	return err
}

// RotateFeedKey draws a new key, keeps the old file beside itself, and says
// what the consequence is.
//
// The consequence is the point. Every menu bar that has connected to this
// publisher pinned the old key and will refuse the new one until somebody says
// otherwise in the application — which is the behaviour that makes pinning
// worth anything, and is indistinguishable from an attack if it happens
// unannounced.
func RotateFeedKey(w io.Writer, priv *profile.Privileged, ask Confirm) error {
	was := "none"
	if seed := priv.FeedKey.Reveal(); seed != "" {
		if fingerprint, err := feed.FingerprintOfSeed(seed); err == nil {
			was = fingerprint
		} else {
			was = "unreadable"
		}
	}

	agreed, err := ask(fmt.Sprintf(
		"replace the feed key %s? every menu bar that pinned it will refuse this publisher "+
			"until you approve the new one", was))
	if err != nil {
		return err
	}
	if !agreed {
		return fmt.Errorf("the feed key was left alone: %s", was)
	}

	seed, err := generateSeed()
	if err != nil {
		return err
	}
	fingerprint, err := feed.FingerprintOfSeed(seed)
	if err != nil {
		// Only reachable if the generator and the reader disagree about what a
		// seed is. Writing one nobody can take a fingerprint of would leave a
		// publisher nobody can verify.
		return err
	}

	// Copied rather than moved: the rewrite below reads the file it is
	// rewriting, and a rotation that moved it out from under itself would find
	// nothing there. The copy also means the original is only ever replaced by
	// a complete new one.
	saved, err := keepCopy(priv.Path, rotateBackupSuffix)
	if err != nil {
		return err
	}
	if writeErr := profile.SetFeedKey(priv.Path, seed); writeErr != nil {
		return fmt.Errorf("%s: %w, and the previous configuration is at %s", priv.Path, writeErr, saved)
	}

	report := fmt.Sprintf("feed key  %s\n  was     %s\n  in      %s\n  previous %s\n",
		fingerprint, was, priv.Path, saved)
	report += "\nThe menu bar application has the old one pinned. It will say the publisher has\n" +
		"changed, and show both fingerprints; approve this one there after checking it\n" +
		"against the line above.\n"
	_, err = io.WriteString(w, report)
	return err
}
