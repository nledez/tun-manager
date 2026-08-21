package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"ledez.net/tun-manager/internal/fsx"
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
//
// The same value doctor checks for, from the same constant, so the command that
// writes the file and the command that judges it cannot drift apart.
const confMode = TunnelFileMode

// Import copies a WireGuard configuration into the directory tun-manager reads
// and lists the tunnel in the `all` group.
//
// Everything is checked before anything is written. An import that half
// succeeds leaves a tunnel that appears in one place and not the other, which
// is worse than one that did not happen.
//
// The file is shown and then agreed to, in that order. What is being added is
// something wg-quick will read as root, so the decision belongs to a person and
// not to the fact that they typed a command - and the moment to make it is
// while what they are agreeing to is still on the screen.
func Import(w io.Writer, ask Confirm, cfg *profile.Config, u privdrop.User, name, source string) error {
	// The same rule wgconf applies when it reads the directory, from the same
	// place: a name this refuses is a name that would be skipped on the next
	// load, and importing something that will not load is a way of saying yes
	// and meaning no.
	if !wgconf.ValidName(name) {
		return fmt.Errorf("%q is not a usable tunnel name: %s", name, wgconf.NameRule)
	}

	// Read once, and parse what was read. Reading it twice - once for the body
	// to write, once for the parser - would mean showing one file and importing
	// another, which is a difference nobody would notice.
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}

	tun, err := wgconf.Parse(body, source)
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
	if _, statErr := fsx.Stat(target); statErr == nil {
		return fmt.Errorf("%s already exists: remove it first if you mean to replace that tunnel", target)
	}

	// Shown before anything is written: what is being imported is a file
	// wg-quick will read as root, and the moment to look at it is now.
	if err = Review(w, source, body, tun); err != nil {
		return err
	}
	agreed, err := ask(fmt.Sprintf("import %s as %s?", filepath.Base(source), target))
	if err != nil {
		return err
	}
	if !agreed {
		// Not an error in the sense of something going wrong, but not a success
		// either: a script that expected a tunnel to be there afterwards has to
		// find out.
		return fmt.Errorf("nothing was imported: %s was not agreed to", source)
	}

	if err = fsx.MkdirAll(cfg.ConfigDir, WireGuardDirMode); err != nil {
		return err
	}
	// Set rather than left to MkdirAll, which does nothing to a directory that
	// already exists and is cut down by the umask when it does create one. This
	// is the command that puts a key in there, so it is the one that has to
	// make sure the place is fit to hold it.
	if err = fsx.Chmod(cfg.ConfigDir, WireGuardDirMode); err != nil {
		return err
	}
	// No chmod after this one, unlike the directory above: a umask can only
	// take bits away, so whatever this lands on is 0600 or stricter. The one
	// way it could be looser is an existing file keeping its own mode, and a
	// target that already exists was refused several lines ago.
	if err = os.WriteFile(target, body, confMode); err != nil {
		return err
	}

	// Taken here rather than earlier: rewriting the configuration is the only
	// step that can damage something the user wrote, and an import that failed
	// before this point should not leave a copy behind to explain.
	saved, err := backup(cfg.Path, u)
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
		"\nimported %s\n  config   %s\n  check    %s\n  group    %s in %s\n",
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
func backup(path string, u privdrop.User) (string, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	mode := os.FileMode(0o644)
	if info, statErr := fsx.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	// Through privdrop: this is root writing beside a file in somebody's home,
	// and that somebody can put a symbolic link where the copy is about to go.
	dest := path + backupSuffix
	if err := u.WriteFile(dest, body, mode); err != nil {
		return "", err
	}
	return dest, nil
}
