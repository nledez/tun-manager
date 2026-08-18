package cli

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/profile"
)

var backupTaken = time.Date(2026, 8, 18, 14, 23, 5, 0, time.UTC)

// backupEnv lays out a wireguard directory beside a configuration file, the way
// the program finds them, and returns the configuration.
func backupEnv(t *testing.T) *profile.Config {
	t.Helper()

	root := t.TempDir()
	cfg := profile.Default()
	cfg.ConfigDir = filepath.Join(root, "config")
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")

	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfg.Path, []byte("groups:\n  all:\n    - alpha\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	for _, name := range []string{"alpha", "bravo"} {
		body := "[Peer]\nPublicKey = " + name + "\n# TO_CHECK=10.20.30.1\n"
		if err := os.WriteFile(filepath.Join(cfg.ConfigDir, name+".conf"), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return cfg
}

// archived reads an archive back into a map of name to contents, and a map of
// name to mode.
func archived(t *testing.T, path string) (map[string]string, map[string]os.FileMode) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()

	bodies := map[string]string{}
	modes := map[string]os.FileMode{}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return bodies, modes
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", header.Name, err)
		}
		bodies[header.Name] = string(body)
		modes[header.Name] = os.FileMode(header.Mode).Perm()
	}
}

func TestBackupArchivesTheConfigurationAndEveryTunnel(t *testing.T) {
	cfg := backupEnv(t)

	dest, err := Backup(&strings.Builder{}, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	bodies, _ := archived(t, dest)
	for _, want := range []string{"config.yaml", "config/alpha.conf", "config/bravo.conf"} {
		if _, ok := bodies[want]; !ok {
			t.Errorf("%s is not in the archive: %v", want, bodies)
		}
	}
	if got := bodies["config/alpha.conf"]; !strings.Contains(got, "TO_CHECK") {
		t.Errorf("alpha.conf = %q, want its contents", got)
	}
}

func TestBackupLandsBesideTheConfigurationDirectory(t *testing.T) {
	// Beside it, not inside: a .conf glob must not start finding archives.
	cfg := backupEnv(t)

	dest, err := Backup(&strings.Builder{}, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	if got := filepath.Dir(dest); got != filepath.Dir(cfg.ConfigDir) {
		t.Errorf("archive went to %s, want %s", got, filepath.Dir(cfg.ConfigDir))
	}
	if got := filepath.Base(dest); got != "tun-manager-20260818-142305.tar.gz" {
		t.Errorf("name = %q, want the date and time in it", got)
	}
}

func TestBackupKeepsTheArchiveToRoot(t *testing.T) {
	// It holds every private key on the machine in one file.
	cfg := backupEnv(t)

	dest, err := Backup(&strings.Builder{}, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != archiveMode {
		t.Errorf("mode = %o, want %o", got, archiveMode)
	}
}

func TestBackupCarriesEachFilesModeIntoTheArchive(t *testing.T) {
	// So that restoring puts a 0600 .conf back as 0600, rather than as
	// whatever the umask of the day decides.
	cfg := backupEnv(t)

	dest, err := Backup(&strings.Builder{}, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	_, modes := archived(t, dest)
	if got := modes["config/alpha.conf"]; got != 0o600 {
		t.Errorf("alpha.conf mode = %o, want 600", got)
	}
	if got := modes["config.yaml"]; got != 0o644 {
		t.Errorf("config.yaml mode = %o, want 644", got)
	}
}

func TestBackupSaysWhatItArchived(t *testing.T) {
	cfg := backupEnv(t)
	var out strings.Builder

	dest, err := Backup(&out, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	got := out.String()
	for _, want := range []string{dest, "config.yaml", "config/alpha.conf", "config/bravo.conf"} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not mention %q:\n%s", want, got)
		}
	}
}

func TestBackupWorksWithNoConfigurationFile(t *testing.T) {
	// Nothing has been configured yet, but the tunnels are still worth keeping.
	cfg := backupEnv(t)
	if err := os.Remove(cfg.Path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	dest, err := Backup(&strings.Builder{}, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	bodies, _ := archived(t, dest)
	if _, ok := bodies["config.yaml"]; ok {
		t.Error("a configuration that does not exist reached the archive")
	}
	if len(bodies) != 2 {
		t.Errorf("archive holds %v, want the two tunnels", bodies)
	}
}

func TestBackupWorksWithNoTunnels(t *testing.T) {
	cfg := backupEnv(t)
	for _, name := range []string{"alpha", "bravo"} {
		if err := os.Remove(filepath.Join(cfg.ConfigDir, name+".conf")); err != nil {
			t.Fatalf("remove: %v", err)
		}
	}

	dest, err := Backup(&strings.Builder{}, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	bodies, _ := archived(t, dest)
	if len(bodies) != 1 {
		t.Errorf("archive holds %v, want the configuration alone", bodies)
	}
}

func TestBackupRefusesWhenThereIsNothingToKeep(t *testing.T) {
	// An empty archive is a backup that will be trusted and is worth nothing.
	cfg := backupEnv(t)
	if err := os.Remove(cfg.Path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.RemoveAll(cfg.ConfigDir); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := Backup(&strings.Builder{}, cfg, backupTaken); err == nil {
		t.Error("Backup wrote an archive of nothing")
	}
}

func TestBackupSkipsWhatIsNotAFile(t *testing.T) {
	cfg := backupEnv(t)
	if err := os.Mkdir(filepath.Join(cfg.ConfigDir, "notatunnel.conf"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dest, err := Backup(&strings.Builder{}, cfg, backupTaken)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	bodies, _ := archived(t, dest)
	if _, ok := bodies["config/notatunnel.conf"]; ok {
		t.Error("a directory was archived as a tunnel")
	}
}

func TestBackupRefusesToOverwriteAnArchive(t *testing.T) {
	// Two backups in the same second: the second must not replace the first
	// without saying so.
	cfg := backupEnv(t)
	if _, err := Backup(&strings.Builder{}, cfg, backupTaken); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	if _, err := Backup(&strings.Builder{}, cfg, backupTaken); err == nil {
		t.Error("the second backup overwrote the first")
	}
}

func TestBackupReportsADestinationItCannotWrite(t *testing.T) {
	cfg := backupEnv(t)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.ConfigDir = filepath.Join(blocker, "config")

	if _, err := Backup(&strings.Builder{}, cfg, backupTaken); err == nil {
		t.Error("Backup wrote an archive under a regular file")
	}
}

func TestBackupReportsADirectoryItCannotSearch(t *testing.T) {
	cfg := backupEnv(t)
	// An unbalanced bracket is the one thing a glob pattern rejects.
	cfg.ConfigDir = filepath.Join(t.TempDir(), "a[b")

	if _, err := Backup(&strings.Builder{}, cfg, backupTaken); err == nil {
		t.Error("Backup accepted a directory name it cannot glob")
	}
}

func TestBackupReportsAWriteFailure(t *testing.T) {
	// `tun-manager backup | head` closes the pipe early; the exit code must not
	// claim success.
	boom := errors.New("broken pipe")
	cfg := backupEnv(t)

	if _, err := Backup(failingWriter{err: boom}, cfg, backupTaken); !errors.Is(err, boom) {
		t.Errorf("Backup = %v, want %v", err, boom)
	}
}

func TestBackupLeavesNoArchiveBehindWhenItFails(t *testing.T) {
	// Half an archive is worse than none: it looks like a backup.
	cfg := backupEnv(t)
	vanishing := filepath.Join(cfg.ConfigDir, "charlie.conf")
	if err := os.WriteFile(vanishing, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	members, err := gather(cfg)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if err := os.Remove(vanishing); err != nil {
		t.Fatalf("remove: %v", err)
	}

	dest := filepath.Join(filepath.Dir(cfg.ConfigDir), "partial.tar.gz")
	if err := writeArchive(dest, members); err == nil {
		t.Fatal("writeArchive succeeded with a file that had gone away")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("stat = %v, want the half-written archive removed", err)
	}
}

// budgetWriter accepts a fixed number of bytes and then refuses, so a test can
// place a failure between the tar header and the body it belongs to.
type budgetWriter struct {
	left int
	err  error
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		return 0, w.err
	}
	w.left -= len(p)
	return len(p), nil
}

func TestBackupReportsAHeaderItCannotWrite(t *testing.T) {
	boom := errors.New("no room left on device")
	cfg := backupEnv(t)
	members, err := gather(cfg)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	if err := addAll(tar.NewWriter(&budgetWriter{err: boom}), members); !errors.Is(err, boom) {
		t.Errorf("addAll = %v, want %v", err, boom)
	}
}

func TestBackupReportsABodyItCannotWrite(t *testing.T) {
	// Enough room for the header and not for what follows it, which is the
	// shape a disk filling up actually takes.
	boom := errors.New("no room left on device")
	cfg := backupEnv(t)
	big := filepath.Join(cfg.ConfigDir, "large.conf")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 64<<10)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(big)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// One member, so the budget lands between its header and its body rather
	// than being eaten by whatever came before it.
	members := []member{{source: big, name: "config/large.conf", info: info}}

	err = addAll(tar.NewWriter(&budgetWriter{left: 8192, err: boom}), members)

	if !errors.Is(err, boom) {
		t.Errorf("addAll = %v, want %v", err, boom)
	}
}
