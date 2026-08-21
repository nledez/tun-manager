package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
)

func healthyEnv(t *testing.T) (*profile.Config, *profile.Privileged, privdrop.User) {
	t.Helper()

	confDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(confDir, "alpha.conf"), []byte("[Peer]\nPublicKey = k\n"), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	// t.TempDir hands back a directory the umask has had its way with; the
	// checks below are about the mode, so it is set rather than assumed.
	chmod(t, confDir, 0o700)

	binDir := t.TempDir()
	wgQuick := filepath.Join(binDir, "wg-quick")
	if err := os.WriteFile(wgQuick, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write wg-quick: %v", err)
	}

	cfg := profile.Default()
	cfg.ConfigDir = confDir
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")

	priv := profile.DefaultPrivileged()
	priv.WgQuick = wgQuick
	// Pinned to a temporary directory: leaving the default would make these
	// checks depend on whether WireGuard happens to be running on the machine.
	priv.RunDir = t.TempDir()
	// Same reasoning: the default socket lives under /var/run, and whether the
	// feed check passes must not depend on that directory happening to exist.
	priv.FeedSocket = filepath.Join(t.TempDir(), "f.sock")

	// A fixture is owned by whoever runs the suite, and the permission checks
	// want root on the WireGuard side and the pre-sudo user on the other. Making
	// that true on disk would mean running the suite as root, which would prove
	// it only on a machine nobody develops on.
	ownedPerPath(t, map[string]int{filepath.Dir(cfg.Path): 1000}, 0)

	return cfg, priv, privdrop.User{Username: "operator", HomeDir: "/home/operator", UID: 1000, Demotable: true}
}

func findCheck(checks []Check, substr string) (Check, bool) {
	for _, c := range checks {
		if strings.Contains(strings.ToLower(c.Name), strings.ToLower(substr)) {
			return c, true
		}
	}
	return Check{}, false
}

func TestDoctorPassesOnAHealthySetup(t *testing.T) {
	cfg, priv, u := healthyEnv(t)

	checks := Doctor(cfg, priv, u, 0, "test")

	for _, c := range checks {
		if c.Status == Fail {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		}
	}
	if !AllPassed(checks) {
		t.Error("AllPassed = false, want true")
	}
}

func TestDoctorFailsWhenNotRoot(t *testing.T) {
	cfg, priv, u := healthyEnv(t)

	checks := Doctor(cfg, priv, u, 501, "test")

	c, ok := findCheck(checks, "root")
	if !ok {
		t.Fatalf("no root check in %+v", checks)
	}
	if c.Status != Fail {
		t.Errorf("root check status = %v, want %v", c.Status, Fail)
	}
	if AllPassed(checks) {
		t.Error("AllPassed = true, want false")
	}
}

