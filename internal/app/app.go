// Package app joins the pieces: static configs, live WireGuard state, network
// context and groups. Both the CLI and the TUI drive tunnels through it.
package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

// Row is one tunnel, seen from every angle at once.
type Row struct {
	Tunnel wgconf.Tunnel
	// Group is the group the tunnel belongs to in the current network context.
	Group  string
	Health wg.Health
	// Peer is the live state; its zero value means the tunnel is down.
	Peer wg.Peer
}

// View is a complete picture of the system at one instant.
type View struct {
	Context netctx.Context
	Rows    []Row
	Taken   time.Time
	// Ignored names the .conf files that were left alone, one sentence each.
	// Carried on the view rather than logged where it was noticed, because
	// nothing down there knows whether anybody is looking - and a file silently
	// missing from the table is the one outcome worth avoiding.
	Ignored []string
}

// AnyUp reports whether at least one tunnel is live. It drives the meaning of
// the TUI's `s` key: stop everything, or start what is needed.
func (v View) AnyUp() bool {
	for _, r := range v.Rows {
		if r.Health != wg.Down {
			return true
		}
	}
	return false
}

// Health maps tunnel names to their health, for notification diffing.
func (v View) Health() map[string]wg.Health {
	out := make(map[string]wg.Health, len(v.Rows))
	for _, r := range v.Rows {
		out[r.Tunnel.Name] = r.Health
	}
	return out
}

// Row returns the row of a tunnel by name.
func (v View) Row(name string) (Row, bool) {
	for _, r := range v.Rows {
		if r.Tunnel.Name == name {
			return r, true
		}
	}
	return Row{}, false
}

// CheckIPs lists the probe targets of the live tunnels, for the ping key.
//
// Down tunnels are left out on purpose: several configs may share a check
// address, and probing it while one of them is up would report a latency for
// all of them.
func (v View) CheckIPs() []string {
	out := make([]string, 0, len(v.Rows))
	for _, r := range v.Rows {
		if r.Health != wg.Down && r.Tunnel.CheckIP != "" {
			out = append(out, r.Tunnel.CheckIP)
		}
	}
	return out
}

// App is the application core.
type App struct {
	Config  *profile.Config
	Reader  wg.Reader
	Control *wg.Controller
	Lister  netctx.Lister
	Pinger  probe.Pinger
	// Locator maps a tunnel name to its interface. Without it, tunnels sharing
	// a peer public key cannot be told apart.
	Locator wg.Locator
	// Now is injectable so health thresholds stay testable.
	Now func() time.Time
	// Stagger is how long a batch waits between launching one tunnel and the
	// next. Zero means DefaultStagger.
	Stagger time.Duration
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) lister() netctx.Lister {
	if a.Lister != nil {
		return a.Lister
	}
	return netctx.System
}

func (a *App) locator() wg.Locator {
	if a.Locator != nil {
		return a.Locator
	}
	return wg.RunDirLocator{Dir: wg.DefaultRunDir}
}

// View reads the configs, the live state and the network context, and merges
// them into rows ordered needed-first.
func (a *App) View() (View, error) {
	tunnels, ignored, err := wgconf.LoadDir(a.Config.ConfigDir)
	if err != nil {
		return View{}, err
	}
	state, err := a.Reader.Read()
	if err != nil {
		return View{}, err
	}
	ctx, err := netctx.Detect(a.Config.Contexts, a.lister())
	if err != nil {
		return View{}, err
	}

	now := a.now()
	rows := make([]Row, 0, len(tunnels))
	for _, tun := range tunnels {
		row := Row{
			Tunnel: tun,
			Group:  a.Config.GroupOf(tun.Name, ctx.Name),
			Health: wg.Down,
		}
		if peer, ok := state.Resolve(tun.Name, tun.PeerPublicKey, a.locator()); ok {
			row.Peer = peer
			row.Health = peer.Health(now, wg.StaleAfter)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := groupRank(rows[i].Group), groupRank(rows[j].Group)
		if ri != rj {
			return ri < rj
		}
		return rows[i].Tunnel.Name < rows[j].Tunnel.Name
	})

	return View{Context: ctx, Rows: rows, Taken: now, Ignored: ignored}, nil
}

// groupRank keeps the always-on tunnels at the top of the table.
func groupRank(group string) int {
	switch group {
	case profile.GroupNeeded:
		return 0
	case profile.GroupExtra:
		return 1
	default:
		return 2
	}
}

// UpGroup starts every tunnel of a group that is not already up.
func (a *App) UpGroup(ctx context.Context, group string) ([]wg.Result, error) {
	view, err := a.View()
	if err != nil {
		return nil, err
	}
	return a.Up(ctx, view, a.Config.Members(group, view.Context.Name))
}

// Up starts the named tunnels that are currently down. A name the table does
// not know is refused: it was typed, and a typo silently doing nothing is worse
// than an error.
func (a *App) Up(ctx context.Context, view View, names []string) ([]wg.Result, error) {
	if err := refuseUnknown(view, names); err != nil {
		return nil, err
	}
	return a.runInOrder(ctx, view.PlanUp(names)), nil
}

func refuseUnknown(view View, names []string) error {
	if unknown := view.Unknown(names); len(unknown) > 0 {
		return fmt.Errorf("unknown tunnel %q", unknown[0])
	}
	return nil
}

// runInOrder walks a plan one step at a time. The command line has nothing to
// display while it waits, so there is nothing to gain from overlapping and one
// less thing to reason about when a step fails.
func (a *App) runInOrder(ctx context.Context, steps []Step) []wg.Result {
	var results []wg.Result
	for _, s := range steps {
		results = append(results, a.Do(ctx, s))
	}
	return results
}

// DownAll stops every live tunnel of the "all" group, in declaration order. A
// name with no configuration is skipped rather than refused: the group is a
// list of things to tear down, and one that is not there is already down.
func (a *App) DownAll(ctx context.Context) ([]wg.Result, error) {
	view, err := a.View()
	if err != nil {
		return nil, err
	}

	names := a.Config.Members(profile.GroupAll, view.Context.Name)
	return a.runInOrder(ctx, view.PlanDown(names)), nil
}

// Down stops the named tunnels that are currently live.
func (a *App) Down(ctx context.Context, view View, names []string) ([]wg.Result, error) {
	if err := refuseUnknown(view, names); err != nil {
		return nil, err
	}
	return a.runInOrder(ctx, view.PlanDown(names)), nil
}

// Toggle stops a live tunnel, or starts a dead one.
func (a *App) Toggle(ctx context.Context, name string) (wg.Result, error) {
	view, err := a.View()
	if err != nil {
		return wg.Result{}, err
	}
	if err := refuseUnknown(view, []string{name}); err != nil {
		return wg.Result{}, err
	}
	return a.Do(ctx, view.PlanToggle([]string{name})[0]), nil
}
