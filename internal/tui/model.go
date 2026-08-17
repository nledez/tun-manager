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
	"ledez.net/tun-manager/internal/rate"
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
	// eventMsg carries one report from a running batch, and the batch it came
	// from so the interface knows where to listen next. Several batches can be
	// running, and their events interleave.
	eventMsg struct {
		event app.Event
		from  <-chan app.Event
	}
	// batchDoneMsg closes a batch, once its channel is drained.
	batchDoneMsg struct{}
	// heartbeatMsg advances the working indicator while something runs.
	heartbeatMsg struct{}
	// sampleMsg carries one reading of a tunnel's counters, for the graph.
	sampleMsg struct {
		tunnel string
		at     time.Time
		rx, tx int64
	}
	// sampleTickMsg says it is time to take the next reading. It is separate
	// from the reading itself: one asks, the other answers.
	sampleTickMsg struct{}
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

	cursor int
	beat   int
	// batches counts the batches still running. Acting on one tunnel does not
	// wait for another, so there can be several at once, and the last to finish
	// is the one that refreshes.
	batches int
	// refreshing is a read of the whole system in flight. Firing a second while
	// the first is out gains nothing.
	refreshing bool

	// series holds the traffic history of each tunnel the graph has watched,
	// kept while the pane is open so that moving the cursor away and back does
	// not lose what was recorded.
	series map[string]*rate.Series

	// inFlight maps a tunnel to what is happening to it right now. A map rather
	// than a single name, because a batch launches them together and several
	// are waiting at any moment.
	inFlight map[string]string
	// events is the running batch, or nil. The interface reads it; it does not
	// drive it.
	width     int
	height    int
	showLogs  bool
	showHelp  bool
	showGraph bool
	pinging   bool
	quitting  bool
	err       error

	lastHealth  map[string]wg.Health
	lastRefresh time.Time

	// now is the clock the interface reads. Two things on screen depend on it,
	// the refresh countdown and the timestamp of a log entry, and neither can
	// be pinned by a test while they read the wall clock.
	now func() time.Time
}

func (m Model) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// New builds the initial model.
func New(a *app.App, n *notify.Notifier) Model {
	return Model{
		app:      a,
		notifier: n,
		pings:    map[string]probe.Result{},
		selected: map[string]bool{},
		inFlight: map[string]string{},
		series:   map[string]*rate.Series{},
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

// heartbeatInterval is how often the working indicator advances. Fast enough to
// read as motion, slow enough not to be a busy loop.
const heartbeatInterval = 120 * time.Millisecond

// heartbeat keeps the frame changing while work runs. A single tunnel is a
// single step, so the progress count alone would sit still for as long as
// wg-quick takes, and a frame that never changes reads as a hung program.
func (m Model) heartbeat() tea.Cmd {
	return tea.Tick(heartbeatInterval, func(time.Time) tea.Msg { return heartbeatMsg{} })
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

// nextEvent turns the next report of a batch into a message, and is re-issued
// for as long as that batch keeps talking. This is the seam: the batch runs on
// its own, the interface only reads what it says.
//
// The channel travels with the message, so several batches can be listened to
// at once without the interface keeping a list of them.
func nextEvent(events <-chan app.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-events
		if !ok {
			return batchDoneMsg{}
		}
		return eventMsg{event: e, from: events}
	}
}

// sampleInterval is how often the graph reads the counters while it is open.
// One a second is what makes a rate a rate; the periodic refresh, minutes
// apart, could only ever draw an average of the whole interval.
const sampleInterval = time.Second

// sample reads the counters of the tunnel under the cursor, off the UI
// goroutine. It reads the control socket alone, which is cheap enough to do
// every second.
func (m Model) sample() tea.Cmd {
	a := m.app
	row, ok := m.current()
	return func() tea.Msg {
		if a == nil || !ok {
			return sampleMsg{}
		}
		s, taken := a.Sample(row.Tunnel)
		if !taken {
			// A tunnel that is down has no counters. Reporting the tunnel
			// anyway keeps the pane on it rather than blanking.
			return sampleMsg{tunnel: row.Tunnel.Name}
		}
		return sampleMsg{tunnel: row.Tunnel.Name, at: s.At, rx: s.Rx, tx: s.Tx}
	}
}

// sampleTick asks for the next reading, a second from now.
func (m Model) sampleTick() tea.Cmd {
	return tea.Tick(sampleInterval, func(time.Time) tea.Msg { return sampleTickMsg{} })
}

// busy reports whether anything is in flight, which is what decides that the
// screen has to keep moving.
func (m Model) busy() bool {
	return m.batches > 0 || m.refreshing
}

// groupMembers resolves a group against the network the interface last saw.
func (m Model) groupMembers(group string) []string {
	if m.app == nil || m.app.Config == nil {
		return nil
	}
	return m.app.Config.Members(group, m.view.Context.Name)
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
	m.logs = append(m.logs, LogEntry{At: m.clock(), Text: redact(text), IsFail: isFail})
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
