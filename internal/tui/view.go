package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/format"
	"ledez.net/tun-manager/internal/wg"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "246"})
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "247", Dark: "241"})
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "243"})
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	ruleStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "252", Dark: "238"})

	upStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	staleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	downStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "247", Dark: "241"})
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// column widths, everything but the endpoint which takes what is left
const (
	wMark     = 3
	wName     = 14
	wGroup    = 7
	wState    = 8
	wShake    = 11
	wTransfer = 17
	wPing     = 8
)

// View renders the whole screen.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.table())
	if m.err != nil {
		b.WriteString("\n" + errStyle.Render(m.fit("error: "+m.err.Error())) + "\n")
	}
	if m.showLogs {
		b.WriteString("\n" + m.logPane())
	}
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m Model) header() string {
	left := titleStyle.Render("tun-manager")
	ctx := format.OrNone(m.view.Context.String())

	right := m.status()
	line := fmt.Sprintf("%s  %s", left, dimStyle.Render("ctx: "+ctx))

	gap := m.width - lipgloss.Width(line) - lipgloss.Width(right)
	if gap < 1 {
		// Too narrow for both. The countdown is the expendable half: which
		// network you are on decides what the table means.
		return clamp(line, m.width) + "\n" + m.rule()
	}
	return line + strings.Repeat(" ", gap) + dimStyle.Render(right) + "\n" + m.rule()
}

func (m Model) status() string {
	switch {
	case m.busy:
		return "working…"
	case m.pinging:
		return "pinging…"
	case m.lastRefresh.IsZero():
		return "loading…"
	default:
		next := m.refreshInterval() - m.clock().Sub(m.lastRefresh)
		if next < 0 {
			next = 0
		}
		return "next ⟳ " + next.Round(time.Second).String()
	}
}

func (m Model) rule() string {
	return ruleStyle.Render(strings.Repeat("─", max(0, m.width)))
}

func (m Model) table() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(m.columns("", "NAME", "GRP", "STATE", "HANDSHAKE", "RX / TX", "PING", "ENDPOINT")))
	b.WriteString("\n")

	if len(m.view.Rows) == 0 {
		b.WriteString(dimStyle.Render(m.fit("  no tunnel found")) + "\n")
		return b.String()
	}

	for i, r := range m.view.Rows {
		b.WriteString(m.line(i, r))
		b.WriteString("\n")
	}
	return b.String()
}

// rowCells is one table row, before it is padded and coloured.
type rowCells struct {
	Name      string
	Group     string
	State     string
	Handshake string
	Transfer  string
	Ping      string
	Endpoint  string
}

// cells renders the values of one row.
//
// A down tunnel has no interface, so it has no handshake, no counters and no
// latency: those cells stay empty rather than carrying a placeholder that reads
// like data. The group and the endpoint come from the configuration, so they
// are shown whatever the state.
func (m Model) cells(r app.Row) rowCells {
	c := rowCells{
		Name:     r.Tunnel.Name,
		Group:    format.OrNone(r.Group),
		State:    healthLabel(r.Health),
		Endpoint: format.OrNone(r.Tunnel.Endpoint),
	}
	if r.Health == wg.Down {
		return c
	}

	if r.Peer.Endpoint != "" {
		// The live endpoint is the resolved one, which is more useful than a
		// DNS name when something is wrong.
		c.Endpoint = r.Peer.Endpoint
	}
	c.Handshake = format.Age(r.Peer.LastHandshake, m.view.Taken)
	c.Transfer = format.Bytes(r.Peer.RxBytes) + " / " + format.Bytes(r.Peer.TxBytes)

	if res, ok := m.pings[r.Tunnel.CheckIP]; ok && r.Tunnel.CheckIP != "" {
		if res.Err != nil {
			c.Ping = errStyle.Render("×")
		} else {
			c.Ping = fmt.Sprintf("%dms", res.RTT.Milliseconds())
		}
	}
	return c
}

