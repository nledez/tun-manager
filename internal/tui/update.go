package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/notify"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

// Update advances the state machine. It performs no I/O: everything that talks
// to the system is returned as a tea.Cmd.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		if m.busy {
			return m, m.tick()
		}
		m.busy = true
		return m, tea.Batch(m.refresh(), m.tick(), m.heartbeat())

	case viewMsg:
		return m.onView(msg)

	case pingMsg:
		m.pinging = false
		for host, res := range msg.results {
			m.pings[host] = res
		}
		return m, nil

	case opMsg:
		return m.onOp(msg)

	case opDoneMsg:
		return m.onOpDone()

	case heartbeatMsg:
		// Only while there is something to report on; a timer firing on an idle
		// interface wakes the process for nothing.
		if !m.busy && !m.pinging {
			return m, nil
		}
		m.beat++
		return m, m.heartbeat()

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onView(msg viewMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	m.err = msg.err
	if msg.err != nil {
		m.log("refresh failed: "+msg.err.Error(), true)
		return m, nil
	}

	var cmd tea.Cmd
	if m.notifier != nil && m.lastHealth != nil {
		transitions := notify.Diff(m.lastHealth, msg.view.Health())
		if len(transitions) > 0 {
			n := m.notifier
			cmd = func() tea.Msg {
				n.Notify(context.Background(), transitions)
				return nil
			}
		}
	}

	m.view = msg.view
	m.lastHealth = msg.view.Health()
	m.lastRefresh = msg.view.Taken
	if m.cursor >= len(m.view.Rows) {
		m.cursor = max(0, len(m.view.Rows)-1)
	}
	return m, cmd
}

// onOp records one step of a batch. Returning without a command is enough:
// Bubble Tea repaints on the message itself, which is the point of reporting
// per step rather than per batch.
func (m Model) onOp(msg opMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.log(msg.err.Error(), true)
		m.showLogs = true
	}
	for _, r := range msg.results {
		text, isFail := describe(r)
		m.log(text, isFail)
		if isFail {
			m.showLogs = true
		}
	}
	m.opDone++
	return m, nil
}

// onOpDone closes a batch: one refresh for the whole of it rather than one per
// tunnel, and the selection is spent.
func (m Model) onOpDone() (tea.Model, tea.Cmd) {
	m.selected = map[string]bool{}
	m.opTotal, m.opDone = 0, 0
	m.busy = true
	return m, m.refresh()
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Quitting must work even mid-operation.
	if key == "q" || key == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	// Reading the table, opening the log pane and moving the cursor change
	// nothing on the machine, so they keep working while wg-quick runs. A key
	// that does nothing is indistinguishable from a hung interface.
	switch key {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
		return m, nil

	case "down", "j":
		m.cursor = min(len(m.view.Rows)-1, m.cursor+1)
		return m, nil

	case " ":
		if row, ok := m.current(); ok {
			if m.selected[row.Tunnel.Name] {
				delete(m.selected, row.Tunnel.Name)
			} else {
				m.selected[row.Tunnel.Name] = true
			}
		}
		return m, nil

	case "l":
		m.showLogs = !m.showLogs
		return m, nil

	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	}

	// Anything below starts work, and two batches of wg-quick at once is how a
	// routing table gets corrupted.
	if m.busy {
		return m, nil
	}

	switch key {
	case "r":
		m.busy = true
		return m, tea.Batch(m.refresh(), m.heartbeat())

	case "p":
		m.pinging = true
		return m, tea.Batch(m.ping(), m.heartbeat())

	case "enter":
		return m.startOp(m.toggleTargets())

	case "s":
		return m.startOp(m.stopStart())

	case "n":
		return m.startOp(m.upGroup(profile.GroupNeeded))
	}
	return m, nil
}

// startOp runs a batch one step at a time, in order, with a closing message.
// tea.Sequence dispatches each step's message before starting the next, which
// is what puts a repaint between tunnels.
func (m Model) startOp(steps []tea.Cmd) (tea.Model, tea.Cmd) {
	if len(steps) == 0 {
		return m, nil
	}
	m.busy = true
	m.opTotal, m.opDone = len(steps), 0
	return m, tea.Batch(
		tea.Sequence(append(steps, func() tea.Msg { return opDoneMsg{} })...),
		m.heartbeat(),
	)
}

// toggleTargets brings the selected tunnels (or the cursor row) to the opposite
// of their current state.
func (m Model) toggleTargets() []tea.Cmd {
	return m.eachTunnel(m.targets(), func(ctx context.Context, a *app.App, name string) ([]wg.Result, error) {
		res, err := a.Toggle(ctx, name)
		if err != nil {
			return nil, err
		}
		return []wg.Result{res}, nil
	})
}

// stopStart is the `s` key: tear everything down, or start what is needed.
func (m Model) stopStart() []tea.Cmd {
	if m.stopStartAction() == "down" {
		view := m.view
		return m.eachTunnel(m.groupMembers(profile.GroupAll),
			func(ctx context.Context, a *app.App, name string) ([]wg.Result, error) {
				return a.Down(ctx, view, []string{name})
			})
	}
	return m.upGroup(profile.GroupNeeded)
}

func (m Model) upGroup(group string) []tea.Cmd {
	view := m.view
	return m.eachTunnel(m.groupMembers(group),
		func(ctx context.Context, a *app.App, name string) ([]wg.Result, error) {
			return a.Up(ctx, view, []string{name})
		})
}
