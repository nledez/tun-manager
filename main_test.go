package main

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/fsx"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

// Invented keys and addresses; the addresses come from the ranges reserved for
// documentation (RFC 5737).
const (
	alphaKey = "JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g="
	bravoKey = "SldkcX6LmKWyv8zZ5vMADRonNEFOW2h1go+cqbbD0N0="
)

type fakeReader struct{ state wg.State }

func (r fakeReader) Read() (wg.State, error) { return r.state, nil }

type fakeRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *fakeRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args[0]+" "+strings.TrimSuffix(filepath.Base(args[1]), ".conf"))
	return "", nil
}

func (r *fakeRunner) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type blindLocator struct{}

func (blindLocator) Device(string) (string, bool) { return "", false }

// testEnv builds an environment whose only live tunnel is alpha.
func testEnv(t *testing.T, runner wg.Runner, live ...string) *env {
	t.Helper()

	dir := t.TempDir()
	confs := map[string]string{
		"alpha": "[Peer]\nPublicKey = " + alphaKey + "\nEndpoint = 192.0.2.10:51820\nAllowedIPs = 10.20.30.1/32\n",
		"bravo": "[Peer]\nPublicKey = " + bravoKey + "\nEndpoint = 198.51.100.20:51821\nAllowedIPs = 10.20.31.1/32\n",
	}
	for name, body := range confs {
		if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	var state wg.State
	for _, k := range live {
		state = append(state, wg.Peer{PublicKey: k, Device: "utun7", LastHandshake: time.Now()})
	}

	home := t.TempDir()

	cfg := profile.Default()
	cfg.ConfigDir = dir
	// Load always sets this; a hand-built configuration has to as well, or
	// anything that writes the file writes to nowhere.
	cfg.Path = filepath.Join(home, ".config", "tun-manager", "config.yaml")
	// The defaults point at a Homebrew wg-quick and at /var/run/wireguard.
	// Depending on either would make these tests pass or fail according to what
	// happens to be installed on the machine running them.
	priv := profile.DefaultPrivileged()
	priv.WgQuick = fakeExecutable(t)
	priv.RunDir = t.TempDir()
	// The feed defaults to on and bound at /var/run/tun-manager.sock: off here so
	// the TUI path in these tests never touches a real, non-hermetic socket, and
	// pointed at a short path regardless of that, so a test that flips it back on
	// still binds somewhere private rather than failing on this test's own long
	// name overflowing a unix socket path.
	priv.Feed = false
	priv.FeedSocket = filepath.Join(shortSocketDir(t), "f.sock")
	// Every installation has one: init-privileged writes it, and the feed
	// refuses to publish without a key to prove which publisher it is.
	priv.FeedKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	cfg.Groups = map[string][]string{
		profile.GroupNeeded: {"alpha", "bravo"},
		profile.GroupAll:    {"alpha", "bravo"},
	}

	return &env{
		out: &strings.Builder{},
		// Nothing to read unless a test says so: a command that asks a question
		// and is not given an answer must fail rather than hang.
		in:   strings.NewReader(""),
		euid: 0,
		// A file only root can write is a file no test can create. The loader
		// stands in for it, and one test asserts that a real env reads the
		// fixed path instead.
		privileged: func() (*profile.Privileged, error) { return priv, nil },
		// The layout a test lays out is owned by whoever runs the suite, which
		// the real check refuses on sight. The rules themselves are exercised
		// in internal/cli, where the owner can be arranged; what the tests here
		// are about is that every command asks.
		enforce:        func(string) error { return nil },
		privilegedPath: profile.PrivilegedPath,
		config: func() (*profile.Config, privdrop.User, error) {
			// A real directory: the configuration and its backups are written
			// under it, and a test must not write outside its own tree.
			return cfg, privdrop.User{Username: "operator", HomeDir: home}, nil
		},
		build: func() (*app.App, error) {
			return &app.App{
				Config:  cfg,
				Reader:  fakeReader{state: state},
				Locator: blindLocator{},
				Lister: func() ([]netctx.Iface, error) {
					return []netctx.Iface{{Name: "en0", Addrs: []netip.Prefix{netip.MustParsePrefix("203.0.113.9/24")}}}, nil
				},
				Control: &wg.Controller{WgQuick: "/bin/true", Runner: runner, Check: installedWgQuick},
			}, nil
		},
	}
}

// fakeExecutable stands in for wg-quick so that doctor has something to find.
func fakeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wg-quick")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func output(e *env) string { return e.out.(*strings.Builder).String() }

// shortSocketDir returns a temporary directory short enough to bind a unix
// socket under, unlike t.TempDir() here: it embeds this test's name, and a
// long one pushes the path past the ~104-byte limit macOS enforces. See
// internal/feed/server_test.go's socketPath for the same fix.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tm")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// The feed refuses to bind under a directory somebody other than root could
	// write. The mode here is real; the owner is the one thing a suite cannot
	// arrange without sudo, so it is stood in for.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	previous := fsx.Owner
	fsx.Owner = func(string, os.FileInfo) (int, int) { return fsx.Root, fsx.Root }
	t.Cleanup(func() { fsx.Owner = previous })
	return dir
}

func TestHelpIsPrintedWithoutTouchingTheSystem(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	e.build = func() (*app.App, error) { return nil, errors.New("build must not run for help") }

	for _, arg := range []string{"help", "-h", "--help"} {
		e.out = &strings.Builder{}
		if err := e.run([]string{arg}); err != nil {
			t.Fatalf("run(%q): %v", arg, err)
		}
		if !strings.Contains(output(e), "Usage:") {
			t.Errorf("run(%q) printed no usage:\n%s", arg, output(e))
		}
	}
}

func TestUnknownCommandIsRejected(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	err := e.run([]string{"frobnicate"})

	if err == nil {
		t.Fatal("run succeeded on an unknown command, want an error")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("err = %v, want it to name the command", err)
	}
}

func TestEveryCommandButDoctorNeedsRoot(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"up", "alpha"}, {"down", "--all"}, nil} {
		e := testEnv(t, &fakeRunner{})
		e.euid = 501

		err := e.run(args)

		if err == nil {
			t.Fatalf("run(%v) succeeded as a plain user, want an error", args)
		}
		if !strings.Contains(err.Error(), "sudo") {
			t.Errorf("run(%v) err = %v, want it to mention sudo", args, err)
		}
	}
}

func TestDoctorRunsWithoutRoot(t *testing.T) {
	// Telling you that you are not root is doctor's job, so it must run anyway.
	e := testEnv(t, &fakeRunner{})
	e.euid = 501

	err := e.run([]string{"doctor"})

	if err == nil {
		t.Fatal("doctor returned nil as a plain user, want the failed check reported")
	}
	if !strings.Contains(output(e), "running as root") {
		t.Errorf("doctor printed no root check:\n%s", output(e))
	}
}

func TestDoctorSucceedsWhenEveryCheckPasses(t *testing.T) {
	e := demoEnv(t, &fakeRunner{})

	// Through --wg-socket, which is the only shape of clean report reachable
	// from here: a fixture is owned by whoever runs the suite, and the
	// permission checks want root on the WireGuard side. A simulated run skips
	// them on purpose - its config_dir is a directory of fixtures in a
	// checked-out repository. Those checks are exercised in internal/cli, where
	// the owner can be arranged.
	if err := e.run([]string{"--wg-socket", t.TempDir(), "doctor"}); err != nil {
		t.Fatalf("doctor: %v\n%s", err, output(e))
	}
	if !strings.Contains(output(e), "config dir") {
		t.Errorf("doctor printed no report:\n%s", output(e))
	}
}

