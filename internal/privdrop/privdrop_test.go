package privdrop

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/fsx"
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

// chownCall is one recorded handover.
type chownCall struct {
	path     string
	uid, gid int
}

// recordingChown makes every handover succeed and remembers what it was asked.
// A real one can only ever be made to the identity the test process already
// has, which proves nothing about whether the right one was chosen — and asking
// for any other fails, unless the suite happens to run under sudo, in which
// case it succeeds instead. Neither outcome is a test.
func recordingChown(t *testing.T, calls *[]chownCall) {
	t.Helper()

	previous := fsx.Lchown
	fsx.Lchown = func(path string, uid, gid int) error {
		*calls = append(*calls, chownCall{path: path, uid: uid, gid: gid})
		return nil
	}
	t.Cleanup(func() { fsx.Lchown = previous })
}

// failingChown makes every handover fail with the error given.
func failingChown(t *testing.T, err error) {
	t.Helper()

	previous := fsx.Lchown
	fsx.Lchown = func(string, int, int) error { return err }
	t.Cleanup(func() { fsx.Lchown = previous })
}

func TestChownGivesTheFileBackToTheRealUser(t *testing.T) {
	// What is worth testing is which identity the file is handed to. The real
	// syscall cannot say: as an ordinary user it only succeeds for the
	// identity the test already has, so it would pass with the wrong ids
	// hard-coded, and under sudo it succeeds for any of them.
	var calls []chownCall
	recordingChown(t, &calls)
	u := User{UID: 501, GID: 20, Demotable: true}

	if err := u.Chown("/tmp/tun-manager.log"); err != nil {
		t.Fatalf("Chown: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want one", calls)
	}
	if calls[0] != (chownCall{path: "/tmp/tun-manager.log", uid: 501, gid: 20}) {
		t.Errorf("chown%+v, want the pre-sudo uid and gid", calls[0])
	}
}

func TestChownReportsWhatTheSystemSaid(t *testing.T) {
	// The caller decides whether a failed handover matters; Chown does not get
	// to swallow it.
	boom := errors.New("operation not permitted")
	failingChown(t, boom)
	u := User{UID: 501, GID: 20, Demotable: true}

	if err := u.Chown("/tmp/tun-manager.log"); !errors.Is(err, boom) {
		t.Errorf("Chown = %v, want %v", err, boom)
	}
}

func TestWithoutAnInjectedChownTheRealOneIsUsed(t *testing.T) {
	// All this proves is that the default resolves to the system call rather
	// than to nothing. The identity is the one the test process already has,
	// so it says nothing about which one would be chosen - that is pinned by
	// TestChownGivesTheFileBackToTheRealUser.
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	u := User{UID: os.Getuid(), GID: os.Getgid(), Demotable: true}

	if err := u.Chown(path); err != nil {
		t.Fatalf("Chown: %v", err)
	}
}

