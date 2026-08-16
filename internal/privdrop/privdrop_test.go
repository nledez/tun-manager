package privdrop

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fakeLookup(users map[string]*user.User) func(string) (*user.User, error) {
	return func(name string) (*user.User, error) {
		u, ok := users[name]
		if !ok {
			return nil, user.UnknownUserError(name)
		}
		return u, nil
	}
}

func TestResolvePrefersSudoUserOverHome(t *testing.T) {
	// sudo with env_reset rewrites HOME to /var/root; the real user is in SUDO_USER.
	env := map[string]string{"SUDO_USER": "operator", "HOME": "/var/root"}
	lookup := fakeLookup(map[string]*user.User{
		"operator": {Username: "operator", Uid: "1000", Gid: "1000", HomeDir: "/home/operator"},
	})

	got, err := Resolve(mapEnv(env), lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Username != "operator" {
		t.Errorf("Username = %q, want %q", got.Username, "operator")
	}
	if got.HomeDir != "/home/operator" {
		t.Errorf("HomeDir = %q, want %q", got.HomeDir, "/home/operator")
	}
	if got.UID != 1000 || got.GID != 1000 {
		t.Errorf("UID/GID = %d/%d, want 1000/1000", got.UID, got.GID)
	}
	if !got.Demotable {
		t.Error("Demotable = false, want true when SUDO_USER resolves")
	}
}

func TestResolveFallsBackToHomeWithoutSudoUser(t *testing.T) {
	env := map[string]string{"HOME": "/home/operator"}

	got, err := Resolve(mapEnv(env), fakeLookup(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.HomeDir != "/home/operator" {
		t.Errorf("HomeDir = %q, want %q", got.HomeDir, "/home/operator")
	}
	if got.Demotable {
		t.Error("Demotable = true, want false without SUDO_USER: nothing to demote to")
	}
}

func TestResolveIgnoresSudoUserRoot(t *testing.T) {
	// `sudo -i` from a root shell leaves SUDO_USER=root; demoting to root is a no-op.
	env := map[string]string{"SUDO_USER": "root", "HOME": "/var/root"}
	lookup := fakeLookup(map[string]*user.User{
		"root": {Username: "root", Uid: "0", Gid: "0", HomeDir: "/var/root"},
	})

	got, err := Resolve(mapEnv(env), lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Demotable {
		t.Error("Demotable = true, want false when SUDO_USER is root")
	}
}

func TestResolveFailsOnUnknownSudoUser(t *testing.T) {
	env := map[string]string{"SUDO_USER": "ghost", "HOME": "/var/root"}

	if _, err := Resolve(mapEnv(env), fakeLookup(nil)); err == nil {
		t.Fatal("Resolve succeeded with an unresolvable SUDO_USER, want error")
	}
}

func TestConfigDirUsesXDGConfigHomeUnderTheRealUser(t *testing.T) {
	u := User{Username: "operator", HomeDir: "/home/operator"}

	got := u.ConfigDir("tun-manager")

	want := filepath.Join("/home/operator", ".config", "tun-manager")
	if got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}
}

func mapEnv(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestCommandRunsAsTheRealUserWhenDemotable(t *testing.T) {
	u := User{Username: "operator", UID: 1000, GID: 1000, HomeDir: "/home/operator", Demotable: true}

	cmd := u.CommandContext(context.Background(), "/usr/bin/osascript", "-e", "beep")

	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatal("no credential set, want the command demoted")
	}
	if got := cmd.SysProcAttr.Credential.Uid; got != 1000 {
		t.Errorf("uid = %d, want 1000", got)
	}
	if !slices.Contains(cmd.Env, "HOME=/home/operator") {
		t.Errorf("env = %v, want HOME pointing at the real home", cmd.Env)
	}
	if !slices.Contains(cmd.Env, "USER=operator") {
		t.Errorf("env = %v, want USER set", cmd.Env)
	}
}

func TestCommandStaysAsIsWithoutADemotableUser(t *testing.T) {
	// Not started through sudo: there is no other identity to drop to.
	u := User{HomeDir: "/home/operator"}

	cmd := u.CommandContext(context.Background(), "/bin/echo", "hi")

	if cmd.SysProcAttr != nil {
		t.Errorf("SysProcAttr = %+v, want none", cmd.SysProcAttr)
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "hi" {
		t.Errorf("Args = %v, want the arguments preserved", cmd.Args)
	}
}

func TestCommandIsRunnable(t *testing.T) {
	u := User{HomeDir: t.TempDir()}

	out, err := u.CommandContext(context.Background(), "/bin/echo", "hello").Output()
	if err != nil {
		t.Fatalf("Output: %v", err)
	}

	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("out = %q, want %q", out, "hello")
	}
}

func TestChownGivesTheFileBackToTheRealUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Chowning to the current identity is the one case a test can exercise
	// without root, and it still runs the real syscall.
	u := User{UID: os.Getuid(), GID: os.Getgid(), Demotable: true}

	if err := u.Chown(path); err != nil {
		t.Fatalf("Chown: %v", err)
	}
}

func TestChownIsANoOpWithoutADemotableUser(t *testing.T) {
	u := User{}

	if err := u.Chown("/nonexistent/path"); err != nil {
		t.Errorf("Chown = %v, want nil: there is nobody to give the file to", err)
	}
}

func TestCurrentReadsTheProcessEnvironment(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	t.Setenv("HOME", "/home/operator")

	got, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}

	if got.HomeDir != "/home/operator" {
		t.Errorf("HomeDir = %q, want %q", got.HomeDir, "/home/operator")
	}
}

func TestCommandStopsWithItsContext(t *testing.T) {
	// A notification that never returns must not leak a process for the life of
	// the TUI.
	u := User{HomeDir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := u.CommandContext(ctx, "/bin/sleep", "5").Run(); err == nil {
		t.Fatal("the command ran to completion with a cancelled context, want an error")
	}
}

func TestCommandContextStillDemotes(t *testing.T) {
	u := User{Username: "operator", UID: 1000, GID: 1000, HomeDir: "/home/operator", Demotable: true}

	cmd := u.CommandContext(context.Background(), "/usr/bin/osascript", "-e", "beep")

	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatal("no credential set, want the command demoted")
	}
}

func TestResolveRejectsANonNumericUID(t *testing.T) {
	// os/user hands back whatever the directory holds; a uid that is not a
	// number would otherwise be silently truncated to zero, which is root.
	env := map[string]string{"SUDO_USER": "operator", "HOME": "/var/root"}
	lookup := fakeLookup(map[string]*user.User{
		"operator": {Username: "operator", Uid: "not-a-number", Gid: "1000", HomeDir: "/home/operator"},
	})

	if _, err := Resolve(mapEnv(env), lookup); err == nil {
		t.Fatal("Resolve accepted a non-numeric uid, want an error")
	}
}

func TestResolveRejectsANonNumericGID(t *testing.T) {
	env := map[string]string{"SUDO_USER": "operator", "HOME": "/var/root"}
	lookup := fakeLookup(map[string]*user.User{
		"operator": {Username: "operator", Uid: "1000", Gid: "not-a-number", HomeDir: "/home/operator"},
	})

	if _, err := Resolve(mapEnv(env), lookup); err == nil {
		t.Fatal("Resolve accepted a non-numeric gid, want an error")
	}
}

func TestCacheDirSitsUnderTheRealUserHome(t *testing.T) {
	// Under sudo, HOME points at /var/root; anything the program writes for the
	// user to read has to land in their home, not root's.
	u := User{Username: "operator", HomeDir: "/home/operator"}

	got := u.CacheDir("tun-manager")

	want := filepath.Join("/home/operator", ".cache", "tun-manager")
	if got != want {
		t.Errorf("CacheDir = %q, want %q", got, want)
	}
}
