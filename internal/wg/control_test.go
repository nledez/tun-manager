package wg

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/wgconf"
)

type call struct {
	name string
	args []string
}

// recordRunner captures the exact argv it was handed.
type recordRunner struct {
	mu     sync.Mutex
	calls  []call
	output string
	err    error
}

func (r *recordRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, call{name: name, args: args})
	r.mu.Unlock()
	return r.output, r.err
}

func (r *recordRunner) argv(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.calls[i]
	return strings.Join(append([]string{c.name}, c.args...), " ")
}

// stubPinger answers every ping the same way.
type stubPinger struct {
	rtt  time.Duration
	err  error
	hits atomic.Int32
}

func (p *stubPinger) Ping(context.Context, string) (time.Duration, error) {
	p.hits.Add(1)
	return p.rtt, p.err
}

var alpha = wgconf.Tunnel{
	Name:    "alpha",
	Path:    "/etc/wireguard/alpha.conf",
	CheckIP: "10.20.30.1",
}

func TestUpRunsWgQuickWithTheConfigPath(t *testing.T) {
	runner := &recordRunner{}
	c := &Controller{WgQuick: "/usr/bin/wg-quick", Runner: runner}

	res := c.Up(context.Background(), alpha)

	if res.Err != nil {
		t.Fatalf("Up: %v", res.Err)
	}
	want := "/usr/bin/wg-quick up /etc/wireguard/alpha.conf"
	if got := runner.argv(0); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestDownRunsWgQuickDown(t *testing.T) {
	runner := &recordRunner{}
	c := &Controller{WgQuick: "/usr/bin/wg-quick", Runner: runner}

	res := c.Down(context.Background(), alpha)

	if res.Err != nil {
		t.Fatalf("Down: %v", res.Err)
	}
	want := "/usr/bin/wg-quick down /etc/wireguard/alpha.conf"
	if got := runner.argv(0); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestUpSkipsWhenCheckIPAlreadyAnswers(t *testing.T) {
	// A reachable check address means the tunnel (or a plain LAN route to it)
	// already works, so there is nothing to bring up.
	runner := &recordRunner{}
	c := &Controller{WgQuick: "wg-quick", Runner: runner, Pinger: &stubPinger{rtt: 3 * time.Millisecond}}

	res := c.Up(context.Background(), alpha)

	if !res.Skipped {
		t.Error("Skipped = false, want true when the check address answers")
	}
	if len(runner.calls) != 0 {
		t.Errorf("wg-quick was run %d time(s), want 0", len(runner.calls))
	}
}

func TestUpProceedsWhenCheckIPIsUnreachable(t *testing.T) {
	runner := &recordRunner{}
	c := &Controller{WgQuick: "wg-quick", Runner: runner, Pinger: &stubPinger{err: errors.New("timeout")}}

	res := c.Up(context.Background(), alpha)

	if res.Skipped {
		t.Error("Skipped = true, want false when the check address is unreachable")
	}
	if len(runner.calls) != 1 {
		t.Errorf("wg-quick was run %d time(s), want 1", len(runner.calls))
	}
}

func TestUpDoesNotPreCheckWithoutACheckIP(t *testing.T) {
	runner := &recordRunner{}
	pinger := &stubPinger{rtt: time.Millisecond}
	c := &Controller{WgQuick: "wg-quick", Runner: runner, Pinger: pinger}

	c.Up(context.Background(), wgconf.Tunnel{Name: "x", Path: "/tmp/x.conf"})

	if pinger.hits.Load() != 0 {
		t.Errorf("pinger called %d time(s), want 0 when the tunnel has no check address", pinger.hits.Load())
	}
	if len(runner.calls) != 1 {
		t.Errorf("wg-quick was run %d time(s), want 1", len(runner.calls))
	}
}

func TestDownNeverPreChecks(t *testing.T) {
	// A reachable check address must not stop a shutdown.
	runner := &recordRunner{}
	pinger := &stubPinger{rtt: time.Millisecond}
	c := &Controller{WgQuick: "wg-quick", Runner: runner, Pinger: pinger}

	res := c.Down(context.Background(), alpha)

	if res.Skipped {
		t.Error("Skipped = true, want false: down must always run")
	}
	if pinger.hits.Load() != 0 {
		t.Errorf("pinger called %d time(s), want 0", pinger.hits.Load())
	}
}

func TestResultCarriesTheCommandOutputOnFailure(t *testing.T) {
	runner := &recordRunner{output: "wg-quick: `utun7' is not a WireGuard interface", err: errors.New("exit status 1")}
	c := &Controller{WgQuick: "wg-quick", Runner: runner}

	res := c.Down(context.Background(), alpha)

	if res.Err == nil {
		t.Fatal("Err = nil, want the runner error")
	}
	if !strings.Contains(res.Output, "not a WireGuard interface") {
		t.Errorf("Output = %q, want the command output preserved", res.Output)
	}
	if res.Tunnel != "alpha" || res.Action != "down" {
		t.Errorf("Result identity = %q/%q, want alpha/down", res.Tunnel, res.Action)
	}
}

func TestOperationsMayOverlap(t *testing.T) {
	// The controller no longer decides how many wg-quick runs coexist: only the
	// caller knows whether it is walking a list for the command line or
	// spreading a batch across an interface. app.RunBatch makes that call.
	var live, peak atomic.Int32
	runner := runnerFunc(func(context.Context, string, ...string) (string, error) {
		n := live.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		live.Add(-1)
		return "", nil
	})
	c := &Controller{WgQuick: "wg-quick", Runner: runner}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Up(context.Background(), wgconf.Tunnel{Name: "t", Path: "/tmp/t.conf"})
		}()
	}
	wg.Wait()

	if peak.Load() < 2 {
		t.Errorf("peak concurrency = %d, want the controller to let them overlap", peak.Load())
	}
}

type runnerFunc func(context.Context, string, ...string) (string, error)

func (f runnerFunc) Run(ctx context.Context, name string, args ...string) (string, error) {
	return f(ctx, name, args...)
}

func TestExecRunnerReturnsTheCombinedOutput(t *testing.T) {
	out, err := (ExecRunner{}).Run(context.Background(), "/bin/echo", "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.TrimSpace(out) != "hello" {
		t.Errorf("out = %q, want %q", out, "hello")
	}
}

func TestExecRunnerReportsAFailingCommand(t *testing.T) {
	// wg-quick exits non-zero and explains itself on stderr, so both the error
	// and the output have to survive.
	out, err := (ExecRunner{}).Run(context.Background(), "/bin/sh", "-c", "echo boom >&2; exit 3")

	if err == nil {
		t.Fatal("Run returned nil for a failing command, want an error")
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("out = %q, want stderr captured", out)
	}
}

func TestExecRunnerFailsOnAMissingBinary(t *testing.T) {
	if _, err := (ExecRunner{}).Run(context.Background(), "/nonexistent/wg-quick"); err == nil {
		t.Fatal("Run succeeded with a missing binary, want an error")
	}
}

func TestExecRunnerHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (ExecRunner{}).Run(ctx, "/bin/sleep", "5"); err == nil {
		t.Fatal("Run succeeded with a cancelled context, want an error")
	}
}
