package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
)

// A configuration with everything an import needs. The key is invented for this
// repository and the addresses come from the ranges reserved for documentation.
const importable = `[Interface]
PrivateKey = QK9kL2mXvR7tYuIoPaSdFgHjKlZxCvBnMqWeRtYuIo0=
Address = 10.20.30.2/32
# TO_CHECK=10.20.30.1

[Peer]
PublicKey = JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g=
Endpoint = 192.0.2.10:51820
AllowedIPs = 10.20.30.0/24
`

// importEnv returns a configuration pointing at empty temporary directories,
// plus the path of a source file holding body.
func importEnv(t *testing.T, body string) (*profile.Config, string) {
	t.Helper()

	cfg := profile.Default()
	cfg.ConfigDir = filepath.Join(t.TempDir(), "config")
	cfg.Path = filepath.Join(t.TempDir(), "tun-manager", "config.yaml")

	source := filepath.Join(t.TempDir(), "downloaded.conf")
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return cfg, source
}

func TestImportCopiesTheConfigurationAndListsTheTunnel(t *testing.T) {
	cfg, source := importEnv(t, importable)
	var out strings.Builder

	if err := Import(&out, cfg, privdrop.User{}, "alpha", source); err != nil {
		t.Fatalf("Import: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "alpha.conf"))
	if err != nil {
		t.Fatalf("read the copy: %v", err)
	}
	if string(got) != importable {
		t.Errorf("the copy differs from the source:\n%s", got)
	}

	written, err := profile.Load(cfg.Path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if members := written.Groups[profile.GroupAll]; len(members) != 1 || members[0] != "alpha" {
		t.Errorf("all = %v, want alpha", members)
	}
}

func TestImportKeepsTheKeyToItself(t *testing.T) {
	// The file carries a private key. It is copied, never shown.
	cfg, source := importEnv(t, importable)
	var out strings.Builder

	if err := Import(&out, cfg, privdrop.User{}, "alpha", source); err != nil {
		t.Fatalf("Import: %v", err)
	}

	if strings.Contains(out.String(), "QK9kL2mXvR7tYuIoPaSdFgHjKlZxCvBnMqWeRtYuIo0=") {
		t.Errorf("the private key reached the output:\n%s", out.String())
	}
	info, err := os.Stat(filepath.Join(cfg.ConfigDir, "alpha.conf"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != confMode {
		t.Errorf("mode = %o, want %o: the file holds a private key", got, confMode)
	}
}

func TestImportSaysWhatItDid(t *testing.T) {
	cfg, source := importEnv(t, importable)
	var out strings.Builder

	if err := Import(&out, cfg, privdrop.User{}, "alpha", source); err != nil {
		t.Fatalf("Import: %v", err)
	}

	got := out.String()
	for _, want := range []string{"alpha", filepath.Join(cfg.ConfigDir, "alpha.conf"), "10.20.30.1", cfg.Path} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not mention %q:\n%s", want, got)
		}
	}
}

func TestImportBacksUpTheConfigurationBeforeChangingIt(t *testing.T) {
	before := "# hand written\ngroups:\n  all:\n    - alpha\n"
	cfg, source := importEnv(t, importable)
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfg.Path, []byte(before), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out strings.Builder

	if err := Import(&out, cfg, privdrop.User{}, "bravo", source); err != nil {
		t.Fatalf("Import: %v", err)
	}

	saved, err := os.ReadFile(cfg.Path + backupSuffix)
	if err != nil {
		t.Fatalf("read the backup: %v", err)
	}
	if string(saved) != before {
		t.Errorf("backup = %q, want the file as it was", saved)
	}
	if !strings.Contains(out.String(), cfg.Path+backupSuffix) {
		t.Errorf("output does not say where the backup went:\n%s", out.String())
	}
}

func TestImportKeepsTheBackupsPermissions(t *testing.T) {
	cfg, source := importEnv(t, importable)
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfg.Path, []byte("groups:\n  all: []\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source); err != nil {
		t.Fatalf("Import: %v", err)
	}

	info, err := os.Stat(cfg.Path + backupSuffix)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600: a backup must not be readable by more people than its original", got)
	}
}

func TestImportHasNothingToBackUpOnAFirstRun(t *testing.T) {
	// No configuration file yet. That is the normal first import, not a
	// failure, and there is no empty .bak left to puzzle over.
	cfg, source := importEnv(t, importable)
	var out strings.Builder

	if err := Import(&out, cfg, privdrop.User{}, "alpha", source); err != nil {
		t.Fatalf("Import: %v", err)
	}

	if _, err := os.Stat(cfg.Path + backupSuffix); !os.IsNotExist(err) {
		t.Errorf("stat = %v, want no backup of a file that did not exist", err)
	}
	if strings.Contains(out.String(), "backup") {
		t.Errorf("output mentions a backup that was not made:\n%s", out.String())
	}
}

func TestImportStopsWhenTheBackupCannotBeWritten(t *testing.T) {
	// Rewriting the configuration without a copy of it is the one thing this
	// step exists to prevent.
	cfg, source := importEnv(t, importable)
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfg.Path, []byte("groups:\n  all: []\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A directory where the backup wants to be a file.
	if err := os.Mkdir(cfg.Path+backupSuffix, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source)

	if err == nil {
		t.Fatal("Import went ahead without a backup")
	}
	if !strings.Contains(err.Error(), "backed up") {
		t.Errorf("error = %v, want it to say the backup failed", err)
	}
	got, readErr := os.ReadFile(cfg.Path)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if strings.Contains(string(got), "alpha") {
		t.Errorf("the configuration was changed anyway:\n%s", got)
	}
}

func TestImportRefusesAConfigurationWithoutToCheck(t *testing.T) {
	// This is the whole point of the check: an address is what tells a tunnel
	// that is up and carrying nothing from one that works.
	body := strings.ReplaceAll(importable, "# TO_CHECK=10.20.30.1\n", "")
	// AllowedIPs holds a /24, which cannot be inferred as a host to ping.
	cfg, source := importEnv(t, body)

	err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source)

	if err == nil {
		t.Fatal("Import accepted a configuration with no TO_CHECK")
	}
	if !strings.Contains(err.Error(), "TO_CHECK") {
		t.Errorf("error = %v, want it to name what is missing", err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.ConfigDir, "alpha.conf")); !os.IsNotExist(statErr) {
		t.Error("the configuration was copied despite being refused")
	}
}

func TestImportRefusesAnInferredCheckAddress(t *testing.T) {
	// A single-host AllowedIPs lets the parser guess an address. A guess is not
	// what was asked for: the comment has to be there.
	body := strings.ReplaceAll(importable, "# TO_CHECK=10.20.30.1\n", "")
	body = strings.ReplaceAll(body, "AllowedIPs = 10.20.30.0/24", "AllowedIPs = 10.20.30.1/32")
	cfg, source := importEnv(t, body)

	err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source)

	if err == nil {
		t.Fatal("Import accepted a configuration whose check address was guessed")
	}
	if !strings.Contains(err.Error(), "TO_CHECK") {
		t.Errorf("error = %v, want it to name what is missing", err)
	}
}

func TestImportRefusesToReplaceAnExistingTunnel(t *testing.T) {
	// Quietly overwriting the configuration of a tunnel that works is the kind
	// of thing found out much later.
	cfg, source := importEnv(t, importable)
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := filepath.Join(cfg.ConfigDir, "alpha.conf")
	if err := os.WriteFile(existing, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source)

	if err == nil {
		t.Fatal("Import replaced an existing configuration")
	}
	if got, _ := os.ReadFile(existing); string(got) != "keep me\n" {
		t.Errorf("the existing configuration was overwritten: %q", got)
	}
}

func TestImportRefusesANameThatIsNotOne(t *testing.T) {
	cfg, source := importEnv(t, importable)

	for _, name := range []string{"", "../escape", "with space", "dotted.name", "-leading", "sub/dir"} {
		if err := Import(&strings.Builder{}, cfg, privdrop.User{}, name, source); err == nil {
			t.Errorf("Import accepted %q as a tunnel name", name)
		}
	}
}

func TestImportRefusesAFileThatIsNotAConfiguration(t *testing.T) {
	cfg, source := importEnv(t, "this is not a WireGuard configuration\n")

	if err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source); err == nil {
		t.Error("Import accepted a file with no [Peer] section")
	}
}

