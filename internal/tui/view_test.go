package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

// TestMain pins the colour profile so that a golden frame is the same text
// wherever it is produced. Whether a terminal gets colour is decided by
// lipgloss from the environment, which a golden file cannot depend on; the
// colour decisions themselves are checked separately, further down, by forcing
// a profile that emits them.
// It also neutralises the notification tools: see stubNotificationTools.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)

	dir, err := stubNotificationTools()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stub the notification tools:", err)
		os.Exit(1)
	}

	code := m.Run()

	// Not deferred: os.Exit does not run deferred calls.
	os.RemoveAll(dir) //nolint:errcheck // the suite is over either way
	os.Exit(code)
}

// stubNotificationTools puts a harmless stand-in for terminal-notifier and
// osascript on PATH, so no test in this package can reach the desktop.
//
// internal/notify picks its tool by looking both names up on PATH. A test that
// builds a notifier without pointing Binary at a script of its own would
// otherwise post a real notification onto the screen of whoever is running the
// suite, with nothing failing to say so.
func stubNotificationTools() (string, error) {
	dir, err := os.MkdirTemp("", "notify-stubs")
	if err != nil {
		return "", err
	}
	for _, name := range []string{"terminal-notifier", "osascript"} {
		script := filepath.Join(dir, name)
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			os.RemoveAll(dir) //nolint:errcheck // the error being returned is the one that matters
			return "", err
		}
	}
	if err := os.Setenv("PATH", dir); err != nil {
		os.RemoveAll(dir) //nolint:errcheck // the error being returned is the one that matters
		return "", err
	}
	return dir, nil
}

var frameTaken = time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)

// frameRow is a row with everything filled in, so the golden frames exercise
// the widest values each column has to hold.
func frameRow(name, group string, health wg.Health, rx, tx int64, handshake time.Duration) app.Row {
	r := app.Row{
		Tunnel: wgconf.Tunnel{
			Name:     name,
			CheckIP:  "10.20.30." + name[:1],
			Endpoint: name + ".example:51820",
		},
		Group:  group,
		Health: health,
	}
	if health != wg.Down {
		r.Peer = wg.Peer{
			Device:        "utun7",
			Endpoint:      "192.0.2.10:51820",
			LastHandshake: frameTaken.Add(-handshake),
			RxBytes:       rx,
			TxBytes:       tx,
		}
	}
	return r
}

// frameModel is a fully populated interface at a fixed instant.
func frameModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := New(nil, nil)
	m.width, m.height = width, height
	m.now = func() time.Time { return frameTaken.Add(48 * time.Second) }

	next, _ := m.Update(viewMsg{view: app.View{
		Context: netctx.Context{Name: "office", Interface: "en0", Address: "198.51.100.42"},
		Taken:   frameTaken,
		Rows: []app.Row{
			frameRow("alpha", profile.GroupNeeded, wg.Up, 1258291, 4096, 12*time.Second),
			frameRow("bravo", profile.GroupNeeded, wg.Stale, 2411724, 984320, 9*time.Minute),
			frameRow("charlie", profile.GroupExtra, wg.Down, 0, 0, 0),
			frameRow("delta", "", wg.Down, 0, 0, 0),
		},
	}})
	m = next.(Model)
	m.pings = map[string]probe.Result{
		"10.20.30.a": {RTT: 18 * time.Millisecond},
		"10.20.30.b": {Err: os.ErrDeadlineExceeded},
	}
	return m
}

// A golden frame is the only assertion that catches a column drifting, a value
// overflowing its width or a row losing its alignment. Regenerate them all with
// `go test ./internal/tui/ -update` after checking the diff is what you meant.