func TestDoctorFailsOnMissingWgQuick(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	priv.WgQuick = filepath.Join(t.TempDir(), "absent")

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "wg-quick")
	if !ok {
		t.Fatalf("no wg-quick check in %+v", checks)
	}
	if c.Status != Fail {
		t.Errorf("wg-quick check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorFailsOnAWgQuickThatIsNotExecutable(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	if err := os.Chmod(priv.WgQuick, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	checks := Doctor(cfg, priv, u, 0, "test")

	c, _ := findCheck(checks, "wg-quick")
	if c.Status != Fail {
		t.Errorf("wg-quick check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorFailsOnAConfigDirWithoutTunnels(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	cfg.ConfigDir = t.TempDir()

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "config dir")
	if !ok {
		t.Fatalf("no config dir check in %+v", checks)
	}
	if c.Status != Fail {
		t.Errorf("config dir check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorReportsWhichConfigFileIsInUse(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	cfg.IsDefault = true

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "configuration")
	if !ok {
		t.Fatalf("no configuration check in %+v", checks)
	}
	if c.Status != Warn {
		t.Errorf("configuration check status = %v, want %v when falling back to defaults", c.Status, Warn)
	}
	if !strings.Contains(c.Detail, cfg.Path) {
		t.Errorf("detail = %q, want it to name %q", c.Detail, cfg.Path)
	}
}

func TestWriteDoctorRendersEveryCheck(t *testing.T) {
	var out strings.Builder
	checks := []Check{
		{Name: "root", Status: Pass, Detail: "euid 0"},
		{Name: "wg-quick", Status: Fail, Detail: "not found"},
	}

	if err := WriteDoctor(&out, checks); err != nil {
		t.Fatalf("WriteDoctor: %v", err)
	}

	got := out.String()
	for _, want := range []string{"root", "wg-quick", "not found"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorReportsTheWireGuardRunDirectory(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	priv.RunDir = t.TempDir()

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "run dir")
	if !ok {
		t.Fatalf("no run dir check in %+v", checks)
	}
	if c.Status != Pass {
		t.Errorf("run dir check status = %v, want %v", c.Status, Pass)
	}
}

func TestDoctorWarnsWhenTheRunDirectoryIsUnreadable(t *testing.T) {
	// Without it, tunnels sharing a peer public key cannot be told apart.
	cfg, priv, u := healthyEnv(t)
	priv.RunDir = filepath.Join(t.TempDir(), "absent")

	checks := Doctor(cfg, priv, u, 0, "test")

	c, _ := findCheck(checks, "run dir")
	if c.Status != Warn {
		t.Errorf("run dir check status = %v, want %v", c.Status, Warn)
	}
	if !strings.Contains(c.Detail, "public key") {
		t.Errorf("detail = %q, want it to explain the fallback", c.Detail)
	}
}

func TestDoctorWarnsWhenNoGroupIsConfigured(t *testing.T) {
	// Without groups, `down --all` and the s/n/e keys have nothing to act on.
	cfg, priv, u := healthyEnv(t)
	cfg.Groups = map[string][]string{}
	cfg.Overrides = nil

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "groups")
	if !ok {
		t.Fatalf("no groups check in %+v", checks)
	}
	if c.Status != Warn {
		t.Errorf("groups check status = %v, want %v", c.Status, Warn)
	}
	if !AllPassed(checks) {
		t.Error("AllPassed = false, want true: an empty configuration is not a failure")
	}
}

func TestDoctorPassesWhenGroupsAreConfigured(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	cfg.Groups = map[string][]string{"needed": {"alpha"}}

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "groups")
	if !ok {
		t.Fatalf("no groups check in %+v", checks)
	}
	if c.Status != Pass {
		t.Errorf("groups check status = %v, want %v", c.Status, Pass)
	}
}

func TestStatusStringsAreStable(t *testing.T) {
	for status, want := range map[Status]string{Pass: "ok", Warn: "warn", Fail: "FAIL"} {
		if got := status.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", status, got, want)
		}
	}
}

func TestDoctorFailsOnAConfigDirThatIsNotThere(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	cfg.ConfigDir = filepath.Join(t.TempDir(), "absent")

	checks := Doctor(cfg, priv, u, 0, "test")

	c, _ := findCheck(checks, "config dir")
	if c.Status != Fail {
		t.Errorf("config dir check status = %v, want %v", c.Status, Fail)
	}
}

func TestADirectoryItCannotReadIsNotReportedAsAnEmptyOne(t *testing.T) {
	// config_dir is 0700 and owned by root, so running doctor without sudo
	// cannot look inside it. Saying "no *.conf" there tells the user their
	// tunnels have disappeared, when the truth is that this process cannot see
	// them - the difference between "nothing is configured" and "ask again with
	// sudo".
	//
	// A regular file where the directory belongs, rather than a chmod: the
	// suite is run under sudo often enough, and root can read a 0000 directory,
	// so a permission test would pass by not testing anything.
	cfg, priv, u := healthyEnv(t)
	notADirectory := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(notADirectory, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.ConfigDir = notADirectory

	checks := Doctor(cfg, priv, u, 0, "test")

	c, _ := findCheck(checks, "config dir")
	if c.Status != Fail {
		t.Fatalf("status = %v, want a failure", c.Status)
	}
	if strings.Contains(c.Detail, "no *.conf") {
		t.Errorf("detail = %q: a directory it could not read was reported as an empty one", c.Detail)
	}
	if !strings.Contains(c.Detail, notADirectory) {
		t.Errorf("detail = %q, want the path it could not read", c.Detail)
	}
}

func TestADirectoryThatHoldsNoTunnelSaysSo(t *testing.T) {
	// The other half of the pair above: this one really is empty, and must not
	// borrow the wording of a failure to look.
	cfg, priv, u := healthyEnv(t)
	cfg.ConfigDir = t.TempDir()

	checks := Doctor(cfg, priv, u, 0, "test")

	if c, _ := findCheck(checks, "config dir"); !strings.Contains(c.Detail, "no *.conf") {
		t.Errorf("detail = %q, want it to say the directory holds none", c.Detail)
	}
}

func TestADirectoryNamedLikeAPatternIsStillJustADirectory(t *testing.T) {
	// config_dir is user input. It used to reach filepath.Glob, where a bracket
	// was a malformed pattern rather than a name; it is now a path like any
	// other, and this pins that a name nobody expected does not take a
	// different route through the check.
	cfg, priv, u := healthyEnv(t)
	dir := filepath.Join(t.TempDir(), "wireguard[")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha.conf"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.ConfigDir = dir

	checks := Doctor(cfg, priv, u, 0, "test")

	if c, _ := findCheck(checks, "config dir"); c.Status != Pass {
		t.Errorf("status = %v (%s), want the tunnel found", c.Status, c.Detail)
	}
}

func TestADirectoryEndingInConfIsNotCountedAsATunnel(t *testing.T) {
	// The count is what "5 tunnel(s)" means, and a directory is not one.
	cfg, priv, u := healthyEnv(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "spare.conf"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg.ConfigDir = dir

	checks := Doctor(cfg, priv, u, 0, "test")

	if c, _ := findCheck(checks, "config dir"); c.Status != Fail {
		t.Errorf("status = %v (%s), want a directory not to count", c.Status, c.Detail)
	}
}

func TestDoctorReportsWhereTheFeedWouldBind(t *testing.T) {
	// It is the first thing to look at when the menu bar shows nothing.
	cfg, priv, u := healthyEnv(t)
	priv.Feed = true
	priv.FeedSocket = filepath.Join(t.TempDir(), "f.sock")

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "status feed")
	if !ok {
		t.Fatalf("no status feed check in %+v", checks)
	}
	if c.Status != Pass {
		t.Errorf("status = %v (%s), want Pass on a writable directory", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "operator") {
		t.Errorf("detail = %q, want who the socket is handed to", c.Detail)
	}
}

func TestDoctorSaysSoWhenTheFeedIsOff(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	priv.Feed = false

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "status feed")
	if !ok {
		t.Fatalf("no status feed check in %+v", checks)
	}
	if c.Status != Warn {
		t.Errorf("status = %v, want Warn: the program works, just without a feed", c.Status)
	}
}

func TestDoctorFailsWhenTheFeedHasNowhereToBind(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	priv.Feed = true
	priv.FeedSocket = filepath.Join(t.TempDir(), "no-such-dir", "f.sock")

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "status feed")
	if !ok {
		t.Fatalf("no status feed check in %+v", checks)
	}
	if c.Status != Fail {
		t.Errorf("status = %v (%s), want Fail", c.Status, c.Detail)
	}
}

func TestDoctorFailsWhenTheFeedSocketsDirectoryIsAFile(t *testing.T) {
	// filepath.Dir of the configured socket resolves to something that stat
	// succeeds on but that cannot hold a socket: a plain file, not a directory.
	cfg, priv, u := healthyEnv(t)
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	priv.Feed = true
	priv.FeedSocket = filepath.Join(notADir, "f.sock")

	checks := Doctor(cfg, priv, u, 0, "test")

	c, ok := findCheck(checks, "status feed")
	if !ok {
		t.Fatalf("no status feed check in %+v", checks)
	}
	if c.Status != Fail {
		t.Errorf("status = %v (%s), want Fail", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "not a directory") {
		t.Errorf("detail = %q, want it to say why", c.Detail)
	}
}

func TestNothingSimulatedReportsNoCheckAtAll(t *testing.T) {
	// A line saying "not simulated" on every ordinary run would be noise in the
	// place a reader looks for problems.
	if _, ok := Simulation("", false); ok {
		t.Error("Simulation reported a check for a run with no flags")
	}
}

func TestASimulatedWireGuardSaysWhereItIsReading(t *testing.T) {
	check, ok := Simulation("/tmp/tm-demo/wireguard", false)

	if !ok {
		t.Fatal("Simulation reported nothing for a simulated run")
	}
	if check.Status != Warn {
		t.Errorf("status = %v, want a warning: nothing is broken, nothing is real either", check.Status)
	}
	if !strings.Contains(check.Detail, "/tmp/tm-demo/wireguard") {
		t.Errorf("detail = %q, want the directory it is reading", check.Detail)
	}
}

func TestInventedRoundTripsSayThatNothingIsSent(t *testing.T) {
	check, ok := Simulation("", true)

	if !ok {
		t.Fatal("Simulation reported nothing for invented probes")
	}
	if !strings.Contains(check.Detail, "nothing is sent") {
		t.Errorf("detail = %q, want it to say no packets leave", check.Detail)
	}
}

func TestBothSimulationsAreReportedTogether(t *testing.T) {
	check, _ := Simulation("/tmp/tm-demo/wireguard", true)

	if !strings.Contains(check.Detail, "wireguard") || !strings.Contains(check.Detail, "invented") {
		t.Errorf("detail = %q, want both named", check.Detail)
	}
}

func TestRootIsNotRequiredOfARunThatReadsASimulator(t *testing.T) {
	// Nothing a simulated run touches is root-only, so asking for a password
	// would be asking for one to read /tmp - and a demo whose own diagnostic
	// exits non-zero is one nobody believes the rest of.
	cfg, priv, u := healthyEnv(t)

	checks := Doctor(cfg, priv, u, 501, "test", RootNotNeeded())

	root, ok := findCheck(checks, "root")
	if !ok {
		t.Fatal("no root check at all")
	}
	if root.Status != Pass {
		t.Errorf("status = %v, want it to pass: %s", root.Status, root.Detail)
	}
	if !strings.Contains(root.Detail, "simulator") {
		t.Errorf("detail = %q, want it to say why root is not needed", root.Detail)
	}
	if !AllPassed(checks) {
		t.Error("AllPassed = false: a simulated run must be able to come out clean")
	}
}

func TestRootIsStillRequiredOfAnOrdinaryRun(t *testing.T) {
	// The flag is the only thing that lifts it.
	cfg, priv, u := healthyEnv(t)

	checks := Doctor(cfg, priv, u, 501, "test")

	root, _ := findCheck(checks, "root")
	if root.Status != Fail {
		t.Errorf("status = %v, want a failure without the option", root.Status)
	}
}

// withFeedKey is a privileged configuration that has been through
// init-privileged: the only shape in which the file is complete.
func withFeedKey() *profile.Privileged {
	priv := profile.DefaultPrivileged()
	priv.FeedKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	return priv
}

func TestThePrivilegedFileIsReportedAsReadWhenItWas(t *testing.T) {
	check := PrivilegedFile("/private/wireguard/config/tun-manager.yaml", withFeedKey(), nil, 0)

	if check.Status != Pass {
		t.Errorf("status = %v, want Pass", check.Status)
	}
	if !strings.Contains(check.Detail, "/private/wireguard/config/tun-manager.yaml") {
		t.Errorf("detail %q does not name the file", check.Detail)
	}
}

func TestAPrivilegedFileAPlainUserCannotReadIsOnlyAWarning(t *testing.T) {
	// It is 0600 and root's. A plain user being unable to read it is the design
	// working, and a FAIL there would teach people to chmod it.
	check := PrivilegedFile("/private/wireguard/config/tun-manager.yaml", withFeedKey(), fs.ErrPermission, 501)

	if check.Status != Warn {
		t.Errorf("status = %v, want Warn", check.Status)
	}
	if !strings.Contains(check.Detail, "sudo tun-manager doctor") {
		t.Errorf("detail %q does not say how to check it", check.Detail)
	}
}

func TestAPrivilegedFileRootCannotReadIsAFailure(t *testing.T) {
	// Root can read anything it is allowed to read. If it cannot, the file is
	// missing, a symbolic link, or owned by somebody else - and the reason has
	// to reach the report rather than be softened into a warning.
	check := PrivilegedFile("/private/wireguard/config/tun-manager.yaml", withFeedKey(), errors.New("is a symbolic link"), 0)

	if check.Status != Fail {
		t.Errorf("status = %v, want Fail", check.Status)
	}
	if !strings.Contains(check.Detail, "symbolic link") {
		t.Errorf("detail %q loses the reason", check.Detail)
	}
}

func TestThePrivilegedCheckShowsTheFeedKeyFingerprint(t *testing.T) {
	// It is what somebody compares against the application's About window when
	// the menu bar says the publisher has changed. A fingerprint the publisher
	// side never prints is a comparison nobody can make.
	priv := profile.DefaultPrivileged()
	priv.FeedKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	want, err := feed.FingerprintOfSeed(priv.FeedKey.Reveal())
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}

	check := PrivilegedFile("/private/wireguard/config/tun-manager.yaml", priv, nil, 0)

	if check.Status != Pass {
		t.Errorf("status = %v, want Pass", check.Status)
	}
	if !strings.Contains(check.Detail, want) {
		t.Errorf("detail %q does not show the fingerprint %q", check.Detail, want)
	}
	if strings.Contains(check.Detail, priv.FeedKey.Reveal()) {
		t.Errorf("detail %q prints the private seed", check.Detail)
	}
}

func TestAPrivilegedFileWithoutAFeedKeyIsAWarning(t *testing.T) {
	// Everything works without one until the menu bar has to tell this
	// publisher from another. Saying so at doctor time is the cheap moment.
	priv := profile.DefaultPrivileged() // no feed key

	check := PrivilegedFile("/private/wireguard/config/tun-manager.yaml", priv, nil, 0)

	if check.Status != Warn {
		t.Errorf("status = %v, want Warn", check.Status)
	}
	if !strings.Contains(check.Detail, "init-privileged") {
		t.Errorf("detail %q does not say how to get one", check.Detail)
	}
}

func TestAPrivilegedFileWithAnUnusableFeedKeyIsAWarning(t *testing.T) {
	// A seed edited by hand, truncated by a copy and paste. The file itself is
	// sound, so the failure belongs on this line rather than at startup.
	priv := profile.DefaultPrivileged()
	priv.FeedKey = "not a key"

	check := PrivilegedFile("/private/wireguard/config/tun-manager.yaml", priv, nil, 0)

	if check.Status != Warn {
		t.Errorf("status = %v, want Warn", check.Status)
	}
	if strings.Contains(check.Detail, "not a key") {
		t.Errorf("detail %q prints what was in the file", check.Detail)
	}
}

func TestDoctorFailsOnAWgQuickAnybodyCanWrite(t *testing.T) {
	// The same rule the controller enforces, from the same code: root runs that
	// file, so whoever can write it chooses what root does.
	cfg, priv, u := healthyEnv(t)
	if err := os.Chmod(priv.WgQuick, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Fail {
		t.Errorf("status = %v, want %v: %s", c.Status, Fail, c.Detail)
	}
	if !strings.Contains(c.Detail, "0777") {
		t.Errorf("detail %q does not say what is wrong", c.Detail)
	}
}

func TestDoctorWarnsAboutAWgQuickRootDoesNotOwn(t *testing.T) {
	// Homebrew installs under the user who ran it, so this is the ordinary
	// state of a `brew install wireguard-tools` — and a real hole all the same:
	// a process running as that user can replace what root executes. It cannot
	// be refused without refusing the installation this program documents, so
	// it is said out loud, here, where there is room to say what it means.
	cfg, priv, u := healthyEnv(t)
	ownedPerPath(t, map[string]int{"wg-quick": 501}, 0)

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Warn {
		t.Errorf("status = %v, want %v: %s", c.Status, Warn, c.Detail)
	}
	for _, want := range []string{"uid 501", "root"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q does not contain %q", c.Detail, want)
		}
	}
}

func TestDoctorWarnsAboutADirectoryOnTheWayThatRootDoesNotOwn(t *testing.T) {
	// /opt/homebrew/bin is the usual one, and what it grants is the right to
	// replace the binary inside it whatever that binary's own mode says.
	cfg, priv, u := healthyEnv(t)
	ownedPerPath(t, map[string]int{filepath.Dir(priv.WgQuick): 501}, 0)

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Warn {
		t.Errorf("status = %v, want %v: %s", c.Status, Warn, c.Detail)
	}
	if !strings.Contains(c.Detail, filepath.Dir(priv.WgQuick)) {
		t.Errorf("detail %q does not name the directory", c.Detail)
	}
}

func TestDoctorPassesAWgQuickRootOwnsAllTheWay(t *testing.T) {
	cfg, priv, u := healthyEnv(t)
	ownedBy(t, 0)

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Pass {
		t.Errorf("status = %v, want %v: %s", c.Status, Pass, c.Detail)
	}
}

func TestDoctorWarnsAboutAWgQuickAGroupCanWrite(t *testing.T) {
	// /opt/homebrew/bin is 0775 and group staff, which on a Mac with a second
	// account means that account can replace what root runs. Root owns it in
	// this fixture, so only the group bit is left to catch it.
	cfg, priv, u := healthyEnv(t)
	ownedBy(t, 0)
	if err := os.Chmod(priv.WgQuick, 0o775); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Warn {
		t.Errorf("status = %v, want %v: %s", c.Status, Warn, c.Detail)
	}
	if !strings.Contains(c.Detail, "group") {
		t.Errorf("detail %q does not say what is wrong", c.Detail)
	}
}

func TestDoctorFailsOnAWgQuickRootDoesNotOwnWhenThatIsAskedFor(t *testing.T) {
	// The warning becomes a refusal for anybody who has put wg-quick somewhere
	// root owns and said so.
	cfg, priv, u := healthyEnv(t)
	priv.WgQuickRootOwned = true
	ownedPerPath(t, map[string]int{"wg-quick": 501}, 0)

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Fail {
		t.Errorf("status = %v, want %v: %s", c.Status, Fail, c.Detail)
	}
	if !strings.Contains(c.Detail, "wg_quick_root_owned") {
		t.Errorf("detail %q does not name the rule that refused it", c.Detail)
	}
}

func TestDoctorDoesNotWarnAboutWhatItAlreadyRefuses(t *testing.T) {
	// With the rule on and nothing wrong, there is nothing left to say about
	// ownership: repeating the warning would say the opposite of what the check
	// just found.
	cfg, priv, u := healthyEnv(t)
	priv.WgQuickRootOwned = true
	ownedBy(t, 0)

	c, _ := findCheck(Doctor(cfg, priv, u, 0, "test"), "wg-quick")

	if c.Status != Pass {
		t.Errorf("status = %v, want %v: %s", c.Status, Pass, c.Detail)
	}
}

// doctorConfig is a loaded-looking configuration: a real path, and the built-in
// values for everything else.
func doctorConfig(t *testing.T) *profile.Config {
	t.Helper()
	cfg := profile.Default()
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")
	return cfg
}

func TestDoctorSaysWhenARefreshIntervalWasRaised(t *testing.T) {
	// A setting that does not do what it says is worth a line, however small
	// the difference: somebody wrote that number for a reason, and finding out
	// here beats wondering why the table is slower than they asked for.
	cfg := doctorConfig(t)
	cfg.RefreshRaisedFrom = time.Nanosecond

	check := checkConfigFile(cfg)

	if check.Status != Warn {
		t.Errorf("status = %v, want a warning", check.Status)
	}
	for _, want := range []string{"refresh_interval", "1ns", profile.MinRefresh.String(), "root"} {
		if !strings.Contains(check.Detail, want) {
			t.Errorf("detail does not mention %q: %s", want, check.Detail)
		}
	}
}

func TestDoctorSaysNothingAboutARefreshIntervalItKept(t *testing.T) {
	check := checkConfigFile(doctorConfig(t))

	if check.Status != Pass {
		t.Errorf("status = %v, want a pass", check.Status)
	}
	if strings.Contains(check.Detail, "refresh_interval") {
		t.Errorf("detail talks about a setting nobody changed: %s", check.Detail)
	}
}
