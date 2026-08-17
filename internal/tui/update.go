package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/notify"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/rate"
)

// Update advances the state machine. It performs no I/O: everything that talks
// to the system is returned as a tea.Cmd.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		if m.refreshing {
			return m, m.tick()
		}
		m.refreshing = true
		return m, tea.Batch(m.refresh(), m.tick(), m.heartbeat())

	case viewMsg:
		return m.onView(msg)

	case pingMsg:
		m.pinging = false
		for host, res := range msg.results {
			m.pings[host] = res
		}
		return m, nil

	case eventMsg:
		return m.onEvent(msg)

	case batchDoneMsg:
		return m.onBatchDone()

	case sampleMsg:
		return m.onSample(msg)

	case sampleTickMsg:
		// The pane may have closed while the tick was out.
		if !m.showGraph {
			return m, nil
		}
		return m, m.sample()

	case heartbeatMsg:
		// Only while there is something to report on; a timer firing on an idle
		// interface wakes the process for nothing.
		if !m.busy() && !m.pinging {
			return m, nil
		}
		m.beat++
		return m, m.heartbeat()

	case tea.KeyMsg:
		return m.onKey(msg)

	case requestMsg:
		// Listening again first: whatever this request turns into, the next one
		// still has to be heard.
		listen := nextRequest(msg.from)
		if m.refreshing {
			return m, listen
		}
		m.refreshing = true
		return m, tea.Batch(m.refresh(), m.heartbeat(), listen)
	}
	return m, nil
}

func (m Model) onView(msg viewMsg) (tea.Model, tea.Cmd) {
	m.refreshing = false
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

	if m.feed != nil {
		f, published := m.feed, msg.view
		cmd = tea.Batch(cmd, func() tea.Msg {
			f.Publish(published)
			return nil
		})
	}

	m.view = msg.view
	m.lastHealth = msg.view.Health()
	m.lastRefresh = msg.view.Taken
	if m.cursor >= len(m.view.Rows) {
		m.cursor = max(0, len(m.view.Rows)-1)
	}
	return m, cmd
}

// onEvent records one report from a running batch and asks for the next. Each
// event names its own tunnel, so nothing here has to know what the others are
// doing.
func (m Model) onEvent(msg eventMsg) (tea.Model, tea.Cmd) {
	e := msg.event
	switch e.Phase {
	case app.Started:
		m.inFlight = withKey(m.inFlight, e.Tunnel, e.Action)

	case app.Finished:
		m.inFlight = withoutKey(m.inFlight, e.Tunnel)
		text, isFail := describe(e.Result)
		m.log(text, isFail)
		if isFail {
			m.showLogs = true
		}
	}
	return m, nextEvent(msg.from)
}

// onSample records one reading and asks for the next, for as long as the pane
// is open.
func (m Model) onSample(msg sampleMsg) (tea.Model, tea.Cmd) {
	if !m.showGraph {
		return m, nil
	}
	// A tunnel that is down reports itself with no reading: there is nothing to
	// record, and the next tick will look again.
	if msg.tunnel != "" && !msg.at.IsZero() {
		m.series = withSample(m.series, msg)
	}
	return m, m.sampleTick()
}

// withSample copies the histories before adding to one of them: the model is
// copied on every update, and the series a previous model still points at must
// not change underneath it.
func withSample(series map[string]*rate.Series, msg sampleMsg) map[string]*rate.Series {
	out := make(map[string]*rate.Series, len(series)+1)
	for name, s := range series {
		out[name] = s
	}
	if out[msg.tunnel] == nil {
		out[msg.tunnel] = rate.New(graphPoints)
	}
	out[msg.tunnel].Add(msg.at, msg.rx, msg.tx)
	return out
}

// onBatchDone closes one batch. The refresh waits for the last of them: reading
// the whole system once per batch would be several reads of the same thing.
func (m Model) onBatchDone() (tea.Model, tea.Cmd) {
	m.batches = max(0, m.batches-1)
	if m.batches > 0 {
		return m, nil
	}

	m.selected = map[string]bool{}
	if m.refreshing {
		return m, nil
	}
	m.refreshing = true
	return m, m.refresh()
}

// The model is copied on every update, so a map it holds has to be copied too:
// mutating the one the previous model still points at would reach back in time.
func withKey(m map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for key, val := range m {
		out[key] = val
	}
	out[k] = v
	return out
}

func withoutKey(m map[string]string, k string) map[string]string {
	out := make(map[string]string, len(m))
	for key, val := range m {
		if key != k {
			out[key] = val
		}
	}
	return out
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

	case "g":
		m.showGraph = !m.showGraph
		if !m.showGraph {
			// A reading a second for a pane nobody is looking at is a reading
			// wasted, and the history goes with it: what it was worth was
			// being on screen.
			m.series = map[string]*rate.Series{}
			return m, nil
		}
		return m, m.sample()

	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	}

	switch key {
	case "r":
		// A second read of the whole system while the first is out gains
		// nothing.
		if m.refreshing {
			return m, nil
		}
		m.refreshing = true
		return m, tea.Batch(m.refresh(), m.heartbeat())

	case "p":
		if m.pinging {
			return m, nil
		}
		m.pinging = true
		return m, tea.Batch(m.ping(), m.heartbeat())

	// Acting on one tunnel no longer waits for another: batches overlap, and
	// startBatch leaves out whatever is already running.
	case "enter":
		return m.startBatch(m.toggleTargets())

	case "s":
		return m.startBatch(m.stopStart())

	case "n":
		return m.startBatch(m.upNeeded())
	}
	return m, nil
}

// startBatch hands a plan to the application and starts listening. The work
// runs on its own from here: everything the interface knows about it arrives as
// an event.
func (m Model) startBatch(steps []app.Step) (tea.Model, tea.Cmd) {
	steps = m.idle(steps)
	if len(steps) == 0 || m.app == nil {
		return m, nil
	}

	m.batches++
	// No deadline on the batch as a whole: it is as long as its slowest tunnel,
	// and a batch cut off halfway leaves the machine in a state nobody asked
	// for. Each wg-quick run is the thing that can hang, and the context that
	// carries it is the program's own.
	events := m.app.RunBatch(context.Background(), steps)

	return m, tea.Batch(nextEvent(events), m.heartbeat())
}

// idle drops the steps whose tunnel is already being worked on. Two wg-quick
// runs on the same tunnel is the one overlap that is never wanted, and it is
// the only thing a second batch has to be kept away from.
func (m Model) idle(steps []app.Step) []app.Step {
	out := make([]app.Step, 0, len(steps))
	for _, s := range steps {
		if _, running := m.inFlight[s.Tunnel.Name]; !running {
			out = append(out, s)
		}
	}
	return out
}

// toggleTargets brings the selected tunnels (or the cursor row) to the opposite
// of their current state.
func (m Model) toggleTargets() []app.Step {
	return m.view.PlanToggle(m.targets())
}

// stopStart is the `s` key: tear everything down, or start what is needed.
func (m Model) stopStart() []app.Step {
	if m.stopStartAction() == "down" {
		return m.view.PlanDown(m.groupMembers(profile.GroupAll))
	}
	return m.upNeeded()
}

// upNeeded is the `n` key, and the other half of `s`. There is no group
// parameter: the `extra` group is picked from the table with space and enter,
// not started wholesale.
func (m Model) upNeeded() []app.Step {
	return m.view.PlanUp(m.groupMembers(profile.GroupNeeded))
}
