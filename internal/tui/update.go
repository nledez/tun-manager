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
		return m, tea.Batch(m.refresh(), m.tick())

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
	m.selected = map[string]bool{}
	m.busy = true
	return m, m.refresh()
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Quitting must work even mid-operation; everything else waits.
	if key == "q" || key == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	if m.busy {
		return m, nil
	}

	switch key {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)

	case "down", "j":
		m.cursor = min(len(m.view.Rows)-1, m.cursor+1)

	case " ":
		if row, ok := m.current(); ok {
			if m.selected[row.Tunnel.Name] {
				delete(m.selected, row.Tunnel.Name)
			} else {
				m.selected[row.Tunnel.Name] = true
			}
		}

	case "l":
		m.showLogs = !m.showLogs

	case "?":
		m.showHelp = !m.showHelp

	case "r":
		m.busy = true
		return m, m.refresh()

	case "p":
		m.pinging = true
		return m, m.ping()

	case "enter":
		return m.startOp(m.toggleTargets())

	case "s":
		return m.startOp(m.stopStart())

	case "n":
		return m.startOp(m.upGroup(profile.GroupNeeded))
	}
	return m, nil
}

func (m Model) startOp(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if cmd == nil {
		return m, nil
	}
	m.busy = true
	return m, cmd
}

// toggleTargets brings the selected tunnels (or the cursor row) to the opposite
// of their current state.
func (m Model) toggleTargets() tea.Cmd {
	names := m.targets()
	if len(names) == 0 {
		return nil
	}
	return m.operate(func(ctx context.Context, a *app.App) ([]wg.Result, error) {
		var results []wg.Result
		for _, name := range names {
			res, err := a.Toggle(ctx, name)
			if err != nil {
				return results, err
			}
			results = append(results, res)
		}
		return results, nil
	})
}

// stopStart is the `s` key: tear everything down, or start what is needed.
func (m Model) stopStart() tea.Cmd {
	if m.stopStartAction() == "down" {
		return m.operate(func(ctx context.Context, a *app.App) ([]wg.Result, error) {
			return a.DownAll(ctx)
		})
	}
	return m.upGroup(profile.GroupNeeded)
}

func (m Model) upGroup(group string) tea.Cmd {
	return m.operate(func(ctx context.Context, a *app.App) ([]wg.Result, error) {
		return a.UpGroup(ctx, group)
	})
}
