package wg

import (
	"context"
	"os/exec"
	"sync"
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
// Operations are serialised: wg-quick rewrites the routing table and the DNS
// configuration, and two concurrent runs leave the system inconsistent.
type Controller struct {
	WgQuick string
	Runner  Runner
	// Pinger is optional. When set, Up skips tunnels whose check address
	// already answers.
	Pinger Pinger

	mu sync.Mutex
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
	c.mu.Lock()
	defer c.mu.Unlock()

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