func TestStatusPrintsATable(t *testing.T) {
	e := testEnv(t, &fakeRunner{}, alphaKey)

	if err := e.run([]string{"status"}); err != nil {
		t.Fatalf("status: %v", err)
	}

	got := output(e)
	for _, want := range []string{"NAME", "alpha", "bravo", "utun7"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestStatusJSONFlagSwitchesFormat(t *testing.T) {
	e := testEnv(t, &fakeRunner{}, alphaKey)

	if err := e.run([]string{"status", "--json"}); err != nil {
		t.Fatalf("status --json: %v", err)
	}

	if !strings.HasPrefix(strings.TrimSpace(output(e)), "{") {
		t.Errorf("output is not JSON:\n%s", output(e))
	}
}

func TestStatusRejectsAnUnknownFlag(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"status", "--nope"}); err == nil {
		t.Fatal("status accepted an unknown flag, want an error")
	}
}

func TestUpStartsTheNamedTunnels(t *testing.T) {
	runner := &fakeRunner{}
	e := testEnv(t, runner)

	if err := e.run([]string{"up", "bravo"}); err != nil {
		t.Fatalf("up: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "up bravo" {
		t.Errorf("commands = %v, want [up bravo]", got)
	}
}

func TestUpGroupStartsTheWholeGroup(t *testing.T) {
	runner := &fakeRunner{}
	e := testEnv(t, runner)

	if err := e.run([]string{"up", "--group", "needed"}); err != nil {
		t.Fatalf("up --group: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "up alpha,up bravo" {
		t.Errorf("commands = %v, want the whole needed group", got)
	}
}

func TestUpRefusesAGroupAndNamesTogether(t *testing.T) {
	runner := &fakeRunner{}
	e := testEnv(t, runner)

	err := e.run([]string{"up", "--group", "needed", "alpha"})

	if err == nil {
		t.Fatal("up accepted --group with tunnel names, want an error")
	}
	if len(runner.actions()) != 0 {
		t.Errorf("commands = %v, want none: the run must be refused before acting", runner.actions())
	}
}

func TestUpWithoutATargetIsRejected(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"up"}); err == nil {
		t.Fatal("up with no argument succeeded, want an error")
	}
}

func TestUpRejectsAnUnknownTunnel(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"up", "ghost"}); err == nil {
		t.Fatal("up accepted an unknown tunnel, want an error")
	}
}

func TestDownStopsTheNamedTunnels(t *testing.T) {
	runner := &fakeRunner{}
	e := testEnv(t, runner, alphaKey)

	if err := e.run([]string{"down", "alpha"}); err != nil {
		t.Fatalf("down: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "down alpha" {
		t.Errorf("commands = %v, want [down alpha]", got)
	}
}

func TestDownAllStopsEveryLiveTunnel(t *testing.T) {
	runner := &fakeRunner{}
	e := testEnv(t, runner, alphaKey)

	if err := e.run([]string{"down", "--all"}); err != nil {
		t.Fatalf("down --all: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "down alpha" {
		t.Errorf("commands = %v, want only the live tunnel stopped", got)
	}
}

func TestDownRefusesAllAndNamesTogether(t *testing.T) {
	runner := &fakeRunner{}
	e := testEnv(t, runner, alphaKey)

	err := e.run([]string{"down", "--all", "alpha"})

	if err == nil {
		t.Fatal("down accepted --all with tunnel names, want an error")
	}
	if len(runner.actions()) != 0 {
		t.Errorf("commands = %v, want none", runner.actions())
	}
}

func TestDownWithoutATargetIsRejected(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"down"}); err == nil {
		t.Fatal("down with no argument succeeded, want an error")
	}
}

