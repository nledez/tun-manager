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