func TestGoldenTable(t *testing.T) {
	m := frameModel(t, 120, 30)
	m.selected = map[string]bool{"bravo": true}
	m.cursor = 0

	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGoldenLogPane(t *testing.T) {
	m := frameModel(t, 120, 30)
	m = key(m, "l")
	for _, r := range []wg.Result{
		{Tunnel: "alpha", Action: "up"},
		{Tunnel: "bravo", Action: "up", Skipped: true},
		{Tunnel: "charlie", Action: "down", Err: os.ErrPermission, Output: "wg-quick: permission denied"},
	} {
		next, _ := m.Update(eventMsg{event: app.Event{Phase: app.Finished, Tunnel: r.Tunnel, Action: r.Action, Result: r}})
		m = next.(Model)
	}

	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGoldenGraphPane(t *testing.T) {
	m := frameModel(t, 120, 30)
	m = key(m, "g")

	// A ramp that resets, so the frame shows both a slope and the drop back to
	// the axis, and the two directions land on different scales.
	var rx, tx int64
	for i := range 60 {
		rx += int64((i % 12) * 8192)
		tx += int64((i % 20) * 512)
		next, _ := m.Update(sampleMsg{
			tunnel: "alpha",
			at:     frameTaken.Add(time.Duration(i) * time.Second),
			rx:     rx,
			tx:     tx,
		})
		m = next.(Model)
	}

	teatest.RequireEqualOutput(t, []byte(m.View()))
}

func TestGoldenHelp(t *testing.T) {
	m := frameModel(t, 120, 30)

	teatest.RequireEqualOutput(t, []byte(key(m, "?").View()))
}

func TestGoldenNarrowTerminal(t *testing.T) {
	teatest.RequireEqualOutput(t, []byte(frameModel(t, 60, 20).View()))
}

func TestGoldenEmptyTable(t *testing.T) {
	m := New(nil, nil)
	m.width, m.height = 120, 30
	m.now = func() time.Time { return frameTaken }

	teatest.RequireEqualOutput(t, []byte(m.View()))
}

// dataRows returns the table rows of a frame: everything between the header
// row and the first blank line.
func dataRows(t *testing.T, view string) []string {
	t.Helper()
	lines := strings.Split(view, "\n")
	var out []string
	seenHeader := false
	for _, l := range lines {
		if strings.Contains(l, "NAME") && strings.Contains(l, "ENDPOINT") {
			seenHeader = true
			continue
		}
		if !seenHeader {
			continue
		}
		if strings.TrimSpace(l) == "" {
			break
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		t.Fatalf("no data row found in:\n%s", view)
	}
	return out
}

func TestEveryColumnStartsWhereItsHeaderDoes(t *testing.T) {
	// The table is laid out by padding, not by a tab writer, so a value one
	// rune too wide shifts a single row and nothing else complains. Anchoring
	// every row to the header is what catches that.
	view := frameModel(t, 120, 30).View()
	header := headerRow(t, view)

	// Each of these columns always holds something: a name, a group or its
	// dash, a state, an endpoint read from the configuration.
	for _, column := range []string{"NAME", "GRP", "STATE", "ENDPOINT"} {
		at := strings.Index(header, column)
		if at < 0 {
			t.Fatalf("no %s column in the header: %q", column, header)
		}
		for i, row := range dataRows(t, view) {
			runes := []rune(row)
			if at >= len(runes) || runes[at] == ' ' {
				t.Errorf("row %d has nothing at the %s column (offset %d):\n%s\n%s",
					i, column, at, header, row)
			}
		}
	}
}

func headerRow(t *testing.T, view string) string {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "NAME") && strings.Contains(l, "ENDPOINT") {
			return l
		}
	}
	t.Fatalf("no header row in:\n%s", view)
	return ""
}

func TestNoLineOverflowsTheTerminal(t *testing.T) {
	// A line one column too long wraps, and a wrapped table is unreadable.
	for _, width := range []int{40, 60, 80, 120, 200} {
		m := frameModel(t, width, 30)
		m = key(m, "l")
		for _, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("at width %d a line is %d wide: %q", width, got, line)
			}
		}
	}
}

func TestALongValueIsTruncatedRatherThanWrapped(t *testing.T) {
	m := frameModel(t, 120, 30)
	long := app.Row{
		Tunnel: wgconf.Tunnel{
			Name:     "a-tunnel-name-far-longer-than-its-column",
			Endpoint: "an-endpoint-name-that-will-not-fit-either.example:51820",
		},
		Group:  profile.GroupNeeded,
		Health: wg.Down,
	}
	m.view.Rows = append(m.view.Rows, long)

	rows := dataRows(t, m.View())

	if n := len(rows); n != 5 {
		t.Fatalf("got %d rows, want 5: a long value wrapped onto its own line", n)
	}
	for _, r := range rows {
		if lipgloss.Width(r) > 120 {
			t.Errorf("row is %d wide: %q", lipgloss.Width(r), r)
		}
	}
}

// The colour decisions cannot be read off a golden frame, which is deliberately
// colourless. These force a profile that emits escapes and check that the
// states are told apart, without pinning the exact codes.

func withColour(t *testing.T) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
}

