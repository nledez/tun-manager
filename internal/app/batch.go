package app

import (
	"context"
	"sync"
	"time"

	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

// The two things a batch can do to a tunnel.
const (
	ActionUp   = "up"
	ActionDown = "down"
)

// DefaultStagger is how long a batch waits between launching one tunnel and the
// next. wg-quick writes to the routing table, and two of them reaching it in
// the same instant is the one thing worth avoiding; a few milliseconds is
// enough to space them out without making a batch feel sequential.
const DefaultStagger = 50 * time.Millisecond

// Step is one tunnel's worth of a batch, decided before anything runs.
//
// Deciding up front is what lets the interface say a tunnel is starting while
// it starts, rather than working out what happened from the result.
type Step struct {
	Tunnel wgconf.Tunnel
	Action string
}

// Phase says whether a step is beginning or over.
type Phase int

const (
	// Started means the step is running now.
	Started Phase = iota
	// Finished means the step is over and Result says how it went.
	Finished
)

// Event is what a batch reports as it goes. Steps run at the same time, so
// events from different tunnels interleave: each one names its own.
type Event struct {
	Phase  Phase
	Tunnel string
	Action string
	// Result is only meaningful once the phase is Finished.
	Result wg.Result
}

// PlanUp lists the steps needed to bring the named tunnels up, leaving out
// those already up.
func (v View) PlanUp(names []string) []Step {
	return v.plan(names, func(r Row) (string, bool) {
		return ActionUp, r.Health == wg.Down
	})
}

// PlanDown lists the steps needed to take the named tunnels down, leaving out
// those already down.
func (v View) PlanDown(names []string) []Step {
	return v.plan(names, func(r Row) (string, bool) {
		return ActionDown, r.Health != wg.Down
	})
}

// PlanToggle brings each named tunnel to the opposite of what it is now.
func (v View) PlanToggle(names []string) []Step {
	return v.plan(names, func(r Row) (string, bool) {
		if r.Health == wg.Down {
			return ActionUp, true
		}
		return ActionDown, true
	})
}

// Unknown lists the names this table has never heard of.
//
// Planning skips them rather than refusing, because a group naming a tunnel
// with no configuration should not stop the tunnels that do have one. Where a
// name came from a person rather than from a group, the caller asks this first
// and refuses on the spot.
func (v View) Unknown(names []string) []string {
	var out []string
	for _, name := range names {
		if _, ok := v.Row(name); !ok {
			out = append(out, name)
		}
	}
	return out
}

func (v View) plan(names []string, decide func(Row) (string, bool)) []Step {
	steps := make([]Step, 0, len(names))
	for _, name := range names {
		row, ok := v.Row(name)
		if !ok {
			continue
		}
		if action, wanted := decide(row); wanted {
			steps = append(steps, Step{Tunnel: row.Tunnel, Action: action})
		}
	}
	return steps
}

// Do runs one step.
func (a *App) Do(ctx context.Context, s Step) wg.Result {
	if s.Action == ActionUp {
		return a.Control.Up(ctx, s.Tunnel)
	}
	return a.Control.Down(ctx, s.Tunnel)
}

func (a *App) stagger() time.Duration {
	if a.Stagger > 0 {
		return a.Stagger
	}
	return DefaultStagger
}

// RunBatch runs every step at the same time and reports each one as it starts
// and as it finishes, on a channel that closes when the batch is over.
//
// At the same time, because wg-quick takes seconds per tunnel and eight of them
// one after another is eight times as long for no reason. Launches are spaced
// out so that two do not reach the routing table in the same instant.
//
// The channel is buffered for every event the batch can produce, so a caller
// that stops reading cannot wedge the work.
func (a *App) RunBatch(ctx context.Context, steps []Step) <-chan Event {
	events := make(chan Event, 2*len(steps))

	go func() {
		defer close(events)

		var running sync.WaitGroup
		for i, s := range steps {
			if i > 0 {
				select {
				case <-time.After(a.stagger()):
				case <-ctx.Done():
					// Whatever is already running still reports; nothing new
					// is launched.
					running.Wait()
					return
				}
			}

			running.Add(1)
			go func() {
				defer running.Done()
				events <- Event{Phase: Started, Tunnel: s.Tunnel.Name, Action: s.Action}
				events <- Event{
					Phase:  Finished,
					Tunnel: s.Tunnel.Name,
					Action: s.Action,
					Result: a.Do(ctx, s),
				}
			}()
		}
		running.Wait()
	}()

	return events
}