func TestConfigLoadFailureStopsTheCommand(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("unreadable config")
	e.config = func() (*profile.Config, privdrop.User, error) { return nil, privdrop.User{}, boom }

	if err := e.run([]string{"doctor"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestBuildFailureStopsTheCommand(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("no wireguard socket")
	e.build = func() (*app.App, error) { return nil, boom }

	if err := e.run([]string{"status"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestConfigPathSitsUnderTheRealUserHome(t *testing.T) {
	// Under sudo, HOME points at /var/root; the file lives in the real home.
	u := privdrop.User{Username: "operator", HomeDir: "/home/operator"}

	got := configPath(u)

	want := filepath.Join("/home/operator", ".config", "tun-manager", "config.yaml")
	if got != want {
		t.Errorf("configPath = %q, want %q", got, want)
	}
}

func TestTUIIsTheDefaultCommand(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	var started bool
	e.interactive = func(context.Context, *app.App, *feed.Server, []string) error {
		started = true
		return nil
	}

	if err := e.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !started {
		t.Error("the TUI was not started, want it as the default command")
	}
}

func TestTUIFailureIsReported(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("no terminal")
	e.interactive = func(context.Context, *app.App, *feed.Server, []string) error { return boom }

	if err := e.run(nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestTUIBuildFailureStopsBeforeStarting(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	e.build = func() (*app.App, error) { return nil, errors.New("no wireguard socket") }
	e.interactive = func(context.Context, *app.App, *feed.Server, []string) error {
		t.Fatal("the TUI started despite a failed build")
		return nil
	}

	if err := e.run(nil); err == nil {
		t.Fatal("run succeeded with a failed build, want an error")
	}
}

func TestTheInterfaceStartsWithoutAFeedWhenItIsOff(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	var got *feed.Server
	e.interactive = func(_ context.Context, _ *app.App, f *feed.Server, _ []string) error {
		got = f
		return nil
	}

	if err := e.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got != nil {
		t.Errorf("feed = %+v, want none when feed is off", got)
	}
}

func TestTheInterfaceStartsWithAFeedWhenItIsOn(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	sock := filepath.Join(shortSocketDir(t), "f.sock")
	// Turning the feed on is a change to the root-only half now, so it happens
	// there rather than on the App.
	basePrivileged := e.privileged
	e.privileged = func() (*profile.Privileged, error) {
		priv, err := basePrivileged()
		if err != nil {
			return nil, err
		}
		priv.Feed = true
		priv.FeedSocket = sock
		return priv, nil
	}
	var got *feed.Server
	e.interactive = func(_ context.Context, _ *app.App, f *feed.Server, _ []string) error {
		got = f
		// Checked here, not after e.run returns: runTUI cancels the feed's
		// context once this callback returns, and its own shutdown removes
		// the socket file.
		if f == nil {
			t.Fatal("feed = nil, want one when feed is on")
		}
		if _, err := os.Stat(sock); err != nil {
			t.Errorf("socket not created: %v", err)
		}
		return nil
	}

	if err := e.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got == nil {
		t.Error("feed = nil, want one when feed is on")
	}
}

func TestTheInterfaceStepsOverAFeedThatCannotBind(t *testing.T) {
	// Losing the menu bar must never cost the ability to bring a tunnel up: a
	// feed that cannot start is reported and stepped over, not fatal.
	e := testEnv(t, &fakeRunner{})
	basePrivileged := e.privileged
	e.privileged = func() (*profile.Privileged, error) {
		priv, err := basePrivileged()
		if err != nil {
			return nil, err
		}
		priv.Feed = true
		priv.FeedSocket = filepath.Join(shortSocketDir(t), "no-such-dir", "f.sock")
		return priv, nil
	}
	var got *feed.Server
	var told []string
	started := false
	e.interactive = func(_ context.Context, _ *app.App, f *feed.Server, problems []string) error {
		got, told, started = f, problems, true
		return nil
	}

	if err := e.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !started {
		t.Fatal("the TUI never started: an unbindable feed must not be fatal")
	}
	if got != nil {
		t.Errorf("feed = %+v, want none when the socket cannot bind", got)
	}
	// Handed to the interface rather than printed: a line written here goes to
	// a terminal the alternate screen covers a millisecond later, which is how
	// this message went unread on a real machine.
	if len(told) != 1 || !strings.Contains(told[0], "status feed unavailable") {
		t.Errorf("the interface was told %q, want the reason the feed did not start", told)
	}
	if strings.Contains(output(e), "status feed unavailable") {
		t.Errorf("the reason was printed where nobody would see it:\n%s", output(e))
	}
}

func TestStartFeedsServedChannelClosesOnlyOnceServeReturns(t *testing.T) {
	// startFeed's contract is the whole reason runTUI can wait correctly: the
	// channel it returns must stay open while Serve is still accepting, and
	// close only once Serve has actually returned - not merely once the
	// context has been cancelled, which is a different moment.
	e := testEnv(t, &fakeRunner{})
	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Feed = true
	priv.FeedSocket = filepath.Join(shortSocketDir(t), "f.sock")

	ctx, cancel := context.WithCancel(context.Background())
	f, served, _ := e.startFeed(ctx, a, priv, privdrop.User{})
	if f == nil {
		t.Fatal("feed = nil, want one when feed is on")
	}

	select {
	case <-served:
		t.Fatal("served closed before the context was ever cancelled")
	default:
	}

	cancel()

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("served never closed after the context was cancelled")
	}

	if _, err := os.Stat(priv.FeedSocket); !os.IsNotExist(err) {
		t.Error("socket still present once served closed, want Serve's own shutdown to have removed it")
	}
}

func TestStartFeedReturnsNoChannelWhenThereIsNoFeed(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Feed = false

	f, served, _ := e.startFeed(context.Background(), a, priv, privdrop.User{})

	if f != nil || served != nil {
		t.Errorf("startFeed = (%v, %v), want (nil, nil) when the feed is off", f, served)
	}
}

func TestUpRejectsAnUnknownFlag(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"up", "--nope", "alpha"}); err == nil {
		t.Fatal("up accepted an unknown flag, want an error")
	}
}

func TestDownRejectsAnUnknownFlag(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"down", "--nope"}); err == nil {
		t.Fatal("down accepted an unknown flag, want an error")
	}
}

func TestUpReportsThatNothingWasNeeded(t *testing.T) {
	e := testEnv(t, &fakeRunner{}, alphaKey)

	if err := e.run([]string{"up", "alpha"}); err != nil {
		t.Fatalf("up: %v", err)
	}

	if !strings.Contains(output(e), "nothing to do") {
		t.Errorf("output = %q, want it to say the tunnel was already up", output(e))
	}
}

func TestNewEnvReadsTheProcess(t *testing.T) {
	e := newEnv()

	if e.out == nil || e.config == nil || e.build == nil || e.interactive == nil {
		t.Errorf("env is incomplete: %+v", e)
	}
	if e.euid != os.Geteuid() {
		t.Errorf("euid = %d, want %d", e.euid, os.Geteuid())
	}
}

func TestVersionIsReportedWithoutRoot(t *testing.T) {
	// Knowing which binary you have must not require a password.
	e := testEnv(t, &fakeRunner{})
	e.euid = 501
	e.build = func() (*app.App, error) { return nil, errors.New("version must not open anything") }

	if err := e.run([]string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}

	if !strings.Contains(output(e), version) {
		t.Errorf("output = %q, want it to contain %q", output(e), version)
	}
}

func TestVersionDefaultsToDev(t *testing.T) {
	// Release builds stamp this through -ldflags; a local build says so.
	if version == "" {
		t.Error("version is empty, want a default")
	}
}

func TestDoctorReportsTheVersion(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	// The error is ignored rather than asserted on. A fixture is owned by
	// whoever runs the suite, so the permission checks fail on it by
	// construction - and what this test is about is that the report comes out
	// and names the build. Whether each check passes is settled in
	// internal/cli, where the owner can be arranged.
	_ = e.run([]string{"doctor"})

	if !strings.Contains(output(e), version) {
		t.Errorf("doctor does not report the version:\n%s", output(e))
	}
}

// isolatedHome points the configuration lookup at an empty directory, so these
// tests read the built-in defaults rather than whatever the machine holds.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUDO_USER", "")
	t.Setenv("HOME", home)
	return home
}

func TestLoadConfigReadsUnderTheRealUserHome(t *testing.T) {
	home := isolatedHome(t)

	cfg, u, err := newEnv().loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if u.HomeDir != home {
		t.Errorf("HomeDir = %q, want %q", u.HomeDir, home)
	}
	if cfg.Path != filepath.Join(home, ".config", "tun-manager", "config.yaml") {
		t.Errorf("Path = %q", cfg.Path)
	}
	if !cfg.IsDefault {
		t.Error("IsDefault = false, want the defaults for an empty home")
	}
}

func TestLoadConfigReportsAnUnreadableFile(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, ".config", "tun-manager")
	if err := os.MkdirAll(filepath.Join(dir, "config.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, _, err := newEnv().loadConfig(); err == nil {
		t.Fatal("loadConfig succeeded on an unreadable configuration, want an error")
	}
}

func TestLoadConfigReportsAnUnresolvableSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "nobody-by-that-name")

	if _, _, err := newEnv().loadConfig(); err == nil {
		t.Fatal("loadConfig succeeded with an unresolvable SUDO_USER, want an error")
	}
}

func TestBuildWiresTheApplicationFromBothHalvesOfTheConfiguration(t *testing.T) {
	// buildApp is what the injected env.build stands in for everywhere else, so
	// nothing else ever runs it.
	//
	// Driven as a simulated run, because the real path reads
	// /private/wireguard/config/tun-manager.yaml, which is root's: a test that
	// needed that file would only pass on a machine set up for production.
	isolatedHome(t)
	stub := fakeExecutable(t)
	runDir := t.TempDir()

	e := newEnv()
	e.euid = 501
	if _, err := e.parseFlags([]string{"--wg-quick", stub, "--wg-socket", runDir}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	a, err := e.buildApp()
	if err != nil {
		t.Fatalf("buildApp: %v", err)
	}
	t.Cleanup(func() {
		if r, ok := a.Reader.(*wg.CtrlReader); ok {
			_ = r.Close()
		}
	})

	if a.Reader == nil || a.Pinger == nil || a.Control == nil {
		t.Fatalf("application incomplete: %+v", a)
	}
	// The two fields that come from the root-only half. Assembling an App
	// without having read it is not expressible: assemble takes it.
	if a.Control.WgQuick != stub {
		t.Errorf("controller uses %q, want the privileged %q", a.Control.WgQuick, stub)
	}
	if got, ok := a.Locator.(wg.RunDirLocator); !ok || got.Dir != runDir {
		t.Errorf("locator = %+v, want it to follow run_dir %q", a.Locator, runDir)
	}
	// The pre-check that skips a tunnel whose address already answers only
	// happens if the controller has a pinger.
	if a.Control.Pinger == nil {
		t.Error("the controller has no pinger, so up would never skip")
	}
}

func TestBuildRefusesToRunWithoutThePrivilegedConfiguration(t *testing.T) {
	// Not defaults, and not a warning: a machine where that file cannot be read
	// is a machine where nothing should be brought up.
	isolatedHome(t)

	e := newEnv()
	e.euid = 0
	e.privilegedPath = filepath.Join(t.TempDir(), "tun-manager.yaml")

	if _, err := e.buildApp(); err == nil {
		t.Fatal("buildApp succeeded without the privileged configuration")
	}
}

func TestBuildReportsAConfigurationFailure(t *testing.T) {
	t.Setenv("SUDO_USER", "nobody-by-that-name")

	if _, err := newEnv().buildApp(); err == nil {
		t.Fatal("buildApp succeeded on an unreadable configuration, want an error")
	}
}

func TestRunTUIStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- runTUI(ctx, nil, nil, nil) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err = %v, want a cancellation to read as a clean stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runTUI did not return after the context was cancelled")
	}
}

