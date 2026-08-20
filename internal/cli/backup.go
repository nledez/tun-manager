package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ledez.net/tun-manager/internal/profile"
)

// archiveMode is what the archive is written as. It gathers every private key
// on the machine into one file, so it belongs to root and to nobody else — and
// unlike the configuration, it is never handed back to the pre-sudo user.
const archiveMode os.FileMode = 0o600

// archiveStamp names archives so that sorting them by name sorts them by age,
// which is how a directory of them gets read.
const archiveStamp = "20060102-150405"

// member is one file on its way into the archive, with the name it takes there.
type member struct {
	source string
	name   string
	info   os.FileInfo
}

// Backup writes a tar.gz of both configurations and every tunnel .conf beside
// the configuration directory, and reports where it went.
//
// The archive holds every private key on the machine: the tunnels' own, and the
// key the feed signs with. It is created 0600 and stays root's.
func Backup(w io.Writer, cfg *profile.Config, priv *profile.Privileged, now time.Time) (string, error) {
	members, err := gather(cfg, priv)
	if err != nil {
		return "", err
	}
	if len(members) == 0 {
		return "", fmt.Errorf("nothing to back up: no %s, and no *.conf in %s", cfg.Path, cfg.ConfigDir)
	}

	// Beside the configuration directory rather than inside it: a .conf glob
	// must not start finding archives.
	dest := filepath.Join(
		filepath.Dir(cfg.ConfigDir),
		"tun-manager-"+now.Format(archiveStamp)+".tar.gz",
	)
	if err := writeArchive(dest, members); err != nil {
		return "", err
	}

	var report strings.Builder
	fmt.Fprintf(&report, "backed up %d file(s) to %s\n", len(members), dest) //nolint:errcheck // a Builder does not fail
	for _, m := range members {
		report.WriteString("  " + m.name + "\n") //nolint:errcheck // likewise
	}
	// Said out loud, because the next thing somebody does with an archive is
	// copy it somewhere convenient.
	report.WriteString("\nIt holds the tunnels' private keys and the feed signing key. " + //nolint:errcheck // likewise
		"It is 0600 and root's; keep it that way.\n")
	if _, err := io.WriteString(w, report.String()); err != nil {
		return "", err
	}
	return dest, nil
}

// gather lists what goes in, in the order it goes in.
//
// The privileged file is named rather than globbed, and the glob beside it asks
// for *.conf: it sits in the same directory, and a glob that grew to *.yaml
// would archive it twice - two copies of a signing key, in a file nobody
// re-reads.
func gather(cfg *profile.Config, priv *profile.Privileged) ([]member, error) {
	var members []member

	if info, err := os.Stat(cfg.Path); err == nil && info.Mode().IsRegular() {
		members = append(members, member{cfg.Path, filepath.Base(cfg.Path), info})
	}
	if info, err := os.Stat(priv.Path); err == nil && info.Mode().IsRegular() {
		// Under config/, where it lives, so a restore puts it back beside the
		// .conf files rather than beside the user's own configuration.
		members = append(members, member{
			priv.Path,
			filepath.Join("config", filepath.Base(priv.Path)),
			info,
		})
	}

	confs, err := filepath.Glob(filepath.Join(cfg.ConfigDir, "*.conf"))
	if err != nil {
		return nil, fmt.Errorf("look for tunnels in %s: %w", cfg.ConfigDir, err)
	}
	for _, path := range confs {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			// A directory called something.conf, or one that went away between
			// the glob and here. Neither is a tunnel.
			continue
		}
		members = append(members, member{path, filepath.Join("config", filepath.Base(path)), info})
	}
	return members, nil
}

// writeArchive builds the archive, and removes it again if anything goes wrong:
// half an archive is worse than none, because it looks like a backup.
func writeArchive(dest string, members []member) error {
	// O_EXCL rather than a prior stat: two backups in the same second must not
	// have one quietly overwrite the other.
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, archiveMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	err = addAll(tw, members)
	// Closed in order, innermost first, and the first failure is the one
	// reported: a later one is usually the same problem seen again.
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		os.Remove(dest) //nolint:errcheck // the failure being reported is the one that matters
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}

func addAll(tw *tar.Writer, members []member) error {
	for _, m := range members {
		body, err := os.ReadFile(m.source)
		if err != nil {
			return err
		}
		header := &tar.Header{
			Name: m.name,
			// The mode travels so that a restore puts a 0600 .conf back as
			// 0600 rather than as whatever the umask of the day says.
			Mode:    int64(m.info.Mode().Perm()),
			Size:    int64(len(body)),
			ModTime: m.info.ModTime(),
			Format:  tar.FormatPAX,
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tw.Write(body); err != nil {
			return err
		}
	}
	return nil
}
