package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ledez.net/tun-manager/internal/fsx"
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
	dir := filepath.Dir(cfg.ConfigDir)
	if err := checkArchiveDir(dir); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "tun-manager-"+now.Format(archiveStamp)+".tar.gz")
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

// checkArchiveDir refuses to put every private key on the machine somewhere
// root does not own outright.
//
// The archive itself is 0600 and root's, and that is not enough: a directory
// somebody else can write is a directory where they can wait for the archive
// and move it, and one they can replace with a symbolic link before it is
// written. The .conf files are held to the same rule before anything reads
// them; the file that holds all of them at once is not held to a looser one.
func checkArchiveDir(dir string) error {
	info, err := fsx.Lstat(dir)
	if err != nil {
		return fmt.Errorf("no archive written: %s cannot be read: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"no archive written: %s is a symbolic link, and tun-manager will not follow one to "+
				"write every private key on this machine into it", dir)
	}
	if uid, _ := fsx.Owner(dir, info); uid != fsx.Root {
		return fmt.Errorf(
			"no archive written: %s is owned by uid %d rather than root, and the archive holds "+
				"every private key on this machine: `sudo chown 0:0 %s`", dir, uid, dir)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf(
			"no archive written: %s is %04o and writable by others, and the archive holds every "+
				"private key on this machine: `sudo chmod go-w %s`", dir, info.Mode().Perm(), dir)
	}
	return nil
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
	// have one quietly overwrite the other, and a name somebody claimed while
	// this was gathering is a name this does not take. O_NOFOLLOW says the same
	// thing about a symbolic link, which O_EXCL already refuses - it is there so
	// that reading the line does not require knowing that.
	f, err := fsx.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, archiveMode)
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
		fsx.Remove(dest) //nolint:errcheck // the failure being reported is the one that matters
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
