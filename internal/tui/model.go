// Package tui is the interactive front end.
//
// The model is a plain state machine: every side effect happens in a tea.Cmd,
// so Update stays a pure function of (state, message) and is directly testable.
package tui

import (
	"context"
	"fmt"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/notify"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

// maxLogs bounds the log pane: it is a tail, not an archive.
const maxLogs = 200

// keyPattern matches a WireGuard base64 key, so that a config line echoed by
// wg-quick never reaches the log pane.
var keyPattern = regexp.MustCompile(`[A-Za-z0-9+/]{42}[A-Za-z0-9+/=]{2}`)

// LogEntry is one line of the log pane.
type LogEntry struct {
	At     time.Time
	Text   string
	IsFail bool
}

// Messages produced by the commands.
type (
	// viewMsg carries a fresh reading of the system.
	viewMsg struct {
		view app.View
		err  error
	}
	// pingMsg carries the outcome of a probe round.
	pingMsg struct {
		results map[string]probe.Result
	}
	// opMsg carries the outcome of a batch of up/down operations.
	opMsg struct {
		results []wg.Result
		err     error
	}
	// tickMsg fires the periodic refresh.
	tickMsg time.Time
)

// Model is the TUI state.
type Model struct {
	app      *app.App
	notifier *notify.Notifier

	view     app.View
	pings    map[string]probe.Result
	selected map[string]bool
	logs     []LogEntry

	cursor   int
	width    int
	height   int
	showLogs bool
	showHelp bool
	busy     bool
	pinging  bool
	quitting bool
	err      error

	lastHealth  map[string]wg.Health
	lastRefresh time.Time
}

// New builds the initial model.
func New(a *app.App, n *notify.Notifier) Model {
	return Model{
		app:      a,
		notifier: n,
		pings:    map[string]probe.Result{},
		selected: map[string]bool{},
	}
}

// Init triggers the first refresh and starts the periodic tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), m.tick())
}

func (m Model) refreshInterval() time.Duration {
	if m.app == nil || m.app.Config == nil {
		return profile.DefaultRefresh
	}
	return m.app.Config.RefreshInterval
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.refreshInterval(), func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) refresh() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if a == nil {
			return viewMsg{}
		}
		view, err := a.View()
		return viewMsg{view: view, err: err}
	}
}

func (m Model) ping() tea.Cmd {
	a, hosts := m.app, m.view.CheckIPs()
	return func() tea.Msg {
		if a == nil {
			return pingMsg{results: map[string]probe.Result{}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return pingMsg{results: probe.All(ctx, a.Pinger, hosts)}
	}
}

// operate runs a batch of up/down operations off the UI goroutine.
func (m Model) operate(fn func(context.Context, *app.App) ([]wg.Result, error)) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		if a == nil {
			return opMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		results, err := fn(ctx, a)
		return opMsg{results: results, err: err}
	}
}

// stopStartAction tells what the `s` key would do right now: tear everything
// down if anything is up, otherwise start the needed group.
func (m Model) stopStartAction() string {
	if m.view.AnyUp() {
		return "down"
	}
	return "up"
}

// targets returns the tunnels an action applies to: the checked rows, or the
// row under the cursor when nothing is checked.
func (m Model) targets() []string {
	var out []string
	for _, r := range m.view.Rows {
		if m.selected[r.Tunnel.Name] {
			out = append(out, r.Tunnel.Name)
		}
	}
	if len(out) > 0 {
		return out
	}
	if row, ok := m.current(); ok {
		return []string{row.Tunnel.Name}
	}
	return nil
}

func (m Model) current() (app.Row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.view.Rows) {
		return app.Row{}, false
	}
	return m.view.Rows[m.cursor], true
}

func (m *Model) log(text string, isFail bool) {
	m.logs = append(m.logs, LogEntry{At: time.Now(), Text: redact(text), IsFail: isFail})
	if len(m.logs) > maxLogs {
		m.logs = m.logs[len(m.logs)-maxLogs:]
	}
}

// redact removes anything that looks like a WireGuard key.
func redact(s string) string {
	return keyPattern.ReplaceAllString(s, "<redacted>")
}

func describe(r wg.Result) (string, bool) {
	switch {
	case r.Skipped:
		return fmt.Sprintf("%s %s: skipped, check address already reachable", r.Action, r.Tunnel), false
	case r.Err != nil:
		text := fmt.Sprintf("%s %s: FAILED: %v", r.Action, r.Tunnel, r.Err)
		if r.Output != "" {
			text += " — " + r.Output
		}
		return text, true
	default:
		return fmt.Sprintf("%s %s: ok", r.Action, r.Tunnel), false
	}
}