func TestTheThreeStatesAreColouredDifferently(t *testing.T) {
	withColour(t)

	up, stale, down := healthLabel(wg.Up), healthLabel(wg.Stale), healthLabel(wg.Down)

	if up == stale || up == down || stale == down {
		t.Errorf("two states render identically: up=%q stale=%q down=%q", up, stale, down)
	}
	for name, got := range map[string]string{"up": up, "stale": stale, "down": down} {
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("%s carries no colour: %q", name, got)
		}
	}
}

func TestAFailedLogEntryStandsOut(t *testing.T) {
	withColour(t)
	m := frameModel(t, 120, 30)
	m = key(m, "l")

	for _, r := range []wg.Result{
		{Tunnel: "alpha", Action: "up"},
		{Tunnel: "bravo", Action: "up", Err: os.ErrPermission},
	} {
		next, _ := m.Update(eventMsg{event: app.Event{Phase: app.Finished, Tunnel: r.Tunnel, Action: r.Action, Result: r}})
		m = next.(Model)
	}
	pane := m.logPane()

	var ok, failed string
	for _, line := range strings.Split(pane, "\n") {
		switch {
		case strings.Contains(line, "up alpha"):
			ok = line
		case strings.Contains(line, "up bravo"):
			failed = line
		}
	}
	if ok == "" || failed == "" {
		t.Fatalf("both entries not found in:\n%s", pane)
	}
	if !strings.Contains(failed, "\x1b[") {
		t.Errorf("the failure is not highlighted: %q", failed)
	}
	if strings.Contains(ok, "\x1b[") {
		t.Errorf("a successful entry is highlighted, so failures do not stand out: %q", ok)
	}
}

func TestADownRowIsDimmedAndAnUpRowIsNot(t *testing.T) {
	withColour(t)
	m := frameModel(t, 120, 30)

	up := m.line(9, m.view.Rows[0])   // index off the cursor
	down := m.line(9, m.view.Rows[2]) // charlie, down

	if !strings.Contains(down, "\x1b[") {
		t.Errorf("a down row carries no styling: %q", down)
	}
	if strings.Count(down, "\x1b[") <= strings.Count(up, "\x1b[") {
		t.Errorf("a down row is not dimmed as a whole:\n up   %q\n down %q", up, down)
	}
}

func TestGoldenBatchInProgress(t *testing.T) {
	// The one frame the reported bug was about: a batch running, with the
	// tunnel being waited on saying so where its state would be.
	m := frameModel(t, 120, 30)
	m.batches = 1
	m.inFlight = map[string]string{"charlie": app.ActionUp}
	next, _ := m.Update(started("charlie", app.ActionUp))

	teatest.RequireEqualOutput(t, []byte(next.(Model).View()))
}

func TestNothingFromAFileCanDriveTheTerminal(t *testing.T) {
	// The two ends of the same problem. The context name comes from
	// ~/.config/tun-manager/config.yaml, which is the one file this program
	// reads that a process running as the user can rewrite; the endpoint comes
	// from a .conf. Both are printed to a terminal, which runs what it is
	// handed.
	m := New(nil, nil)
	m.width, m.height = 120, 30
	m.now = func() time.Time { return frameTaken }

	row := frameRow("alpha", profile.GroupNeeded, wg.Down, 0, 0, 0)
	row.Tunnel.Endpoint = "moc.elpmaxe‮:51820"
	next, _ := m.Update(viewMsg{view: app.View{
		Context: netctx.Context{Name: "office\x1b[2J\x1b[Hgone"},
		Taken:   frameTaken,
		Rows:    []app.Row{row},
	}})
	m = next.(Model)

	screen := m.View()
	for _, bad := range []string{"\x1b[2J", "‮"} {
		if strings.Contains(screen, bad) {
			t.Errorf("the screen carries %q", bad)
		}
	}
	// What was in the value is still shown, as text.
	if !strings.Contains(screen, "office") || !strings.Contains(screen, "moc.elpmaxe") {
		t.Errorf("the values themselves were lost:\n%s", screen)
	}
}

func TestALogLineCannotRepaintTheScreen(t *testing.T) {
	// A log line carries the output of wg-quick, which runs as root and whose
	// output nobody here wrote.
	m := New(nil, nil)
	m.width, m.height = 120, 30
	m.now = func() time.Time { return frameTaken }
	m.showLogs = true

	m.log("up alpha: FAILED — \x1b[2Jnothing to see", true)

	if strings.Contains(m.View(), "\x1b[2J") {
		t.Error("a log line put an escape sequence on the screen")
	}
}
