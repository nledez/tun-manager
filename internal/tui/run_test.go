package tui

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

// Invented keys and addresses; the addresses come from the ranges reserved for
// documentation (RFC 5737).
const alphaKey = "JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g="
const bravoKey = "SldkcX6LmKWyv8zZ5vMADRonNEFOW2h1go+cqbbD0N0="

type fakeReader struct {
	state wg.State
	reads atomic.Int32
}

func (r *fakeReader) Read() (wg.State, error) {
	r.reads.Add(1)
	return r.state, nil
}

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

// blindLocator declines to answer, so tunnels are matched by public key.
type blindLocator struct{}

func (blindLocator) Device(string) (string, bool) { return "", false }

type deadPinger struct{}

func (deadPinger) Ping(context.Context, string) (time.Duration, error) {
	return 0, context.DeadlineExceeded
}

func testApp(t *testing.T, runner wg.Runner, live ...string) *app.App {
	t.Helper()

	dir := t.TempDir()
	confs := map[string]string{
		"alpha": "[Peer]\nPublicKey = " + alphaKey + "\nEndpoint = 192.0.2.10:51820\nAllowedIPs = 10.20.30.0/24\n",
		"bravo": "[Peer]\nPublicKey = " + bravoKey + "\nEndpoint = 198.51.100.20:51821\nAllowedIPs = 10.20.31.1/32\n",
	}
	for name, body := range confs {
		if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	var state wg.State
	for _, k := range live {
		state = append(state, wg.Peer{PublicKey: k, Device: "utun7", LastHandshake: time.Now(), RxBytes: 2048})
	}

	cfg := profile.Default()
	cfg.ConfigDir = dir
	cfg.RefreshInterval = time.Hour // no background tick during the test
	cfg.Groups = map[string][]string{
		profile.GroupNeeded: {"alpha", "bravo"},
		profile.GroupExtra:  {},
		profile.GroupAll:    {"alpha", "bravo"},
	}
	cfg.Overrides = nil

	return &app.App{
		Config: cfg,
		Reader: &fakeReader{state: state},
		Pinger: deadPinger{},
		Lister: func() ([]netctx.Iface, error) {
			return []netctx.Iface{{Name: "en0", Addrs: []netip.Prefix{netip.MustParsePrefix("203.0.113.9/24")}}}, nil
		},
		Locator: blindLocator{},
		Control: &wg.Controller{WgQuick: "/bin/true", Runner: runner},
	}
}

// waitFor blocks until every substring has appeared in the program output.
//
// tm.Output() is a stream that each call drains, and the program only writes a
// frame when something changed. So one call must cover everything expected from
// a single state, and a second call only makes sense after a new key was sent.
func waitFor(t *testing.T, tm *teatest.TestModel, substrings ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, s := range substrings {
			if !bytes.Contains(b, []byte(s)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(50*time.Millisecond))
}

func TestProgramRendersTheTunnelTable(t *testing.T) {
	a := testApp(t, &fakeRunner{}, alphaKey)
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))

	waitFor(t, tm, "alpha", "bravo")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestProgramShowsTheLiveStateOfEachTunnel(t *testing.T) {
	a := testApp(t, &fakeRunner{}, alphaKey)
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))

	waitFor(t, tm, "● up", "○ down")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestPressingSStopsEverythingWhenATunnelIsUp(t *testing.T) {
	runner := &fakeRunner{}
	a := testApp(t, runner, alphaKey)
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))
	waitFor(t, tm, "alpha")

	// The log pane is where operation outcomes are shown; open it first.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	waitFor(t, tm, "down alpha: ok")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	if got := runner.actions(); len(got) != 1 || got[0] != "down alpha" {
		t.Errorf("commands = %v, want [down alpha]", got)
	}
}

func TestPressingSStartsTheNeededGroupWhenEverythingIsDown(t *testing.T) {
	runner := &fakeRunner{}
	a := testApp(t, runner)
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))
	waitFor(t, tm, "alpha")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	waitFor(t, tm, "up bravo: ok")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	if got := runner.actions(); strings.Join(got, ",") != "up alpha,up bravo" {
		t.Errorf("commands = %v, want [up alpha up bravo]", got)
	}
}

