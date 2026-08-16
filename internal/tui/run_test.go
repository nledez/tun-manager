package tui

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

// Invented keys and addresses; the addresses come from the ranges reserved for
// documentation (RFC 5737).
const alphaKey = "JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g="
const bravoKey = "SldkcX6LmKWyv8zZ5vMADRonNEFOW2h1go+cqbbD0N0="

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
		Reader: fakeReader{state: state},
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
		done <- Run(ctx, a, nil, WithoutTerminal())
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

var _ = probe.Result{}
