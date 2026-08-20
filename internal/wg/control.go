package wg

import (
	"context"
	"os/exec"
	"time"

	"ledez.net/tun-manager/internal/wgconf"
)

// Runner executes an external command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// Pinger probes a host. Controller uses it to skip a tunnel whose check address
// already answers.
type Pinger interface {
	Ping(ctx context.Context, host string) (time.Duration, error)
}

// Result describes one completed up/down operation.
type Result struct {
	Tunnel string
	Action string
	// Skipped reports that the pre-check found the target already reachable, so
	// wg-quick was never run.
	Skipped bool
	Output  string
	Err     error
}

// Controller brings tunnels up and down through wg-quick.
//
// It runs whatever it is asked to, whenever it is asked: how many wg-quick runs
// overlap is the caller's decision, because only the caller knows whether it is
// walking a list for the command line or spreading a batch across an interface.
// app.RunBatch is where that decision lives, and it spaces launches out so two
// of them do not reach the routing table in the same instant.
type Controller struct {
	WgQuick string
	Runner  Runner
	// Pinger is optional. When set, Up skips tunnels whose check address
	// already answers.
	Pinger Pinger

	// Check verifies the binary before it is run. Zero means CheckExecutable,
	// so a Controller assembled without thinking about it is the safe one - a
	// field that has to be filled in to be secure is a field somebody forgets.
	//
	// It is a field at all because a test needs a Controller whose wg-quick is
	// a name rather than an installation: what those tests are about is the
	// argv, and a real binary in a real directory would make them depend on the
	// machine running them.
	Check func(path string) error
}

func (c *Controller) check() func(string) error {
	if c.Check != nil {
		return c.Check
	}
	return CheckExecutable
}

// Up brings a tunnel up, unless its check address already answers.
func (c *Controller) Up(ctx context.Context, tun wgconf.Tunnel) Result {
	if c.Pinger != nil && tun.CheckIP != "" {
		if _, err := c.Pinger.Ping(ctx, tun.CheckIP); err == nil {
			return Result{Tunnel: tun.Name, Action: "up", Skipped: true}
		}
	}
	return c.run(ctx, tun, "up")
}

// Down brings a tunnel down. It never pre-checks: a reachable address is not a
// reason to keep a tunnel open.
func (c *Controller) Down(ctx context.Context, tun wgconf.Tunnel) Result {
	return c.run(ctx, tun, "down")
}

func (c *Controller) run(ctx context.Context, tun wgconf.Tunnel, action string) Result {
	// Before every run rather than once for the process. A stat costs
	// microseconds against a wg-quick run that takes seconds; checking again is
	// what notices a binary replaced while the interface was open, and what
	// stops refusing once somebody has put it right without restarting.
	if err := c.check()(c.WgQuick); err != nil {
		return Result{Tunnel: tun.Name, Action: action, Err: err}
	}
	out, err := c.Runner.Run(ctx, c.WgQuick, action, tun.Path)
	return Result{Tunnel: tun.Name, Action: action, Output: out, Err: err}
}

// ExecRunner runs commands for real.
type ExecRunner struct{}

// Run executes the command and returns its combined output, which is what
// wg-quick logs its progress to.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
