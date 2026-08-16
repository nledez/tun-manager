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
		t.Fatalf("doctor: %v", err)
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
		t.Fatalf("doctor: %v", err)
	}

	if !strings.Contains(output(e), version) {
		t.Errorf("doctor does not report the version:\n%s", output(e))
	}
}
