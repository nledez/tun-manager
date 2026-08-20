package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/fsx"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
)

// ownedBy makes every path look owned by one uid for the length of a test.
//
// A fixture is owned by whoever runs the suite. Making it root-owned would mean
// running the suite as root, and a suite that only proves itself under sudo
// proves nothing on anybody else's machine.
func ownedBy(t *testing.T, uid int) {
	t.Helper()
	ownedPerPath(t, nil, uid)
}

// ownedPerPath is the same, for a test that needs one path to differ. A path
// matches when it ends with the key, so a test names "alpha.conf" rather than
// the temporary directory it happens to be under.
func ownedPerPath(t *testing.T, bySuffix map[string]int, fallback int) {
	t.Helper()

	previous := fsx.Owner
	fsx.Owner = func(path string, _ os.FileInfo) (int, int) {
		for suffix, uid := range bySuffix {
			if strings.HasSuffix(path, suffix) {
				return uid, uid
			}
		}
		return fallback, fallback
	}
	t.Cleanup(func() { fsx.Owner = previous })
}

// chmod sets a mode and puts it back afterwards, so a directory made
// unwritable does not defeat the temporary directory's own cleanup.
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
}

// aLayout builds the directories the checks look at, in the modes they are
// meant to have.
func aLayout(t *testing.T) *profile.Config {
	t.Helper()

	root := t.TempDir()
	cfg := profile.Default()
	cfg.ConfigDir = filepath.Join(root, "wireguard", "config")
	if err := os.MkdirAll(cfg.ConfigDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// MkdirAll obeys the umask, so the modes are set explicitly afterwards.
	for _, dir := range []string{filepath.Dir(cfg.ConfigDir), cfg.ConfigDir} {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("chmod: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, "alpha.conf"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg.Path = filepath.Join(root, "home", ".config", "tun-manager", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chmod(t, filepath.Dir(cfg.Path), 0o755)
	return cfg
}

var operator = privdrop.User{Username: "operator", UID: 501, GID: 20, Demotable: true}

func check(t *testing.T, checks []Check, name string) Check {
	t.Helper()

	c, ok := findCheck(checks, name)
	if !ok {
		t.Fatalf("no %q check in %+v", name, checks)
	}
	return c
}

func TestALayoutInTheDocumentedModesPasses(t *testing.T) {
	ownedPerPath(t, map[string]int{"tun-manager": 501}, 0)
	cfg := aLayout(t)

	for _, c := range Permissions(cfg, operator) {
		if c.Status != Pass {
			t.Errorf("%s = %v: %s", c.Name, c.Status, c.Detail)
		}
	}
}

// MARK: the directory holding the .conf files

func TestAConfigDirectoryAnybodyCanReadIsReported(t *testing.T) {
	// The keys are safe either way - the files are 0600 - but the list is not:
	// how many tunnels there are, what they are called, and through the names
	// people give them, often who they reach.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, cfg.ConfigDir, 0o755)

	c := check(t, Permissions(cfg, operator), "config dir mode")

	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if !strings.Contains(c.Detail, "chmod 0700") {
		t.Errorf("detail = %q, want the command that fixes it", c.Detail)
	}
}

func TestAConfigDirectoryStricterThanAskedForPasses(t *testing.T) {
	// 0500 is somebody being careful, not somebody making a mistake, and a
	// diagnostic that tells them to loosen it is one people stop reading.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, cfg.ConfigDir, 0o500)

	if c := check(t, Permissions(cfg, operator), "config dir mode"); c.Status != Pass {
		t.Errorf("status = %v (%s), want stricter to be fine", c.Status, c.Detail)
	}
}

func TestAConfigDirectoryNotOwnedByRootIsReported(t *testing.T) {
	ownedBy(t, 501)
	cfg := aLayout(t)

	c := check(t, Permissions(cfg, operator), "config dir mode")

	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if !strings.Contains(c.Detail, "chown 0:0") {
		t.Errorf("detail = %q, want the command that fixes it", c.Detail)
	}
}

func TestAConfigDirectoryThatIsNotThereIsReported(t *testing.T) {
	ownedBy(t, 0)
	cfg := aLayout(t)
	cfg.ConfigDir = filepath.Join(cfg.ConfigDir, "absent")

	if c := check(t, Permissions(cfg, operator), "config dir mode"); c.Status != Fail {
		t.Errorf("status = %v, want a failure", c.Status)
	}
}

// MARK: the directory holding that one

func TestAParentAnybodyCanWriteIsReported(t *testing.T) {
	// Its mode leaks nothing while the directory inside is 0700. What it grants
	// is the right to rename that directory away and leave another in its
	// place, which wg-quick would then read.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, filepath.Dir(cfg.ConfigDir), 0o777)

	c := check(t, Permissions(cfg, operator), "config dir mode")

	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if !strings.Contains(c.Detail, "could be replaced") {
		t.Errorf("detail = %q, want it to say what the risk is", c.Detail)
	}
}

func TestAParentAnybodyCanReadIsNotReported(t *testing.T) {
	// config_dir may well be /etc/wireguard, and a command that tells people to
	// chmod 700 /etc is worse than anything it is guarding against.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, filepath.Dir(cfg.ConfigDir), 0o755)

	if c := check(t, Permissions(cfg, operator), "config dir mode"); c.Status != Pass {
		t.Errorf("status = %v (%s), want a readable parent to be fine", c.Status, c.Detail)
	}
}

func TestAParentNotOwnedByRootIsReported(t *testing.T) {
	ownedPerPath(t, map[string]int{"wireguard": 501}, 0)
	cfg := aLayout(t)

	c := check(t, Permissions(cfg, operator), "config dir mode")

	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if !strings.Contains(c.Detail, "wireguard") {
		t.Errorf("detail = %q, want it to name the parent", c.Detail)
	}
}

// MARK: the .conf files

func TestATunnelFileAnybodyCanReadIsReported(t *testing.T) {
	// This one holds a private key.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, filepath.Join(cfg.ConfigDir, "alpha.conf"), 0o644)

	c := check(t, Permissions(cfg, operator), "tunnel files")

	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if !strings.Contains(c.Detail, "alpha.conf") || !strings.Contains(c.Detail, "chmod 0600") {
		t.Errorf("detail = %q, want the file and the command that fixes it", c.Detail)
	}
}

func TestEveryOffendingTunnelFileIsNamedRatherThanTheFirst(t *testing.T) {
	// Fixing one and running it again to find the next is how somebody stops
	// after the first.
	ownedBy(t, 0)
	cfg := aLayout(t)
	for _, name := range []string{"alpha.conf", "bravo.conf"} {
		if err := os.WriteFile(filepath.Join(cfg.ConfigDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		chmod(t, filepath.Join(cfg.ConfigDir, name), 0o644)
	}

	c := check(t, Permissions(cfg, operator), "tunnel files")

	if !strings.Contains(c.Detail, "alpha.conf") || !strings.Contains(c.Detail, "bravo.conf") {
		t.Errorf("detail = %q, want both named", c.Detail)
	}
}

func TestATunnelFileNotOwnedByRootIsReported(t *testing.T) {
	ownedPerPath(t, map[string]int{"alpha.conf": 501}, 0)
	cfg := aLayout(t)

	c := check(t, Permissions(cfg, operator), "tunnel files")

	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if !strings.Contains(c.Detail, "chown 0:0") {
		t.Errorf("detail = %q, want the command that fixes it", c.Detail)
	}
}

func TestADirectoryEndingInConfIsNotCheckedAsATunnelFile(t *testing.T) {
	ownedBy(t, 0)
	cfg := aLayout(t)
	if err := os.MkdirAll(filepath.Join(cfg.ConfigDir, "spare.conf"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if c := check(t, Permissions(cfg, operator), "tunnel files"); c.Status != Pass {
		t.Errorf("status = %v (%s), want a directory skipped", c.Status, c.Detail)
	}
}

func TestNoTunnelFilesAtAllIsSaidRatherThanPassedOver(t *testing.T) {
	// Nothing is wrong, and nothing was checked either. A green line would
	// claim the second.
	ownedBy(t, 0)
	cfg := aLayout(t)
	if err := os.Remove(filepath.Join(cfg.ConfigDir, "alpha.conf")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if c := check(t, Permissions(cfg, operator), "tunnel files"); c.Status != Warn {
		t.Errorf("status = %v (%s), want a warning", c.Status, c.Detail)
	}
}

func TestTunnelFilesInADirectoryItCannotReadAreReported(t *testing.T) {
	ownedBy(t, 0)
	cfg := aLayout(t)
	cfg.ConfigDir = filepath.Join(cfg.ConfigDir, "absent")

	if c := check(t, Permissions(cfg, operator), "tunnel files"); c.Status != Fail {
		t.Errorf("status = %v, want a failure", c.Status)
	}
}

// MARK: the directory holding config.yaml

func TestAUserConfigDirectoryLeftOwnedByRootIsReported(t *testing.T) {
	// What happens when something creates it during a sudo run without handing
	// it back: a file its owner can no longer edit.
	ownedBy(t, 0)
	cfg := aLayout(t)

	c := check(t, Permissions(cfg, operator), "user config dir")

	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if !strings.Contains(c.Detail, "chown -R 501:20") {
		t.Errorf("detail = %q, want the command that hands it back", c.Detail)
	}
}

func TestAUserConfigDirectoryAnybodyCanWriteIsReported(t *testing.T) {
	ownedBy(t, 501)
	cfg := aLayout(t)
	chmod(t, filepath.Dir(cfg.Path), 0o777)

	if c := check(t, Permissions(cfg, operator), "user config dir"); c.Status != Fail {
		t.Errorf("status = %v, want a failure", c.Status)
	}
}

func TestAUserConfigDirectoryStricterThanAskedForPasses(t *testing.T) {
	// 0700 is more private than the 0755 it is meant to have, and nothing in it
	// wants sharing.
	ownedBy(t, 501)
	cfg := aLayout(t)
	chmod(t, filepath.Dir(cfg.Path), 0o700)

	if c := check(t, Permissions(cfg, operator), "user config dir"); c.Status != Pass {
		t.Errorf("status = %v (%s), want stricter to be fine", c.Status, c.Detail)
	}
}

func TestWithoutASudoUserTheOwnerOfTheUserConfigIsNotJudged(t *testing.T) {
	// There is nobody to compare against, and root owning its own files is not
	// news.
	ownedBy(t, 0)
	cfg := aLayout(t)

	c := check(t, Permissions(cfg, privdrop.User{HomeDir: "/var/root"}), "user config dir")

	if c.Status != Pass {
		t.Errorf("status = %v (%s), want no judgement without a pre-sudo user", c.Status, c.Detail)
	}
}

func TestAUserConfigDirectoryThatIsNotThereIsOnlyAWarning(t *testing.T) {
	// A first run, before anything has written one. The configuration check
	// says so already; a second failure over the same fact is noise.
	ownedBy(t, 501)
	cfg := aLayout(t)
	cfg.Path = filepath.Join(cfg.Path, "deeper", "config.yaml")

	if c := check(t, Permissions(cfg, operator), "user config dir"); c.Status != Warn {
		t.Errorf("status = %v, want a warning", c.Status)
	}
}

// MARK: the same rules, as a refusal rather than a report

func TestEnforcePermissionsAcceptsTheDocumentedLayout(t *testing.T) {
	ownedBy(t, 0)
	cfg := aLayout(t)

	if err := EnforcePermissions(cfg.ConfigDir); err != nil {
		t.Errorf("EnforcePermissions on a sound layout: %v", err)
	}
}

func TestEnforcePermissionsRefusesADirectoryAnybodyCanRead(t *testing.T) {
	// doctor has reported this since the day config_dir became 0700. Reporting
	// it is not enough: nothing makes anybody run doctor, and the tunnel list
	// goes on leaking until somebody does.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, cfg.ConfigDir, 0o755)

	err := EnforcePermissions(cfg.ConfigDir)

	if err == nil {
		t.Fatal("EnforcePermissions accepted a directory anybody can read")
	}
	for _, want := range []string{cfg.ConfigDir, "0755", "sudo chmod 0700"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not contain %q", err, want)
		}
	}
}

func TestEnforcePermissionsRefusesATunnelAnybodyCanRead(t *testing.T) {
	// This one is the key itself, not the list.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, filepath.Join(cfg.ConfigDir, "alpha.conf"), 0o644)

	err := EnforcePermissions(cfg.ConfigDir)

	if err == nil {
		t.Fatal("EnforcePermissions accepted a .conf anybody can read")
	}
	for _, want := range []string{"alpha.conf", "0644", "sudo chmod 0600"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not contain %q", err, want)
		}
	}
}

func TestEnforcePermissionsRefusesADirectoryOwnedByAnybodyElse(t *testing.T) {
	ownedBy(t, 501)
	cfg := aLayout(t)

	err := EnforcePermissions(cfg.ConfigDir)

	if err == nil {
		t.Fatal("EnforcePermissions accepted a directory root does not own")
	}
	if !strings.Contains(err.Error(), "sudo chown 0:0") {
		t.Errorf("refusal %q does not say how to fix it", err)
	}
}

func TestEnforcePermissionsRefusesAParentAnybodyCanWrite(t *testing.T) {
	// The mode of the directory inside leaks nothing while it is 0700. What a
	// writable parent grants is the right to rename it away and leave another
	// in its place, which wg-quick would then read.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, filepath.Dir(cfg.ConfigDir), 0o777)

	if err := EnforcePermissions(cfg.ConfigDir); err == nil {
		t.Fatal("EnforcePermissions accepted a parent anybody can write")
	}
}

func TestEnforcePermissionsSaysNothingAboutADirectoryThatIsNotThereYet(t *testing.T) {
	// A machine before its first import. There are no keys to leak, and the
	// commands that need tunnels fail on their own with something specific.
	ownedBy(t, 0)

	if err := EnforcePermissions(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Errorf("EnforcePermissions on a directory that does not exist: %v", err)
	}
}

func TestEnforcePermissionsAcceptsADirectoryWithNoTunnelsInIt(t *testing.T) {
	// The report warns about it, because a run with nothing to manage is worth
	// mentioning. It is not a reason to refuse to start.
	ownedBy(t, 0)
	cfg := aLayout(t)
	if err := os.Remove(filepath.Join(cfg.ConfigDir, "alpha.conf")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if err := EnforcePermissions(cfg.ConfigDir); err != nil {
		t.Errorf("EnforcePermissions on an empty directory: %v", err)
	}
}

func TestEnforcePermissionsAndDoctorReadTheSameRules(t *testing.T) {
	// Two implementations of one rule is how one command starts refusing what
	// the other reports as fine.
	ownedBy(t, 0)
	cfg := aLayout(t)
	chmod(t, filepath.Join(cfg.ConfigDir, "alpha.conf"), 0o640)

	err := EnforcePermissions(cfg.ConfigDir)
	reported := check(t, Permissions(cfg, operator), "tunnel files")

	if err == nil {
		t.Fatal("EnforcePermissions accepted what doctor calls a failure")
	}
	if err.Error() != reported.Detail {
		t.Errorf("the refusal says %q, doctor says %q", err, reported.Detail)
	}
}
