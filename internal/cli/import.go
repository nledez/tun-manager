package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wgconf"
)

// backupSuffix names the copy taken before the configuration is rewritten. It
// sits beside the original so it is found by whoever goes looking for it, and
// says what it is rather than what tool made it.
const backupSuffix = ".before-update"

// confMode is what an imported configuration is written as. It carries the
// tunnel's private key, and wg-quick reads it as root: nobody else needs it.
const confMode os.FileMode = 0o600

// shortName is what a tunnel may be called. The name becomes a file name and,
// through `<name>.name` in the WireGuard run directory, the identity that
// matches a config to a live interface — so anything that would surprise a
// path or a shell is refused rather than escaped.
var shortName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Import copies a WireGuard configuration into the directory tun-manager reads
// and lists the tunnel in the `all` group.
//
// Everything is checked before anything is written. An import that half
// succeeds leaves a tunnel that appears in one place and not the other, which
// is worse than one that did not happen.
func Import(w io.Writer, cfg *profile.Config, u privdrop.User, name, source string) error {
	if !shortName.MatchString(name) {
		return fmt.Errorf("%q is not a usable tunnel name: letters, digits, - and _, starting with a letter or a digit", name)
	}

	// Read before parsing rather than after: parsing it first would leave a
	// read that cannot fail, which is a branch no test can reach.
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}

	tun, err := wgconf.ParseFile(source)
	if err != nil {
		return err
	}
	if tun.CheckIP == "" || tun.CheckIPInferred {
		return fmt.Errorf(
			"%s has no `# TO_CHECK=<address>` comment: without one there is no address to ping, "+
				"and a tunnel that is up but carrying nothing looks the same as one that works",
			source,
		)
	}

	target := filepath.Join(cfg.ConfigDir, name+".conf")
	if _, statErr := os.Stat(target); statErr == nil {
		return fmt.Errorf("%s already exists: remove it first if you mean to replace that tunnel", target)
	}

	if err = os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return err
	}
	if err = os.WriteFile(target, body, confMode); err != nil {
		return err
	}

	// Taken here rather than earlier: rewriting the configuration is the only
	// step that can damage something the user wrote, and an import that failed
	// before this point should not leave a copy behind to explain.
	saved, err := backup(cfg.Path)
	if err != nil {
		return fmt.Errorf("%s was copied to %s, but %s could not be backed up: %w", name, target, cfg.Path, err)
	}

	if err = profile.AddToGroup(cfg.Path, profile.GroupAll, name); err != nil {
		// The copy stays: it is valid, and saying where it went is more use
		// than a rollback that hides half of what happened.
		return fmt.Errorf("%s was copied to %s, but %s could not be updated: %w", name, target, cfg.Path, err)
	}
	// Written by root, and it has to stay editable without root.
	_ = u.Chown(filepath.Dir(cfg.Path))
	_ = u.Chown(cfg.Path)
	if saved != "" {
		_ = u.Chown(saved)
	}

	report := fmt.Sprintf(
		"imported %s\n  config   %s\n  check    %s\n  group    %s in %s\n",
		name, target, tun.CheckIP, profile.GroupAll, cfg.Path,
	)
	if saved != "" {
		report += "  backup   " + saved + "\n"
	}
	_, err = io.WriteString(w, report)
	return err
}

// backup copies the configuration beside itself, and returns where it went.
//
// One copy, overwritten by the next import, so it always holds the file as it
// was before the most recent change - which is the one worth undoing. There is
// nothing to copy the first time, and that is not a failure.
func backup(path string) (string, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	dest := path + backupSuffix
	if err := os.WriteFile(dest, body, mode); err != nil {
		return "", err
	}
	return dest, nil
}