func (m Model) line(i int, r app.Row) string {
	// Two independent facts in one column: where the cursor is, and what is
	// selected. Letting the cursor overwrite the tick would mean pressing space
	// changes nothing on screen, since the selection is always made on the row
	// the cursor is on.
	cursor, tick := " ", " "
	if i == m.cursor {
		cursor = cursorStyle.Render("▸")
	}
	if m.selected[r.Tunnel.Name] {
		tick = "✓"
	}
	mark := cursor + tick

	c := m.cells(r)
	line := m.columns(mark, c.Name, c.Group, c.State, c.Handshake, c.Transfer, c.Ping, c.Endpoint)
	if r.Health == wg.Down {
		return downStyle.Render(line)
	}
	return line
}

// columns lays a row out at fixed widths so the table stays aligned whatever
// the styling does.
//
// The endpoint takes what is left, down to nothing: a column that insists on a
// minimum width pushes the row past the terminal, and a row past the terminal
// wraps, which is the one thing a table must never do.
func (m Model) columns(mark, name, group, state, shake, transfer, ping, endpoint string) string {
	fixed := 1 + wMark + wName + wGroup + wState + wShake + wTransfer + wPing
	wEndpoint := max(0, m.width-fixed)

	return clamp(" "+pad(mark, wMark)+pad(name, wName)+pad(group, wGroup)+
		pad(state, wState)+pad(shake, wShake)+pad(transfer, wTransfer)+
		pad(ping, wPing)+pad(endpoint, wEndpoint), m.width)
}

// pad fits a value to an exact width, cutting it rather than folding it. A
// folded value would push every row below it down by a line and take the table
// out of alignment; lipgloss's Width does exactly that, so it cannot be used
// here.
func pad(s string, w int) string {
	s = clamp(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// clamp cuts a string to a width, counting what is on screen rather than what
// is in the string: escape sequences take no columns.
func clamp(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}

func healthLabel(h wg.Health) string {
	switch h {
	case wg.Up:
		return upStyle.Render("● up")
	case wg.Stale:
		return staleStyle.Render("● stale")
	default:
		return downStyle.Render("○ down")
	}
}

func (m Model) logPane() string {
	var b strings.Builder
	b.WriteString(m.rule() + "\n")
	b.WriteString(headerStyle.Render(" LOGS") + "\n")

	if len(m.logs) == 0 {
		b.WriteString(dimStyle.Render(m.fit("  nothing yet")) + "\n")
		return b.String()
	}

	tail := m.logs
	if n := m.logLines(); len(tail) > n {
		tail = tail[len(tail)-n:]
	}
	for _, e := range tail {
		line := clamp(fmt.Sprintf("  %s  %s", e.At.Format("15:04:05"), e.Text), m.width)
		if e.IsFail {
			line = errStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// logLines keeps the pane to roughly a third of the screen.
func (m Model) logLines() int {
	n := m.height / 3
	if n < 3 {
		return 3
	}
	return n
}

func (m Model) footer() string {
	if m.showHelp {
		return helpStyle.Render(m.fit([]string{
			" ↑↓/jk  move cursor",
			" space  select / deselect a tunnel",
			" enter  toggle the selection (or the row under the cursor)",
			" r      refresh now",
			" p      ping every remote address",
			fmt.Sprintf(" s      stop/start everything (right now: %s)", m.stopStartAction()),
			" n      bring the `needed` group up",
			" l      toggle this log pane",
			" ?      toggle this help",
			" q      quit",
		}...))
	}
	return helpStyle.Render(m.fit(fmt.Sprintf(
		" r refresh · p ping · s %s all · ␣ select · ⏎ toggle · n needed · l logs · ? help · q quit",
		m.stopStartAction(),
	)))
}

// fit joins lines and cuts each to the terminal width, so the help never wraps
// on a narrow window.
func (m Model) fit(lines ...string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = clamp(l, m.width)
	}
	return strings.Join(out, "\n")
}