// brokenEnv can build an application, but that application cannot read a view.
func brokenEnv(t *testing.T) *env {
	t.Helper()
	e := testEnv(t, &fakeRunner{})
	e.build = func() (*app.App, error) {
		cfg := profile.Default()
		cfg.ConfigDir = t.TempDir() // no *.conf in it
		return &app.App{
			Config:  cfg,
			Reader:  fakeReader{},
			Locator: blindLocator{},
			Control: &wg.Controller{WgQuick: "/bin/true", Runner: &fakeRunner{}, Check: installedWgQuick},
		}, nil
	}
	return e
}

func TestStatusReportsAViewFailure(t *testing.T) {
	if err := brokenEnv(t).run([]string{"status"}); err == nil {
		t.Fatal("status succeeded on an unreadable state, want an error")
	}
}

func TestUpReportsAViewFailure(t *testing.T) {
	if err := brokenEnv(t).run([]string{"up", "alpha"}); err == nil {
		t.Fatal("up succeeded on an unreadable state, want an error")
	}
}

func TestDownReportsAViewFailure(t *testing.T) {
	if err := brokenEnv(t).run([]string{"down", "alpha"}); err == nil {
		t.Fatal("down succeeded on an unreadable state, want an error")
	}
}

func TestUpGroupReportsAViewFailure(t *testing.T) {
	// Through act, which is where a batch reports what stopped it.
	if err := brokenEnv(t).run([]string{"up", "--group", "needed"}); err == nil {
		t.Fatal("up --group succeeded on an unreadable state, want an error")
	}
}

func TestDoctorReportsAWriteFailure(t *testing.T) {
	// `tun-manager doctor | head` closes the pipe early.
	e := testEnv(t, &fakeRunner{})
	e.out = failingWriter{err: errors.New("broken pipe")}

	if err := e.run([]string{"doctor"}); err == nil {
		t.Fatal("doctor succeeded with a broken output, want the error reported")
	}
}

// failingWriter refuses every write, like a closed pipe.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestABatchReportsThatTheApplicationCouldNotBeBuilt(t *testing.T) {
	// Through act, which every batch goes through, unlike status.
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("no wireguard socket")
	e.build = func() (*app.App, error) { return nil, boom }

	if err := e.run([]string{"up", "--group", "needed"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestImportAddsTheTunnelToTheConfiguration(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	cfg, _, _ := e.config()
	source := filepath.Join(t.TempDir(), "downloaded.conf")
	body := "[Peer]\nPublicKey = " + alphaKey + "\nEndpoint = 192.0.2.10:51820\n" +
		"AllowedIPs = 10.20.30.0/24\n# TO_CHECK=10.20.30.1\n"
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// --yes, because what this test is about is what import writes; that it
	// asks first has its own tests.
	if err := e.run([]string{"import", "--yes", "charlie", source}); err != nil {
		t.Fatalf("import: %v", err)
	}

	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, "charlie.conf")); err != nil {
		t.Errorf("the configuration was not copied: %v", err)
	}
	written, err := profile.Load(cfg.Path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !slices.Contains(written.Groups[profile.GroupAll], "charlie") {
		t.Errorf("all = %v, want charlie in it", written.Groups[profile.GroupAll])
	}
	if !strings.Contains(output(e), "charlie") {
		t.Errorf("output does not name the tunnel:\n%s", output(e))
	}
}

func TestImportNeedsBothArguments(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	for _, args := range [][]string{
		{"import"}, {"import", "charlie"}, {"import", "a", "b", "c"}, {"import", "--nope", "a", "b"},
	} {
		if err := e.run(args); err == nil {
			t.Errorf("run(%v) succeeded, want the usage line", args)
		}
	}
}

func TestImportStopsWhenTheConfigurationCannotBeRead(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("unreadable config")
	e.config = func() (*profile.Config, privdrop.User, error) { return nil, privdrop.User{}, boom }

	if err := e.run([]string{"import", "charlie", "whatever.conf"}); !errors.Is(err, boom) {
		t.Errorf("import = %v, want %v", err, boom)
	}
}

func TestImportNeedsRoot(t *testing.T) {
	// It writes into the WireGuard configuration directory, which is root's.
	e := testEnv(t, &fakeRunner{})
	e.euid = 501

	if err := e.run([]string{"import", "charlie", "whatever.conf"}); err == nil {
		t.Error("import ran without root")
	}
}

func TestBackupArchivesEverythingWorthKeeping(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	cfg, _, _ := e.config()
	e.now = func() time.Time { return time.Date(2026, 8, 18, 14, 23, 5, 0, time.UTC) }
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfg.Path, []byte("groups:\n  all: [alpha]\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := e.run([]string{"backup"}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	dest := filepath.Join(filepath.Dir(cfg.ConfigDir), "tun-manager-20260818-142305.tar.gz")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the archive was not written: %v", err)
	}
	if !strings.Contains(output(e), dest) {
		t.Errorf("output does not say where the archive went:\n%s", output(e))
	}
}

func TestBackupTakesNoArguments(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	for _, args := range [][]string{{"backup", "extra"}, {"backup", "--nope"}} {
		if err := e.run(args); err == nil {
			t.Errorf("run(%v) succeeded, want the usage line", args)
		}
	}
}

func TestBackupStopsWhenTheConfigurationCannotBeRead(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("unreadable config")
	e.config = func() (*profile.Config, privdrop.User, error) { return nil, privdrop.User{}, boom }

	if err := e.run([]string{"backup"}); !errors.Is(err, boom) {
		t.Errorf("backup = %v, want %v", err, boom)
	}
}

func TestBackupNeedsRoot(t *testing.T) {
	// It reads the WireGuard configuration directory, which is root's.
	e := testEnv(t, &fakeRunner{})
	e.euid = 501

	if err := e.run([]string{"backup"}); err == nil {
		t.Error("backup ran without root")
	}
}

// --- The flags that come before the command -------------------------------

func TestAFlagBeforeTheCommandLeavesTheCommandAlone(t *testing.T) {
	// flag stops at the first argument that is not a flag, which is what lets a
	// command keep its own flags.
	e := demoEnv(t, &fakeRunner{})

	rest, err := e.parseFlags([]string{"--feed-socket", "/tmp/tm-demo/feed.sock", "status", "--json"})

	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if len(rest) != 2 || rest[0] != "status" || rest[1] != "--json" {
		t.Errorf("rest = %v, want the command and its own flags untouched", rest)
	}
}