func TestPressingPFillsThePingColumn(t *testing.T) {
	a := testApp(t, &fakeRunner{}, alphaKey)
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))
	waitFor(t, tm, "alpha")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	// Every probe fails here, which the table shows as a cross.
	waitFor(t, tm, "×")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestPressingEnterTogglesTheRowUnderTheCursor(t *testing.T) {
	runner := &fakeRunner{}
	a := testApp(t, runner, alphaKey)
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))
	waitFor(t, tm, "alpha")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "down alpha: ok")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	if got := runner.actions(); len(got) != 1 || got[0] != "down alpha" {
		t.Errorf("commands = %v, want [down alpha]", got)
	}
}

func TestRunReturnsWhenTheContextIsCancelled(t *testing.T) {
	a := testApp(t, &fakeRunner{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, a, nil, nil, WithoutTerminal())
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestRunWithAFeedWiresPublishingAndRequests(t *testing.T) {
	// A live *feed.Server assigned through the concrete type must not panic
	// when a view is published to it, and Run must listen on its Requests
	// channel rather than leaving it nil - the wiring the composition root
	// relies on.
	a := testApp(t, &fakeRunner{}, alphaKey)
	f := &feed.Server{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, a, nil, f, WithoutTerminal())
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

var _ = probe.Result{}

func TestTheTableRefreshesOnItsOwn(t *testing.T) {
	// The periodic refresh is what makes the table trustworthy without pressing
	// anything. Nothing else exercises the tick.
	a := testApp(t, &fakeRunner{}, alphaKey)
	a.Config.RefreshInterval = 20 * time.Millisecond
	reader := a.Reader.(*fakeReader)

	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))
	waitFor(t, tm, "alpha")

	deadline := time.Now().Add(5 * time.Second)
	for reader.reads.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	if got := reader.reads.Load(); got < 3 {
		t.Errorf("the state was read %d time(s), want the tick to keep refreshing", got)
	}
}

func TestATunnelWhoseConfigVanishedFailsWhenItRuns(t *testing.T) {
	// The plan is made from the table, so a row that is there can always be
	// planned. What can still go wrong is the work: wg-quick meeting a file
	// that is no longer where the table says it is.
	runner := failingRunner{err: errors.New("exit status 1"), output: "wg-quick: `/gone.conf' does not exist"}
	a := testApp(t, runner)
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))
	waitFor(t, tm, "alpha")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "does not exist")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// failingRunner refuses every command, like a wg-quick meeting a file that is
// not there.
type failingRunner struct {
	err    error
	output string
}

func (r failingRunner) Run(context.Context, string, ...string) (string, error) {
	return r.output, r.err
}

func TestRunReportsAFailureThatIsNotACancellation(t *testing.T) {
	// Quitting and being interrupted are not failures. Anything else is, and a
	// program that crashed must not exit zero: an application with no state
	// reader makes the very first refresh panic inside its command.
	a := testApp(t, &fakeRunner{})
	a.Reader = nil

	err := Run(context.Background(), a, nil, nil, WithoutTerminal())

	if err == nil {
		t.Fatal("Run returned nil after the program crashed, want the failure reported")
	}
}

// gatedRunner blocks on every command until the test releases it, so a batch
// can be observed while it is still half done.
type gatedRunner struct {
	mu      sync.Mutex
	calls   []string
	started chan string
	release chan struct{}
}

func newGatedRunner() *gatedRunner {
	return &gatedRunner{started: make(chan string, 8), release: make(chan struct{})}
}

func (r *gatedRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	name := args[0] + " " + strings.TrimSuffix(filepath.Base(args[1]), ".conf")
	r.mu.Lock()
	r.calls = append(r.calls, name)
	r.mu.Unlock()
	r.started <- name
	<-r.release
	return "", nil
}

func TestABatchReportsEachTunnelWhileTheNextIsStillRunning(t *testing.T) {
	// One message at the end of the whole batch means no repaint while it runs:
	// wg-quick is serialised, so eight tunnels leave the screen frozen for as
	// many seconds with no way to tell it apart from a crash.
	runner := newGatedRunner()
	a := testApp(t, runner, alphaKey, bravoKey) // both up, so `s` takes both down
	tm := teatest.NewTestModel(t, New(a, nil), teatest.WithInitialTermSize(120, 30))
	waitFor(t, tm, "alpha")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

	// Let the first tunnel finish, and leave the second blocked.
	<-runner.started
	runner.release <- struct{}{}

	// The first outcome has to be on screen while the second is still running.
	waitFor(t, tm, "down alpha: ok")

	<-runner.started
	close(runner.release)
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
