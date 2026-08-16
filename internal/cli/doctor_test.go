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
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")

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