func TestImportReportsASourceThatIsNotThere(t *testing.T) {
	cfg, _ := importEnv(t, importable)

	err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", filepath.Join(t.TempDir(), "absent.conf"))

	if err == nil {
		t.Error("Import accepted a source file that does not exist")
	}
}

func TestImportReportsADirectoryItCannotWriteTo(t *testing.T) {
	cfg, source := importEnv(t, importable)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.ConfigDir = filepath.Join(blocker, "config")

	if err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source); err == nil {
		t.Error("Import wrote under a regular file")
	}
}

func TestImportReportsATargetItCannotWrite(t *testing.T) {
	// A symlink where the copy belongs, pointing at a directory that is not
	// there. Stat follows it and says nothing is in the way; the write does
	// not get that luxury.
	cfg, source := importEnv(t, importable)
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(cfg.ConfigDir, "alpha.conf")
	if err := os.Symlink(filepath.Join(cfg.ConfigDir, "nowhere", "alpha.conf"), target); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source); err == nil {
		t.Error("Import reported success without writing the configuration")
	}
}

func TestImportSaysWhereTheCopyWentWhenTheConfigurationIsNotYAML(t *testing.T) {
	// The backup works - the file is readable - and the rewrite is what fails.
	// Half an import is worse than none, so the half that happened is named.
	cfg, source := importEnv(t, importable)
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfg.Path, []byte("groups: [unclosed\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source)

	if err == nil {
		t.Fatal("Import reported success without updating the configuration")
	}
	target := filepath.Join(cfg.ConfigDir, "alpha.conf")
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error = %v, want it to say where the copy went", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("the copy was rolled back: %v", statErr)
	}
}

func TestImportSaysWhereTheCopyWentWhenTheConfigurationCannotBeUpdated(t *testing.T) {
	// Half an import is worse than none, so the half that happened is named.
	cfg, source := importEnv(t, importable)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.Path = filepath.Join(blocker, "config.yaml")

	err := Import(&strings.Builder{}, cfg, privdrop.User{}, "alpha", source)

	if err == nil {
		t.Fatal("Import reported success without updating the configuration")
	}
	target := filepath.Join(cfg.ConfigDir, "alpha.conf")
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error = %v, want it to say where the copy went", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("the copy was rolled back: %v", statErr)
	}
}

func TestImportReportsAWriteFailure(t *testing.T) {
	// `tun-manager import ... | head` closes the pipe early; the exit code must
	// not claim success. The work is done by then, which is why the report is
	// the last thing Import does.
	boom := errors.New("broken pipe")
	cfg, source := importEnv(t, importable)

	err := Import(failingWriter{err: boom}, cfg, privdrop.User{}, "alpha", source)

	if !errors.Is(err, boom) {
		t.Errorf("Import = %v, want %v", err, boom)
	}
}
