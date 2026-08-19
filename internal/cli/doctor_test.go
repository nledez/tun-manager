package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
)

func healthyEnv(t *testing.T) (*profile.Config, privdrop.User) {
	t.Helper()

	confDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(confDir, "alpha.conf"), []byte("[Peer]\nPublicKey = k\n"), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	binDir := t.TempDir()
	wgQuick := filepath.Join(binDir, "wg-quick")
	if err := os.WriteFile(wgQuick, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write wg-quick: %v", err)
	}

	cfg := profile.Default()
	cfg.ConfigDir = confDir
	cfg.WgQuick = wgQuick
	// Pinned to a temporary directory: leaving the default would make these
	// checks depend on whether WireGuard happens to be running on the machine.
	cfg.RunDir = t.TempDir()
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")
	// Same reasoning: the default socket lives under /var/run, and whether the
	// feed check passes must not depend on that directory happening to exist.
	cfg.FeedSocket = filepath.Join(t.TempDir(), "f.sock")

	return cfg, privdrop.User{Username: "operator", HomeDir: "/home/operator", UID: 1000, Demotable: true}
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
	cfg, u := healthyEnv(t)

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)

	checks := Doctor(cfg, u, 501, "test")

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
	cfg, u := healthyEnv(t)
	cfg.WgQuick = filepath.Join(t.TempDir(), "absent")

	checks := Doctor(cfg, u, 0, "test")

	c, ok := findCheck(checks, "wg-quick")
	if !ok {
		t.Fatalf("no wg-quick check in %+v", checks)
	}
	if c.Status != Fail {
		t.Errorf("wg-quick check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorFailsOnAWgQuickThatIsNotExecutable(t *testing.T) {
	cfg, u := healthyEnv(t)
	if err := os.Chmod(cfg.WgQuick, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	checks := Doctor(cfg, u, 0, "test")

	c, _ := findCheck(checks, "wg-quick")
	if c.Status != Fail {
		t.Errorf("wg-quick check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorFailsOnAConfigDirWithoutTunnels(t *testing.T) {
	cfg, u := healthyEnv(t)
	cfg.ConfigDir = t.TempDir()

	checks := Doctor(cfg, u, 0, "test")

	c, ok := findCheck(checks, "config dir")
	if !ok {
		t.Fatalf("no config dir check in %+v", checks)
	}
	if c.Status != Fail {
		t.Errorf("config dir check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorWarnsWhenNotificationsCannotReachTheSession(t *testing.T) {
	cfg, u := healthyEnv(t)
	u.Demotable = false

	checks := Doctor(cfg, u, 0, "test")

	c, ok := findCheck(checks, "notification")
	if !ok {
		t.Fatalf("no notification check in %+v", checks)
	}
	if c.Status != Warn {
		t.Errorf("notification check status = %v, want %v", c.Status, Warn)
	}
	if !AllPassed(checks) {
		t.Error("AllPassed = false, want true: a warning is not a failure")
	}
}

func TestDoctorReportsWhichConfigFileIsInUse(t *testing.T) {
	cfg, u := healthyEnv(t)
	cfg.IsDefault = true

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)
	cfg.RunDir = t.TempDir()

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)
	cfg.RunDir = filepath.Join(t.TempDir(), "absent")

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)
	cfg.Groups = map[string][]string{}
	cfg.Overrides = nil

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)
	cfg.Groups = map[string][]string{"needed": {"alpha"}}

	checks := Doctor(cfg, u, 0, "test")

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

func TestDoctorFailsOnAnUnreadableConfigDir(t *testing.T) {
	cfg, u := healthyEnv(t)
	cfg.ConfigDir = filepath.Join(t.TempDir(), "absent")

	checks := Doctor(cfg, u, 0, "test")

	c, _ := findCheck(checks, "config dir")
	if c.Status != Fail {
		t.Errorf("config dir check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorFailsOnAConfigDirThatBreaksThePattern(t *testing.T) {
	// config_dir is user input; a bracket in it reaches filepath.Glob as a
	// malformed pattern rather than as a directory that does not exist.
	cfg, u := healthyEnv(t)
	cfg.ConfigDir = filepath.Join(t.TempDir(), "wireguard[")

	checks := Doctor(cfg, u, 0, "test")

	c, _ := findCheck(checks, "config dir")
	if c.Status != Fail {
		t.Errorf("config dir check status = %v, want %v", c.Status, Fail)
	}
}

func TestDoctorReportsWhereTheFeedWouldBind(t *testing.T) {
	// It is the first thing to look at when the menu bar shows nothing.
	cfg, u := healthyEnv(t)
	cfg.Feed = true
	cfg.FeedSocket = filepath.Join(t.TempDir(), "f.sock")

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)
	cfg.Feed = false

	checks := Doctor(cfg, u, 0, "test")

	c, ok := findCheck(checks, "status feed")
	if !ok {
		t.Fatalf("no status feed check in %+v", checks)
	}
	if c.Status != Warn {
		t.Errorf("status = %v, want Warn: the program works, just without a feed", c.Status)
	}
}

func TestDoctorFailsWhenTheFeedHasNowhereToBind(t *testing.T) {
	cfg, u := healthyEnv(t)
	cfg.Feed = true
	cfg.FeedSocket = filepath.Join(t.TempDir(), "no-such-dir", "f.sock")

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.Feed = true
	cfg.FeedSocket = filepath.Join(notADir, "f.sock")

	checks := Doctor(cfg, u, 0, "test")

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
	cfg, u := healthyEnv(t)

	checks := Doctor(cfg, u, 501, "test", RootNotNeeded())

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
	cfg, u := healthyEnv(t)

	checks := Doctor(cfg, u, 501, "test")

	root, _ := findCheck(checks, "root")
	if root.Status != Fail {
		t.Errorf("status = %v, want a failure without the option", root.Status)
	}
}
