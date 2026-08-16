package main

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/notify"
	"ledez.net/tun-manager/internal/privdrop"
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

	cfg := profile.Default()
	cfg.ConfigDir = dir
	// The defaults point at a Homebrew wg-quick and at /var/run/wireguard.
	// Depending on either would make these tests pass or fail according to what
	// happens to be installed on the machine running them.
	cfg.WgQuick = fakeExecutable(t)
	cfg.RunDir = t.TempDir()
	cfg.Groups = map[string][]string{
		profile.GroupNeeded: {"alpha", "bravo"},
		profile.GroupAll:    {"alpha", "bravo"},
	}

	return &env{
		out:  &strings.Builder{},
		euid: 0,
		config: func() (*profile.Config, privdrop.User, error) {
			return cfg, privdrop.User{Username: "operator", HomeDir: "/home/operator"}, nil
		},
		build: func() (*app.App, error) {
			return &app.App{
				Config:  cfg,
				Reader:  fakeReader{state: state},
				Locator: blindLocator{},
				Lister: func() ([]netctx.Iface, error) {
					return []netctx.Iface{{Name: "en0", Addrs: []netip.Prefix{netip.MustParsePrefix("203.0.113.9/24")}}}, nil
				},
				Control: &wg.Controller{WgQuick: "/bin/true", Runner: runner},
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
	e := testEnv(t, &fakeRunner{})

	if err := e.run([]string{"doctor"}); err != nil {
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
	e.interactive = func(context.Context, *app.App, *notify.Notifier) error {
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

func TestTUIGetsANotifierBuiltFromTheConfiguration(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	var got *notify.Notifier
	e.interactive = func(_ context.Context, _ *app.App, n *notify.Notifier) error {
		got = n
		return nil
	}

	if err := e.run([]string{"tui"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got == nil {
		t.Fatal("notifier = nil, want one built from the configuration")
	}
	if got.User.Username != "operator" {
		t.Errorf("notifier user = %q, want the pre-sudo user", got.User.Username)
	}
}

func TestTUIUsesAnInjectedNotifier(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	want := &notify.Notifier{Enabled: false}
	e.notifier = want
	var got *notify.Notifier
	e.interactive = func(_ context.Context, _ *app.App, n *notify.Notifier) error {
		got = n
		return nil
	}

	if err := e.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != want {
		t.Error("the injected notifier was replaced")
	}
}

func TestTUIFailureIsReported(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("no terminal")
	e.interactive = func(context.Context, *app.App, *notify.Notifier) error { return boom }

	if err := e.run(nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestTUIBuildFailureStopsBeforeStarting(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	e.build = func() (*app.App, error) { return nil, errors.New("no wireguard socket") }
	e.interactive = func(context.Context, *app.App, *notify.Notifier) error {
		t.Fatal("the TUI started despite a failed build")
		return nil
	}

	if err := e.run(nil); err == nil {
		t.Fatal("run succeeded with a failed build, want an error")
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

	if err := e.run([]string{"doctor"}); err != nil {
		t.Fatalf("doctor: %v\n%s", err, output(e))
	}

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

	cfg, u, err := loadConfig()
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

	if _, _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig succeeded on an unreadable configuration, want an error")
	}
}

func TestLoadConfigReportsAnUnresolvableSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "nobody-by-that-name")

	if _, _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig succeeded with an unresolvable SUDO_USER, want an error")
	}
}

func TestBuildWiresTheApplicationFromTheConfiguration(t *testing.T) {
	// build is what the injected env.build stands in for everywhere else, so
	// nothing else ever runs it.
	isolatedHome(t)

	a, err := build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if r, ok := a.Reader.(*wg.CtrlReader); ok {
			_ = r.Close()
		}
	})

	if a.Reader == nil || a.Pinger == nil || a.Control == nil {
		t.Fatalf("application incomplete: %+v", a)
	}
	if a.Control.WgQuick != a.Config.WgQuick {
		t.Errorf("controller uses %q, config says %q", a.Control.WgQuick, a.Config.WgQuick)
	}
	if got, ok := a.Locator.(wg.RunDirLocator); !ok || got.Dir != a.Config.RunDir {
		t.Errorf("locator = %+v, want it to follow run_dir %q", a.Locator, a.Config.RunDir)
	}
	// The pre-check that skips a tunnel whose address already answers only
	// happens if the controller has a pinger.
	if a.Control.Pinger == nil {
		t.Error("the controller has no pinger, so up would never skip")
	}
}

func TestBuildReportsAConfigurationFailure(t *testing.T) {
	t.Setenv("SUDO_USER", "nobody-by-that-name")

	if _, err := build(); err == nil {
		t.Fatal("build succeeded on an unreadable configuration, want an error")
	}
}

func TestRunTUIStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- runTUI(ctx, nil, nil) }()

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
			Control: &wg.Controller{WgQuick: "/bin/true", Runner: &fakeRunner{}},
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

func TestNotifyRunsWithoutRoot(t *testing.T) {
	// Notifications are about the desktop session, not about the tunnels, so
	// this must not demand a password.
	e := testEnv(t, &fakeRunner{})
	e.euid = 501
	e.build = func() (*app.App, error) { return nil, errors.New("notify must not open anything") }
	e.notifier = &notify.Notifier{Binary: "/usr/bin/true"}

	if err := e.run([]string{"notify"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
}

// fakeNotifierBinary is named after the real tool, because that name is what
// decides the argument form and whether an icon can be shown.
func fakeNotifierBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terminal-notifier")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestNotifyReportsWhatItUsed(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	e.notifier = &notify.Notifier{Binary: fakeNotifierBinary(t), Icon: "/tmp/icon.png"}

	if err := e.run([]string{"notify"}); err != nil {
		t.Fatalf("notify: %v", err)
	}

	got := output(e)
	for _, want := range []string{"terminal-notifier", "/tmp/icon.png", "posted"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestNotifySaysWhenTheCommandCannotShowAnIcon(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	e.notifier = &notify.Notifier{Binary: "/usr/bin/true", Icon: "/tmp/icon.png"}

	if err := e.run([]string{"notify"}); err != nil {
		t.Fatalf("notify: %v", err)
	}

	if !strings.Contains(output(e), "no icon") {
		t.Errorf("output does not say the command cannot show one:\n%s", output(e))
	}
	if !strings.Contains(output(e), "terminal-notifier") {
		t.Errorf("output does not say what to install:\n%s", output(e))
	}
}

func TestNotifySaysWhenTheIconCouldNotBeWritten(t *testing.T) {
	// The command can show one, but there is nothing to show: a different
	// problem from the one above, and worth telling apart.
	e := testEnv(t, &fakeRunner{})
	e.notifier = &notify.Notifier{Binary: fakeNotifierBinary(t)}

	if err := e.run([]string{"notify"}); err != nil {
		t.Fatalf("notify: %v", err)
	}

	if !strings.Contains(output(e), "no icon") {
		t.Errorf("output does not mention the missing icon:\n%s", output(e))
	}
}

func TestNotifyReportsAFailure(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	e.notifier = &notify.Notifier{Binary: filepath.Join(t.TempDir(), "absent")}

	if err := e.run([]string{"notify"}); err == nil {
		t.Fatal("notify succeeded with a missing command, want the failure reported")
	}
}

func TestNotifyBuildsItsOwnNotifierWhenNoneIsInjected(t *testing.T) {
	isolatedHome(t)
	e := testEnv(t, &fakeRunner{})
	e.euid = 501
	// Nothing is injected, so it has to resolve the user, the configuration and
	// the command by itself. The command it finds may not exist on a runner,
	// which is a failure worth reporting rather than a reason to skip.
	_ = e.run([]string{"notify"})

	if !strings.Contains(output(e), "command") {
		t.Errorf("output does not name the command it used:\n%s", output(e))
	}
}

func TestNotifyStopsWhenTheConfigurationCannotBeRead(t *testing.T) {
	e := testEnv(t, &fakeRunner{})
	boom := errors.New("unreadable config")
	e.config = func() (*profile.Config, privdrop.User, error) { return nil, privdrop.User{}, boom }

	if err := e.run([]string{"notify"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}