func TestChownIsANoOpWithoutADemotableUser(t *testing.T) {
	// Nobody to give the file to, so nothing is attempted: the recorder proves
	// the syscall was not merely tolerated but skipped.
	var calls []chownCall
	recordingChown(t, &calls)
	u := User{}

	if err := u.Chown("/nonexistent/path"); err != nil {
		t.Errorf("Chown = %v, want nil: there is nobody to give the file to", err)
	}
	if len(calls) != 0 {
		t.Errorf("calls = %+v, want none", calls)
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

// MARK: writing into somebody else's home, as root

func TestWriteFileWritesAndHandsOver(t *testing.T) {
	// The two halves of the same act: root creates the file, and the user it
	// belongs to owns it afterwards. A file left root-owned under ~/.cache is
	// one its owner can no longer replace.
	var calls []chownCall
	home := t.TempDir()
	u := User{UID: 501, GID: 20, Demotable: true, HomeDir: home}
	previous := fsx.FchownFile
	fsx.FchownFile = func(f *os.File, uid, gid int) error {
		calls = append(calls, chownCall{path: f.Name(), uid: uid, gid: gid})
		return nil
	}
	t.Cleanup(func() { fsx.FchownFile = previous })

	path := filepath.Join(home, "icon.png")
	if err := u.WriteFile(path, []byte("a picture"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil || string(body) != "a picture" {
		t.Errorf("read back %q, %v", body, err)
	}
	if len(calls) != 1 || calls[0].uid != 501 || calls[0].gid != 20 {
		t.Errorf("handed over as %+v, want the pre-sudo uid and gid", calls)
	}
}

func TestWriteFileRefusesALinkSomebodyPlanted(t *testing.T) {
	// The whole reason this does not go through os.WriteFile: between two runs,
	// the owner of that home can replace the name with a link to anywhere, and
	// root would write there.
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "not-theirs")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := filepath.Join(home, "icon.png")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	u := User{UID: 501, GID: 20, Demotable: true, HomeDir: home}

	// It may or may not report a failure: writing goes through a name of its
	// own and then renames over this one, which replaces the link rather than
	// following it. What must be true is on the next lines.
	_ = u.WriteFile(path, []byte("a picture"), 0o644)

	body, readErr := os.ReadFile(target)
	if readErr != nil || string(body) != "original" {
		t.Errorf("what the link pointed at was written through: %q, %v", body, readErr)
	}
}

func TestWriteFileReportsAHandoverItCannotMake(t *testing.T) {
	// Leaving a root-owned file in somebody's cache would look like it worked
	// and then stop them replacing it.
	home := t.TempDir()
	boom := errors.New("operation not permitted")
	previous := fsx.FchownFile
	fsx.FchownFile = func(*os.File, int, int) error { return boom }
	t.Cleanup(func() { fsx.FchownFile = previous })
	u := User{UID: 501, GID: 20, Demotable: true, HomeDir: home}

	if err := u.WriteFile(filepath.Join(home, "icon.png"), []byte("x"), 0o644); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the handover failure", err)
	}
}

func TestWriteFileWithNobodyToHandItToStillWrites(t *testing.T) {
	// A real root login rather than sudo: there is nobody to give the file to,
	// and the icon is still worth writing.
	home := t.TempDir()
	handed := 0
	previous := fsx.FchownFile
	fsx.FchownFile = func(*os.File, int, int) error { handed++; return nil }
	t.Cleanup(func() { fsx.FchownFile = previous })
	u := User{HomeDir: home}

	if err := u.WriteFile(filepath.Join(home, "icon.png"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if handed != 0 {
		t.Errorf("handed over %d time(s), want none", handed)
	}
}

func TestWriteFileReportsAWriteItCannotFinish(t *testing.T) {
	home := t.TempDir()
	boom := errors.New("no space left on device")
	previous := fsx.OpenFile
	fsx.OpenFile = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		f, err := previous(path, flag, mode)
		if err != nil {
			return nil, err
		}
		// Closed underneath, so the write that follows fails the way a full
		// disk does.
		_ = f.Close()
		return f, nil
	}
	t.Cleanup(func() { fsx.OpenFile = previous })
	u := User{HomeDir: home}

	err := u.WriteFile(filepath.Join(home, "icon.png"), []byte("x"), 0o644)

	if err == nil {
		t.Fatal("WriteFile reported success on a descriptor that was gone")
	}
	_ = boom
}

func TestMkdirAllMakesAndHandsOver(t *testing.T) {
	var calls []chownCall
	recordingChown(t, &calls)
	home := t.TempDir()
	u := User{UID: 501, GID: 20, Demotable: true, HomeDir: home}
	dir := filepath.Join(home, ".cache", "tun-manager")

	if err := u.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("stat = %v, %v", info, err)
	}
	if len(calls) != 1 || calls[0].path != dir {
		t.Errorf("handed over %+v, want the directory itself", calls)
	}
}

func TestMkdirAllRefusesALinkSomebodyPlanted(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, ".cache")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	u := User{UID: 501, GID: 20, Demotable: true, HomeDir: home}

	err := u.MkdirAll(filepath.Join(home, ".cache", "tun-manager"), 0o755)

	if err == nil {
		t.Fatal("MkdirAll made a directory through a link")
	}
	if _, statErr := os.Stat(filepath.Join(elsewhere, "tun-manager")); !os.IsNotExist(statErr) {
		t.Error("it was made on the other side of the link")
	}
}

func TestChownChangesTheLinkAndNotWhatItPointsAt(t *testing.T) {
	// The difference between os.Lchown and os.Chown, which is the difference
	// between handing back a file and handing somebody the file it points at.
	// A dangling link is what tells them apart without root: Lchown changes the
	// link itself and succeeds, Chown follows it, finds nothing and fails.
	dir := t.TempDir()
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(filepath.Join(dir, "nothing-here"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// The identity this process already has: what is being checked is which of
	// the two calls is made, not which uid it is given.
	u := User{UID: os.Getuid(), GID: os.Getgid(), Demotable: true}

	if err := u.Chown(link); err != nil {
		t.Errorf("Chown = %v, want the link itself to have been changed", err)
	}
}

func TestWritingWillNotWriteThroughAFileSomebodyLeftAtTheName(t *testing.T) {
	// This is root writing into a directory the user owns, and O_NOFOLLOW
	// covers a symbolic link there but not a hard one - there is nothing to
	// follow, the name simply is the file. On darwin a plain user can make a
	// hard link to a root-owned file they can reach. Left at the name this
	// writes to, root truncates that file, writes a picture into it, and then
	// hands it to the user: a root-owned file becomes theirs to fill in.
	home := t.TempDir()
	victim := filepath.Join(home, "victim")
	const precious = "something root owns\n"
	if err := os.WriteFile(victim, []byte(precious), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	path := filepath.Join(home, "icon.png")
	if err := os.Link(victim, path); err != nil {
		t.Fatalf("hard link: %v", err)
	}

	u := User{Username: "someone", UID: os.Getuid(), GID: os.Getgid(), HomeDir: home}
	if err := u.WriteFile(path, []byte("a picture"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "a picture" {
		t.Errorf("the write did not land where it was meant to: %q, %v", body, err)
	}

	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if string(body) != precious {
		t.Errorf("the file behind the hard link now holds %q: root wrote through it", body)
	}
}

func TestWriteFileReportsAFileItCannotClose(t *testing.T) {
	// The write is only really done once the close says so, and what is being
	// written here is put in place by a rename afterwards: reporting success
	// would move an incomplete file over the name.
	home := t.TempDir()
	previous := fsx.CloseFile
	fsx.CloseFile = func(*os.File) error { return errors.New("no space left on device") }
	t.Cleanup(func() { fsx.CloseFile = previous })

	u := User{Username: "someone", UID: os.Getuid(), GID: os.Getgid(), HomeDir: home}
	err := u.WriteFile(filepath.Join(home, "icon.png"), []byte("a picture"), 0o644)

	if err == nil {
		t.Error("WriteFile reported success on a file it could not close")
	}
	if _, statErr := os.Stat(filepath.Join(home, "icon.png")); statErr == nil {
		t.Error("an unfinished file was moved into place")
	}
}