func TestAnOverrideReachesEveryCommandRatherThanJustOne(t *testing.T) {
	// Applied once, around the loader. Applying it at each call site is how one
	// of these ends up honoured by status and forgotten by doctor.
	e := demoEnv(t, &fakeRunner{})

	if _, err := e.parseFlags([]string{"--wg-socket", "/tmp/tm-demo/wireguard"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	if priv.RunDir != "/tmp/tm-demo/wireguard" {
		t.Errorf("RunDir = %q, want the flag to have won", priv.RunDir)
	}
}

func TestTheFeedSocketCanBeMovedOffTheDefault(t *testing.T) {
	// So a demo publishes somewhere the installed menu bar application is not
	// listening, and cannot be mistaken for the real thing.
	e := demoEnv(t, &fakeRunner{})

	if _, err := e.parseFlags([]string{"--feed-socket", "/tmp/tm-demo/feed.sock"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	if priv.FeedSocket != "/tmp/tm-demo/feed.sock" {
		t.Errorf("FeedSocket = %q, want the flag to have won", priv.FeedSocket)
	}
}

func TestTheWireGuardDirectoryMovesTheInterfaceNamesToo(t *testing.T) {
	// One flag for both, because that is how /var/run/wireguard is: the sockets
	// and the "<name>.name" files sit side by side. Moving one without the
	// other would resolve every tunnel against the real machine.
	e := demoEnv(t, &fakeRunner{})

	if _, err := e.parseFlags([]string{"--wg-socket", "/tmp/tm-demo/wireguard"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	if priv.RunDir != "/tmp/tm-demo/wireguard" {
		t.Errorf("RunDir = %q, want it to follow --wg-socket", priv.RunDir)
	}
}

func TestAFlagLeftUnsetChangesNothing(t *testing.T) {
	// The overrides are applied unconditionally; an empty one must not blank
	// the configured value.
	e := demoEnv(t, &fakeRunner{})
	before, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	socket, runDir := before.FeedSocket, before.RunDir

	if _, parseErr := e.parseFlags(nil); parseErr != nil {
		t.Fatalf("parseFlags: %v", parseErr)
	}

	after, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	if after.FeedSocket != socket || after.RunDir != runDir {
		t.Errorf("the privileged settings changed with no flags set: %+v", after)
	}
}

func TestAConfigurationThatCannotBeReadIsStillReportedThroughTheOverrides(t *testing.T) {
	// The wrapper must pass the failure on rather than apply overrides to a nil
	// configuration.
	e := demoEnv(t, &fakeRunner{})
	boom := errors.New("unreadable config")
	e.config = func() (*profile.Config, privdrop.User, error) { return nil, privdrop.User{}, boom }

	if _, err := e.parseFlags([]string{"--feed-socket", "/tmp/tm-demo/feed.sock"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	if _, _, err := e.config(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the loader's own failure", err)
	}
}

func TestAnotherConfigurationFileCanBeRead(t *testing.T) {
	isolatedHome(t)
	path := filepath.Join(t.TempDir(), "demo.yaml")
	if err := os.WriteFile(path, []byte("refresh_interval: 90s\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	e := newEnv()
	if _, err := e.parseFlags([]string{"--config", path}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	cfg, _, err := e.config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, want %q", cfg.Path, path)
	}
	if cfg.RefreshInterval != 90*time.Second {
		t.Errorf("RefreshInterval = %s, want the file's own value", cfg.RefreshInterval)
	}
}

func TestInventedProbesReplaceTheRealOnesEverywhere(t *testing.T) {
	// Both the table's latencies and the pre-check that skips a tunnel already
	// reachable: one real probe left in would take the timeout on every row.
	isolatedHome(t)
	e := newEnv()
	if _, err := e.parseFlags([]string{"--fake-ping"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if r, ok := a.Reader.(*wg.CtrlReader); ok {
			_ = r.Close()
		}
	})

	if _, ok := a.Pinger.(probe.Simulated); !ok {
		t.Errorf("pinger = %T, want the simulated one", a.Pinger)
	}
	if _, ok := a.Control.Pinger.(probe.Simulated); !ok {
		t.Errorf("controller pinger = %T, want the simulated one", a.Control.Pinger)
	}
}

func TestTheWireGuardDirectoryIsWhatTheApplicationReads(t *testing.T) {
	isolatedHome(t)
	// A directory of this test's own, and one that does not exist. A path
	// shared with the demo would make this pass or fail depending on whether
	// the simulator happens to be running.
	dir := filepath.Join(t.TempDir(), "wireguard")

	e := newEnv()
	if _, err := e.parseFlags([]string{"--wg-socket", dir}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// An empty directory rather than the machine's own state: the point is that
	// it looked where it was told.
	state, err := a.Reader.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("state = %+v, want nothing from a directory that is not there", state)
	}
	if got, ok := a.Locator.(wg.RunDirLocator); !ok || got.Dir != dir {
		t.Errorf("locator = %+v, want it pointed at the same directory", a.Locator)
	}
}

func TestAnUnknownFlagIsRefusedWithTheUsage(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	err := e.run([]string{"--frobnicate", "status"})

	if err == nil {
		t.Fatal("run accepted a flag it does not have")
	}
	if !strings.Contains(err.Error(), "Usage:") {
		t.Errorf("err = %v, want the usage alongside it", err)
	}
}

func TestHelpIsPrintedEvenWhenItLooksLikeAFlag(t *testing.T) {
	// -h and --help reach the flag set before the command switch, and come back
	// as flag.ErrHelp rather than as a command.
	e := testEnv(t, &fakeRunner{})

	rest, err := e.parseFlags([]string{"--help"})

	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if len(rest) != 1 || rest[0] != "help" {
		t.Errorf("rest = %v, want it handed back as the help command", rest)
	}
}

func TestDoctorSaysWhenItIsLookingAtASimulator(t *testing.T) {
	// Every line below it would otherwise describe something that does not
	// exist.
	e := demoEnv(t, &fakeRunner{})

	_ = e.run([]string{"--wg-socket", "/tmp/tm-demo/wireguard", "--fake-ping", "doctor"})

	if !strings.Contains(output(e), "simulated") {
		t.Errorf("doctor did not say it was simulating:\n%s", output(e))
	}
}

func TestEverySimulationFlagIsRefusedUnderRoot(t *testing.T) {
	// Each of these names something root would then read, run, bind or unlink.
	// Under sudo they are refused outright: what root touches is decided by
	// /private/wireguard/config, not by whoever typed the command.
	for _, args := range [][]string{
		{"--config", "/tmp/anywhere/config.yaml"},
		{"--config-dir", "/tmp/anywhere"},
		{"--feed-socket", "/tmp/anywhere/feed.sock"},
		{"--wg-socket", "/tmp/anywhere/wireguard"},
		{"--wg-quick", "/tmp/anywhere/wg-quick"},
		{"--fake-ping"},
	} {
		t.Run(args[0], func(t *testing.T) {
			e := testEnv(t, &fakeRunner{}) // euid 0

			_, err := e.parseFlags(args)

			if err == nil {
				t.Fatalf("parseFlags(%v) was accepted under root", args)
			}
			if !strings.Contains(err.Error(), args[0]) {
				t.Errorf("error %q does not name %s", err, args[0])
			}
			if !strings.Contains(err.Error(), "sudo") {
				t.Errorf("error %q does not say what to do about it", err)
			}
		})
	}
}

func TestTheConfigDirectoryFlagIsHonouredWithoutRoot(t *testing.T) {
	// The simulator writes its .conf files where it likes and runs as nobody
	// in particular. That is the whole reason these flags are safe there.
	e := demoEnv(t, &fakeRunner{})

	if _, err := e.parseFlags([]string{"--config-dir", "/tmp/tm-demo/conf"}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	cfg, _, err := e.config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.ConfigDir != "/tmp/tm-demo/conf" {
		t.Errorf("ConfigDir = %q, want the flag to have won", cfg.ConfigDir)
	}
}

func TestTheConfigDirectoryIsTheFixedOneUnderRoot(t *testing.T) {
	e := testEnv(t, &fakeRunner{}) // euid 0
	if _, err := e.parseFlags(nil); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	cfg, _, err := e.config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	// testEnv points it at a temporary directory, the way a test has to. What
	// this asserts is that nothing on the command line moved it.
	if cfg.ConfigDir == "/tmp/anywhere" {
		t.Error("ConfigDir followed a flag under root")
	}
}

// demoEnv is testEnv as a plain user, which is what a simulated run is. The
// flags that move where the program looks are only honoured there.
func demoEnv(t *testing.T, runner wg.Runner, live ...string) *env {
	t.Helper()

	e := testEnv(t, runner, live...)
	e.euid = 501
	return e
}

func TestARootRunReadsThePrivilegedFileAndNothingElse(t *testing.T) {
	// The path is a constant. What this checks is that the constant is what the
	// loader is pointed at: a run under sudo takes its dangerous settings from
	// that file, and there is no flag, no environment variable and no user key
	// that moves it.
	if newEnv().privilegedPath != profile.PrivilegedPath {
		t.Errorf("privilegedPath = %q, want %q", newEnv().privilegedPath, profile.PrivilegedPath)
	}
}

func TestARootRunFailsWhenThePrivilegedFileCannotBeRead(t *testing.T) {
	// Not defaults. A file that cannot be read must not become a set of
	// built-in values, because that is a state an attacker can arrange by
	// making the file unreadable.
	e := testEnv(t, &fakeRunner{}) // euid 0
	e.privileged = e.loadPrivileged
	e.privilegedPath = filepath.Join(t.TempDir(), "tun-manager.yaml")

	_, err := e.loadPrivileged()

	if err == nil {
		t.Fatal("loadPrivileged accepted a missing file, want a refusal")
	}
	if !strings.Contains(err.Error(), "init-privileged") {
		t.Errorf("error %q does not say how to create it", err)
	}
}

func TestASimulatedRunTakesItsDangerousSettingsFromTheFlags(t *testing.T) {
	// A simulated run is not root, so it cannot read the privileged file at
	// all — and must not be made to try. The flags stand in for it, and they
	// are refused the moment the run is root.
	e := demoEnv(t, &fakeRunner{})
	e.privileged = e.loadPrivileged
	e.privilegedPath = filepath.Join(t.TempDir(), "absent.yaml")

	if _, err := e.parseFlags([]string{
		"--wg-quick", "/tmp/tm-demo/stub.sh",
		"--wg-socket", "/tmp/tm-demo/wireguard",
		"--feed-socket", "/tmp/tm-demo/feed.sock",
	}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}

	if priv.WgQuick != "/tmp/tm-demo/stub.sh" {
		t.Errorf("WgQuick = %q, want the flag", priv.WgQuick)
	}
	if priv.RunDir != "/tmp/tm-demo/wireguard" {
		t.Errorf("RunDir = %q, want the flag", priv.RunDir)
	}
	if priv.FeedSocket != "/tmp/tm-demo/feed.sock" {
		t.Errorf("FeedSocket = %q, want the flag", priv.FeedSocket)
	}
}

func TestInitPrivilegedNeedsRootEvenWithASimulationFlag(t *testing.T) {
	// The root gate lets a simulated run through, because a simulator is
	// readable by whoever started it. This command writes /private/wireguard,
	// so it asks for root on its own account.
	e := demoEnv(t, &fakeRunner{})
	e.privilegedPath = filepath.Join(t.TempDir(), "wireguard", "config", "tun-manager.yaml")

	err := e.run([]string{"--wg-socket", t.TempDir(), "init-privileged"})

	if err == nil {
		t.Fatal("init-privileged ran without root")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error %q does not say what to type", err)
	}
	if _, statErr := os.Stat(e.privilegedPath); !os.IsNotExist(statErr) {
		t.Error("it wrote the file anyway")
	}
}

func TestInitPrivilegedWritesTheFileTheProgramReads(t *testing.T) {
	// The path is not an argument and not a flag: what init writes is what a
	// root run reads, and there is no way to make the two differ.
	e := testEnv(t, &fakeRunner{}) // euid 0
	e.privilegedPath = filepath.Join(t.TempDir(), "wireguard", "config", "tun-manager.yaml")

	if err := e.run([]string{"init-privileged"}); err != nil {
		t.Fatalf("init-privileged: %v", err)
	}

	if _, err := os.Stat(e.privilegedPath); err != nil {
		t.Fatalf("nothing was written: %v", err)
	}
	if !strings.Contains(output(e), e.privilegedPath) {
		t.Errorf("the report does not name the file:\n%s", output(e))
	}
}

func TestInitPrivilegedRejectsAnUnknownFlag(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"init-privileged", "--replace"}); err == nil {
		t.Fatal("init-privileged accepted --replace")
	}
}

func TestTheRootRefusalNamesTheCommandThatWasAskedFor(t *testing.T) {
	// "run `sudo tun-manager`" is the right thing to type for the interface and
	// the wrong thing for everything else: it would start the TUI instead of
	// doing what was asked.
	e := testEnv(t, &fakeRunner{})
	e.euid = 501

	err := e.run([]string{"init-privileged"})

	if err == nil {
		t.Fatal("init-privileged ran without root")
	}
	if !strings.Contains(err.Error(), "sudo tun-manager init-privileged") {
		t.Errorf("error %q does not name the command", err)
	}
}

func TestTheRootRefusalForTheInterfaceStaysPlain(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	e.euid = 501

	err := e.run(nil)

	if err == nil {
		t.Fatal("the interface started without root")
	}
	if !strings.Contains(err.Error(), "`sudo tun-manager` (see") {
		t.Errorf("error %q, want it to name the bare command", err)
	}
}

func TestEveryCommandThatTouchesTunnelsRefusesALeakyLayout(t *testing.T) {
	// The checks existed and only doctor ran them, which nothing obliges
	// anybody to do. A .conf readable by every process on the machine is not a
	// thing to report and then carry on using.
	boom := errors.New("alpha.conf is 0644, want 0600 or stricter")

	for _, args := range [][]string{
		nil, // the interface
		{"status"},
		{"up", "alpha"},
		{"down", "--all"},
		{"backup"},
		{"import", "alpha", "/tmp/nowhere.conf"},
	} {
		name := "tui"
		if len(args) > 0 {
			name = args[0]
		}
		t.Run(name, func(t *testing.T) {
			e := testEnv(t, &fakeRunner{})
			// The real assembly, not the stand-in every other test injects:
			// what is being checked is that the path from the command to the
			// tunnels goes through the permission check.
			e.build = e.buildApp
			e.enforce = func(string) error { return boom }

			err := e.run(args)

			if !errors.Is(err, boom) {
				t.Errorf("err = %v, want the layout to have stopped it", err)
			}
		})
	}
}

func TestTheModesAreCheckedWhereTheTunnelsAre(t *testing.T) {
	// Whatever config_dir resolves to for this run, not the constant: a test
	// and the simulator both point it elsewhere, and checking the constant
	// would be checking somebody else's machine.
	e := testEnv(t, &fakeRunner{})
	var checked string
	e.enforce = func(dir string) error {
		checked = dir
		return nil
	}

	a, err := e.buildApp()
	if err != nil {
		t.Fatalf("buildApp: %v", err)
	}
	t.Cleanup(func() {
		if r, ok := a.Reader.(*wg.CtrlReader); ok {
			_ = r.Close()
		}
	})

	cfg, _, err := e.config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if checked != cfg.ConfigDir {
		t.Errorf("checked %q, want %q", checked, cfg.ConfigDir)
	}
}

func TestASimulatedRunIsNotHeldToTheModes(t *testing.T) {
	// Its config_dir is a directory of fixtures in a checked-out repository,
	// owned by whoever cloned it and holding no key. Demanding root of it would
	// mean demanding that the demo be run as root, which is the one thing the
	// simulator exists to avoid.
	e := demoEnv(t, &fakeRunner{})
	e.enforce = func(string) error { return errors.New("owned by uid 501 rather than root") }

	if _, err := e.parseFlags([]string{"--wg-socket", t.TempDir()}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	if _, err := e.build(); err != nil {
		t.Errorf("a simulated run was held to the modes: %v", err)
	}
}

func TestARealEnvironmentChecksTheModesForReal(t *testing.T) {
	if newEnv().enforce == nil {
		t.Fatal("newEnv left the permission check unwired, so nothing enforces it")
	}
}

// installedWgQuick stands in for the check wg.Controller makes on the binary
// before running it. These tests name a wg-quick that is not installed, because
// what they are about is everything around the call; the check has its own
// tests in internal/wg.
func installedWgQuick(string) error { return nil }

func TestImportAsksBeforeItWrites(t *testing.T) {
	e, source := importable(t)
	e.in = strings.NewReader("n\n")

	err := e.run([]string{"import", "charlie", source})

	if err == nil {
		t.Fatal("import went ahead on an answer of no")
	}
	if !strings.Contains(output(e), "[y/N]") {
		t.Errorf("nothing was asked:\n%s", output(e))
	}
}

func TestImportGoesAheadOnYes(t *testing.T) {
	e, source := importable(t)
	e.in = strings.NewReader("y\n")

	if err := e.run([]string{"import", "charlie", source}); err != nil {
		t.Fatalf("import: %v", err)
	}

	if !strings.Contains(output(e), "imported charlie") {
		t.Errorf("import did not go ahead:\n%s", output(e))
	}
}

func TestImportWithYesAsksNothing(t *testing.T) {
	// For a script, and for somebody importing eight configurations in a row
	// who has already read them.
	e, source := importable(t)
	e.in = strings.NewReader("") // nothing to read: --yes must not need it

	if err := e.run([]string{"import", "--yes", "charlie", source}); err != nil {
		t.Fatalf("import --yes: %v", err)
	}

	if strings.Contains(output(e), "[y/N]") {
		t.Errorf("--yes still asked:\n%s", output(e))
	}
	if !strings.Contains(output(e), "imported charlie") {
		t.Errorf("--yes did not import:\n%s", output(e))
	}
}

func TestImportWithNobodyToAskSaysSo(t *testing.T) {
	e, source := importable(t)
	e.in = strings.NewReader("")

	err := e.run([]string{"import", "charlie", source})

	if err == nil {
		t.Fatal("import went ahead with nobody to ask")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error %q does not say how to import without being asked", err)
	}
}

// importable builds an environment and a configuration worth importing.
func importable(t *testing.T) (*env, string) {
	t.Helper()

	e := testEnv(t, &fakeRunner{})
	source := filepath.Join(t.TempDir(), "downloaded.conf")
	body := "[Interface]\nPrivateKey = " + alphaKey + "\n\n[Peer]\nPublicKey = " + bravoKey +
		"\nEndpoint = 192.0.2.10:51820\n# TO_CHECK=10.20.30.1\n"
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return e, source
}

func TestTheStrictRulesReachTheControllerThatRunsWgQuick(t *testing.T) {
	// A rule in the privileged configuration that never reached the code
	// running wg-quick would be a setting that reads as enforced and is not.
	e := testEnv(t, &fakeRunner{})
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.WgQuickRootOwned = true
	priv.WgQuickNoSymlink = true

	a, err := e.buildApp()
	if err != nil {
		t.Fatalf("buildApp: %v", err)
	}

	if !a.Control.Strict.RootOwner || !a.Control.Strict.NoSymlink {
		t.Errorf("the controller was given %+v, want both rules", a.Control.Strict)
	}
}

// MARK: what main itself does

func TestMainReportsAFailureAndExitsNonZero(t *testing.T) {
	// The one thing this function is for. os.Exit would end the test process
	// before anything could be looked at, so both ends of it are variables.
	isolatedHome(t)
	var said strings.Builder
	var code int
	swapMain(t, &said, &code, []string{"tun-manager", "nonsense-command"})

	main()

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(said.String(), "tun-manager: ") {
		t.Errorf("nothing useful was said on stderr: %q", said.String())
	}
}

func TestMainSaysNothingWhenTheRunSucceeds(t *testing.T) {
	isolatedHome(t)
	var said strings.Builder
	code := -1
	swapMain(t, &said, &code, []string{"tun-manager", "version"})

	main()

	if code != -1 {
		t.Errorf("exit was called with %d on a run that worked", code)
	}
	if said.String() != "" {
		t.Errorf("stderr = %q, want nothing", said.String())
	}
}

// swapMain points main at a writer and an exit a test can read, and gives it
// the command line to run.
func swapMain(t *testing.T, said *strings.Builder, code *int, args []string) {
	t.Helper()

	previousExit, previousErr, previousArgs := exit, stderr, os.Args
	exit = func(status int) { *code = status }
	stderr = said
	os.Args = args
	t.Cleanup(func() { exit, stderr, os.Args = previousExit, previousErr, previousArgs })
}

func TestBuildReportsAControlClientItCannotOpen(t *testing.T) {
	// Opening it only records where to look, so on darwin it does not fail.
	// What it decides is whether the program can see the tunnels at all, which
	// is not a thing to discover from a nil pointer later.
	isolatedHome(t)
	boom := errors.New("no wireguard on this platform")
	previous := newReader
	newReader = func() (*wg.CtrlReader, error) { return nil, boom }
	t.Cleanup(func() { newReader = previous })

	e := testEnv(t, &fakeRunner{})

	if _, err := e.buildApp(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure to open the client", err)
	}
}

func TestTheOverridesPassOnAPrivilegedFailure(t *testing.T) {
	// The wrapper around the loader must report what the loader said rather
	// than apply flags to a configuration it never got.
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("tun-manager.yaml is a symbolic link")
	e.privileged = func() (*profile.Privileged, error) { return nil, boom }

	if _, err := e.parseFlags(nil); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}

	if _, err := e.privileged(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the loader's own failure", err)
	}
}

func TestTwoRefusedFlagsAreNamedTogether(t *testing.T) {
	// "each of them" rather than "it": somebody who passed two flags is told
	// about both, and reads one sentence rather than running the command twice
	// to find the second.
	e := testEnv(t, &fakeRunner{}) // euid 0

	_, err := e.parseFlags([]string{"--fake-ping", "--wg-socket", "/tmp/anywhere"})

	if err == nil {
		t.Fatal("two simulation flags were accepted under root")
	}
	for _, want := range []string{"--fake-ping", "--wg-socket", "each of them"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestInitPrivilegedTakesNoArgument(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	err := e.run([]string{"init-privileged", "somewhere"})

	if err == nil {
		t.Fatal("init-privileged accepted a path to write to")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("error %q does not say how to call it", err)
	}
}

func TestDoctorReportsAPrivilegedFileItCannotRead(t *testing.T) {
	// The one command that carries on without it: telling somebody why root
	// cannot read what it needs is its whole job.
	e := testEnv(t, &fakeRunner{})
	e.privileged = func() (*profile.Privileged, error) {
		return nil, errors.New("tun-manager.yaml is owned by uid 501 rather than root")
	}

	_ = e.run([]string{"doctor"})

	got := output(e)
	if !strings.Contains(got, "privileged config") {
		t.Errorf("doctor said nothing about the privileged file:\n%s", got)
	}
	if !strings.Contains(got, "uid 501") {
		t.Errorf("doctor did not say why it could not read it:\n%s", got)
	}
}

func TestTheInterfaceRefusesToStartWithoutThePrivilegedFile(t *testing.T) {
	// Unlike doctor. Everything the interface does needs to know what root may
	// run, and defaults that appear when the file cannot be read are defaults
	// somebody can arrange to get.
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("tun-manager.yaml is missing")
	e.privileged = func() (*profile.Privileged, error) { return nil, boom }

	if err := e.run(nil); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the interface to have refused", err)
	}
}

func TestASimulatedRunSaysSoToTheFeed(t *testing.T) {
	// The feed refuses to bind under a directory root does not own. A demo's
	// socket goes wherever --feed-socket said, under a directory belonging to
	// whoever started it, and that flag cannot be passed under sudo.
	e := demoEnv(t, &fakeRunner{})
	if _, err := e.parseFlags([]string{"--feed-socket", filepath.Join(shortSocketDir(t), "f.sock")}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Feed = true

	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, served, _ := e.startFeed(ctx, a, priv, privdrop.User{})

	if f == nil {
		t.Fatal("the feed did not start for a simulated run")
	}
	if !f.Simulated {
		t.Error("the feed was not told the run is simulated, so it will refuse the demo's directory")
	}
	cancel()
	<-served
}

func TestFeedKeyPrintsTheFingerprintTheApplicationPins(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Path = filepath.Join(t.TempDir(), "tun-manager.yaml")
	priv.FeedKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

	if err := e.run([]string{"feed-key"}); err != nil {
		t.Fatalf("feed-key: %v", err)
	}

	if !strings.Contains(output(e), "feed key") {
		t.Errorf("output = %q, want the fingerprint", output(e))
	}
	if strings.Contains(output(e), priv.FeedKey.Reveal()) {
		t.Errorf("output = %q, prints the key itself", output(e))
	}
}

func TestFeedKeyRotatesWhenAskedTo(t *testing.T) {
	e, path := aFeedKeyOnDisk(t)
	e.in = strings.NewReader("y\n")

	if err := e.run([]string{"feed-key", "--rotate"}); err != nil {
		t.Fatalf("feed-key --rotate: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=") {
		t.Error("the key did not change")
	}
	if !strings.Contains(output(e), "[y/N]") {
		t.Errorf("nothing was asked before every pinned connection was broken:\n%s", output(e))
	}
}

func TestFeedKeyRotationCanBeAgreedToOnTheCommandLine(t *testing.T) {
	e, _ := aFeedKeyOnDisk(t)
	e.in = strings.NewReader("") // nothing to read: --yes must not need it

	if err := e.run([]string{"feed-key", "--rotate", "--yes"}); err != nil {
		t.Fatalf("feed-key --rotate --yes: %v", err)
	}

	if strings.Contains(output(e), "[y/N]") {
		t.Errorf("--yes still asked:\n%s", output(e))
	}
}

func TestFeedKeyTakesNoArgument(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"feed-key", "somewhere"}); err == nil {
		t.Fatal("feed-key accepted an argument")
	}
}

func TestFeedKeyReportsAPrivilegedFileItCannotRead(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("tun-manager.yaml is owned by uid 501 rather than root")
	e.privileged = func() (*profile.Privileged, error) { return nil, boom }

	if err := e.run([]string{"feed-key"}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the reason the file could not be read", err)
	}
}

// aFeedKeyOnDisk gives an environment whose privileged configuration is a real
// file with a key in it, which is what a rotation rewrites.
func aFeedKeyOnDisk(t *testing.T) (*env, string) {
	t.Helper()

	e := testEnv(t, &fakeRunner{})
	dir := t.TempDir()
	path := filepath.Join(dir, "tun-manager.yaml")
	body := "wg_quick: /usr/bin/wg-quick\nfeed_key: \"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Path = path
	priv.FeedKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	return e, path
}

func TestFeedKeyRejectsAnUnknownFlag(t *testing.T) {
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"feed-key", "--replace"}); err == nil {
		t.Fatal("feed-key accepted --replace")
	}
}

func TestTheFeedIsGivenTheKeyItPublishesUnder(t *testing.T) {
	// It was not, for a commit: the wiring went in as a text substitution that
	// matched nothing, and the only symptom was the window saying "none" while
	// `feed-key` printed a fingerprint. Nothing here knew the difference.
	e := demoEnv(t, &fakeRunner{})
	if _, err := e.parseFlags([]string{"--feed-socket", filepath.Join(shortSocketDir(t), "f.sock")}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Feed = true
	priv.FeedKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, served, _ := e.startFeed(ctx, a, priv, privdrop.User{})

	if f == nil {
		t.Fatal("the feed did not start")
	}
	if f.FeedKey != priv.FeedKey.Reveal() {
		t.Errorf("the feed was given %q, want the configured key", f.FeedKey)
	}
	cancel()
	<-served
}

func TestASimulatedRunPublishesUnderAKeyOfItsOwn(t *testing.T) {
	// It cannot read the privileged file, and the feed will not publish without
	// a key. A fresh one per run rather than a flag: a seed on a command line
	// is a seed in `ps`, and a demo's key is worth exactly one demo.
	e := demoEnv(t, &fakeRunner{})
	if _, err := e.parseFlags([]string{"--feed-socket", filepath.Join(shortSocketDir(t), "f.sock")}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Feed = true
	priv.FeedKey = ""
	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, served, problems := e.startFeed(ctx, a, priv, privdrop.User{})

	if f == nil {
		t.Fatalf("the demo has no feed: %v", problems)
	}
	if f.FeedKey == "" {
		t.Error("the demo's feed has no key, so nothing can verify it")
	}
	cancel()
	<-served
}

func TestARealRunWithNoKeySaysSoRatherThanInventingOne(t *testing.T) {
	// The other half of the rule above. A publisher that made itself a key on
	// the spot would be pinned as itself and then be a different publisher
	// after every restart, which reads exactly like somebody standing in for
	// it.
	e := testEnv(t, &fakeRunner{}) // euid 0, not simulating
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Feed = true
	priv.FeedKey = ""
	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, _, problems := e.startFeed(ctx, a, priv, privdrop.User{})

	if f != nil {
		t.Fatal("the feed started with no key")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "feed-key --rotate") {
		t.Errorf("the interface was told %q, want the command that writes a key", problems)
	}
}

func TestADemoWithNoKeyItCanDrawHasNoFeed(t *testing.T) {
	// crypto/rand does not fail on darwin. What this branch decides is whether
	// the demo publishes at all, which is not something to leave unread.
	e := demoEnv(t, &fakeRunner{})
	if _, err := e.parseFlags([]string{"--feed-socket", filepath.Join(shortSocketDir(t), "f.sock")}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	previous := newSeed
	newSeed = func() (string, error) { return "", errors.New("no randomness today") }
	t.Cleanup(func() { newSeed = previous })
	priv, err := e.privileged()
	if err != nil {
		t.Fatalf("privileged: %v", err)
	}
	priv.Feed = true
	priv.FeedKey = ""
	a, err := e.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	f, _, problems := e.startFeed(context.Background(), a, priv, privdrop.User{})

	if f != nil {
		t.Fatal("the demo published without a key")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "status feed unavailable") {
		t.Errorf("the interface was told %q, want the reason", problems)
	}
}
