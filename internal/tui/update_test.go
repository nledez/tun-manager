package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/format"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/notify"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

func row(name, group string, health wg.Health) app.Row {
	return app.Row{
		Tunnel: wgconf.Tunnel{Name: name, CheckIP: "10.20.30." + name[:1], Endpoint: name + ".example:51820"},
		Group:  group,
		Health: health,
	}
}

func loadedModel(rows ...app.Row) Model {
	m := New(nil, nil)
	m.width, m.height = 120, 30
	next, _ := m.Update(viewMsg{view: app.View{
		Context: netctx.Context{Name: "office", Interface: "en0", Address: "198.51.100.42"},
		Rows:    rows,
		Taken:   time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
	}})
	return next.(Model)
}

func key(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "up", "down", "enter":
		msg = tea.KeyMsg{Type: map[string]tea.KeyType{"up": tea.KeyUp, "down": tea.KeyDown, "enter": tea.KeyEnter}[k]}
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

var threeRows = []app.Row{
	row("alpha", profile.GroupNeeded, wg.Up),
	row("bravo", profile.GroupNeeded, wg.Down),
	row("charlie", profile.GroupExtra, wg.Down),
}

func TestViewMsgLoadsTheRows(t *testing.T) {
	m := loadedModel(threeRows...)

	if len(m.view.Rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(m.view.Rows))
	}
	if m.busy() {
		t.Error("busy = true, want false once the refresh landed")
	}
}

func TestCursorMovesAndClamps(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "j")
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
	m = key(m, "k")
	m = key(m, "k")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0: it must not go negative", m.cursor)
	}
	for i := 0; i < 10; i++ {
		m = key(m, "j")
	}
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2: it must stop on the last row", m.cursor)
	}
}

func TestSpaceTogglesSelection(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, " ")
	if !m.selected["alpha"] {
		t.Error("alpha is not selected, want it selected")
	}
	m = key(m, " ")
	if m.selected["alpha"] {
		t.Error("alpha is still selected, want the second press to deselect")
	}
}

func TestSelectionSurvivesARefresh(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, "j")
	m = key(m, " ")

	next, _ := m.Update(viewMsg{view: app.View{Rows: threeRows}})
	m = next.(Model)

	if !m.selected["bravo"] {
		t.Error("selection lost across a refresh, want it kept")
	}
}

func TestLTogglesTheLogPane(t *testing.T) {
	m := loadedModel(threeRows...)

	if m.showLogs {
		t.Fatal("showLogs = true at start, want false")
	}
	if m = key(m, "l"); !m.showLogs {
		t.Error("showLogs = false after `l`, want true")
	}
	if m = key(m, "l"); m.showLogs {
		t.Error("showLogs = true after a second `l`, want false")
	}
}

func TestQuestionMarkTogglesHelp(t *testing.T) {
	m := loadedModel(threeRows...)

	if m = key(m, "?"); !m.showHelp {
		t.Error("showHelp = false after `?`, want true")
	}
}

func TestQMarksTheModelQuitting(t *testing.T) {
	m := loadedModel(threeRows...)

	if m = key(m, "q"); !m.quitting {
		t.Error("quitting = false after `q`, want true")
	}
}

func TestStopStartMeansStopWhenSomethingIsUp(t *testing.T) {
	m := loadedModel(threeRows...)

	if got := m.stopStartAction(); got != "down" {
		t.Errorf("stopStartAction = %q, want %q when a tunnel is up", got, "down")
	}
}

func TestStopStartMeansStartWhenEverythingIsDown(t *testing.T) {
	m := loadedModel(
		row("alpha", profile.GroupNeeded, wg.Down),
		row("bravo", profile.GroupNeeded, wg.Down),
	)

	if got := m.stopStartAction(); got != "up" {
		t.Errorf("stopStartAction = %q, want %q when everything is down", got, "up")
	}
}

func TestStopStartTreatsAStaleTunnelAsUp(t *testing.T) {
	// A stale tunnel still holds an interface and routes; `s` must tear it down.
	m := loadedModel(row("alpha", profile.GroupNeeded, wg.Stale))

	if got := m.stopStartAction(); got != "down" {
		t.Errorf("stopStartAction = %q, want %q for a stale tunnel", got, "down")
	}
}

func TestRefreshKeyMarksTheModelBusy(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "r")

	if !m.busy() {
		t.Error("busy = false after `r`, want true while the refresh runs")
	}
}

func TestPingMsgFillsTheLatencyColumn(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(pingMsg{results: map[string]probe.Result{
		"10.20.30.b": {RTT: 18 * time.Millisecond},
	}})
	m = next.(Model)

	if got := m.pings["10.20.30.b"].RTT; got != 18*time.Millisecond {
		t.Errorf("rtt = %v, want 18ms", got)
	}
	if m.pinging {
		t.Error("pinging = true, want false once the results landed")
	}
}

func TestViewMsgErrorIsLoggedAndSurfaced(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(viewMsg{err: errors.New("no wireguard socket")})
	m = next.(Model)

	if m.err == nil {
		t.Fatal("err = nil, want the refresh error kept")
	}
	if len(m.logs) == 0 || !strings.Contains(m.logs[len(m.logs)-1].Text, "no wireguard socket") {
		t.Errorf("logs = %+v, want the error appended", m.logs)
	}
}

func TestViewMsgErrorKeepsThePreviousRows(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(viewMsg{err: errors.New("boom")})
	m = next.(Model)

	if len(m.view.Rows) != 3 {
		t.Errorf("len(rows) = %d, want the last good view kept", len(m.view.Rows))
	}
}

func TestEveryOutcomeIsLogged(t *testing.T) {
	m := loadedModel(threeRows...)

	for _, e := range []eventMsg{
		finished("bravo", "up", nil),
		finished("alpha", "down", errors.New("exit status 1")),
	} {
		next, _ := m.Update(e)
		m = next.(Model)
	}

	joined := logText(m)
	for _, want := range []string{"up bravo", "down alpha", "exit status 1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("logs missing %q:\n%s", want, joined)
		}
	}
}

func TestAFailureOpensTheLogPane(t *testing.T) {
	// A failure the user cannot see is a failure they will not act on.
	m := loadedModel(threeRows...)

	next, _ := m.Update(finished("alpha", "up", errors.New("exit status 1")))
	m = next.(Model)

	if !m.showLogs {
		t.Error("showLogs = false after a failed operation, want the pane opened")
	}
}

func TestTheSelectionSurvivesUntilTheBatchEnds(t *testing.T) {
	// A batch is several steps. Clearing on the first would drop the rows the
	// remaining steps were chosen from.
	m := loadedModel(threeRows...)
	m = key(m, " ")

	next, _ := m.Update(finished("alpha", "down", nil))
	m = next.(Model)

	if len(m.selected) != 1 {
		t.Errorf("selected = %v, want it kept while the batch runs", m.selected)
	}
}

func TestTheSelectionIsClearedWhenTheBatchEnds(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, " ")

	next, _ := m.Update(batchDoneMsg{})
	m = next.(Model)

	if len(m.selected) != 0 {
		t.Errorf("selected = %v, want it cleared once the batch is done", m.selected)
	}
}

func TestTheHeaderCountsWhatIsStillRunning(t *testing.T) {
	// Batches overlap, so a fraction of one of them would be a half-truth. What
	// is true at any moment is how many tunnels are in flight.
	m := loadedModel(threeRows...)
	m.batches = 1
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		next, _ := m.Update(started(name, app.ActionDown))
		m = next.(Model)
	}

	for want := 3; want > 0; want-- {
		if got := m.activity(); !strings.Contains(got, fmt.Sprintf("%d running", want)) {
			t.Errorf("activity = %q, want %d running", got, want)
		}
		next, _ := m.Update(finished([]string{"alpha", "bravo", "charlie"}[3-want], "down", nil))
		m = next.(Model)
	}

	if got := m.activity(); strings.Contains(got, "running") {
		t.Errorf("activity = %q, want nothing left running", got)
	}
}

func TestLogsAreCapped(t *testing.T) {
	m := loadedModel(threeRows...)

	for i := 0; i < maxLogs+20; i++ {
		next, _ := m.Update(finished("alpha", "up", nil))
		m = next.(Model)
	}

	if len(m.logs) > maxLogs {
		t.Errorf("len(logs) = %d, want at most %d", len(m.logs), maxLogs)
	}
}

func TestLogsNeverKeepWireGuardKeys(t *testing.T) {
	// wg-quick can echo a config line; a base64 key must not reach the pane.
	m := loadedModel(threeRows...)

	next, _ := m.Update(eventMsg{event: app.Event{
		Phase: app.Finished, Tunnel: "alpha", Action: "up",
		Result: wg.Result{
			Tunnel: "alpha",
			Action: "up",
			Err:    errors.New("bad config"),
			Output: "PrivateKey = +JTI/TFlmc4CNmqe0wc7b/BhyJm3vQWnYbqNO246QsOWI=",
		},
	}})
	m = next.(Model)

	if strings.Contains(logText(m), "JTI/TFlmc4CNmqe0wc7b") {
		t.Errorf("a key leaked into the logs:\n%s", logText(m))
	}
}

func TestTargetsUsesTheSelectionWhenThereIsOne(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, "j")
	m = key(m, " ")
	m = key(m, "j")
	m = key(m, " ")

	got := m.targets()

	if strings.Join(got, ",") != "bravo,charlie" {
		t.Errorf("targets = %v, want the selected rows in display order", got)
	}
}

func TestTargetsFallsBackToTheCursorRow(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, "j")

	if got := m.targets(); strings.Join(got, ",") != "bravo" {
		t.Errorf("targets = %v, want the row under the cursor", got)
	}
}

func TestWindowResizeIsRecorded(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = next.(Model)

	if m.width != 200 || m.height != 50 {
		t.Errorf("size = %dx%d, want 200x50", m.width, m.height)
	}
}

func TestReadingTheTableKeepsWorkingWhileBusy(t *testing.T) {
	// A key that does nothing is indistinguishable from a hung interface, and
	// moving the cursor or opening the log pane touches nothing on the machine.
	m := loadedModel(threeRows...)
	m = key(m, "r")

	m = key(m, "j")
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want the cursor to move while busy", m.cursor)
	}
	if m = key(m, "l"); !m.showLogs {
		t.Error("the log pane did not open while busy")
	}
	if m = key(m, "?"); !m.showHelp {
		t.Error("the help did not open while busy")
	}
	if m = key(m, " "); len(m.selected) != 1 {
		t.Error("a row could not be selected while busy")
	}
}

func TestReadingTheSystemIsNotStackedOnItself(t *testing.T) {
	// Refreshing and pinging read the whole system; a second one while the
	// first is out reads the same thing twice.
	m := loadedModel(threeRows...)

	m = key(m, "r")
	m = key(m, "p")

	for _, k := range []string{"r", "p"} {
		if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}); cmd != nil {
			t.Errorf("%q started a second read while one was in flight", k)
		}
	}
}

// started and finished build the two reports a batch produces, so a test reads
// as what happened rather than as a struct literal.
func started(tunnel, action string) eventMsg {
	return eventMsg{event: app.Event{Phase: app.Started, Tunnel: tunnel, Action: action}}
}

func finished(tunnel, action string, err error) eventMsg {
	return eventMsg{event: app.Event{
		Phase:  app.Finished,
		Tunnel: tunnel,
		Action: action,
		Result: wg.Result{Tunnel: tunnel, Action: action, Err: err},
	}}
}

func logText(m Model) string {
	var b strings.Builder
	for _, e := range m.logs {
		b.WriteString(e.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func TestEKeyIsNotBound(t *testing.T) {
	// Bringing the whole `extra` group up was never the point: extras are
	// picked one by one with space and enter.
	m := loadedModel(threeRows...)

	m = key(m, "e")

	if m.busy() {
		t.Error("busy = true after `e`, want the key ignored")
	}
}

func TestFooterDoesNotAdvertiseTheExtraGroup(t *testing.T) {
	m := loadedModel(threeRows...)

	for _, view := range []string{m.footer(), key(m, "?").footer()} {
		if strings.Contains(view, "extra") {
			t.Errorf("help still mentions the extra group:\n%s", view)
		}
	}
}

// liveRow is a row with an interface behind it.
func liveRow(name, group string, health wg.Health) app.Row {
	r := row(name, group, health)
	r.Peer = wg.Peer{
		Device:        "utun7",
		LastHandshake: time.Date(2026, 8, 16, 9, 59, 18, 0, time.UTC),
		RxBytes:       765952,
		TxBytes:       752230,
	}
	return r
}

func TestDownTunnelShowsNothingInTheLiveColumns(t *testing.T) {
	// A tunnel with no interface has no handshake, no counters and no latency;
	// a placeholder in those cells only invites reading them as data.
	m := loadedModel(threeRows...)
	m.pings["10.20.30.b"] = probe.Result{RTT: 93 * time.Millisecond}

	c := m.cells(row("bravo", profile.GroupNeeded, wg.Down))

	for _, cell := range []struct{ name, got string }{
		{"handshake", c.Handshake},
		{"transfer", c.Transfer},
		{"ping", c.Ping},
	} {
		if cell.got != "" {
			t.Errorf("%s = %q, want empty for a down tunnel", cell.name, cell.got)
		}
	}
}

func TestDownTunnelStillShowsItsIdentity(t *testing.T) {
	m := loadedModel(threeRows...)

	c := m.cells(row("bravo", profile.GroupNeeded, wg.Down))

	if c.Name != "bravo" {
		t.Errorf("Name = %q, want %q", c.Name, "bravo")
	}
	if c.Endpoint == "" {
		t.Error("Endpoint is empty, want the configured endpoint of a down tunnel")
	}
	if c.State == "" {
		t.Error("State is empty, want the down marker")
	}
}

func TestUngroupedTunnelStillShowsADashInTheGroupColumn(t *testing.T) {
	// The group is a property of the configuration, not of the live state.
	m := loadedModel(threeRows...)

	c := m.cells(row("bravo", "", wg.Down))

	if c.Group != format.None {
		t.Errorf("Group = %q, want %q", c.Group, format.None)
	}
}

func TestUpTunnelShowsItsLiveValues(t *testing.T) {
	m := loadedModel(threeRows...)
	m.pings["10.20.30.a"] = probe.Result{RTT: 81 * time.Millisecond}

	c := m.cells(liveRow("alpha", profile.GroupNeeded, wg.Up))

	if c.Handshake != "42s" {
		t.Errorf("Handshake = %q, want %q", c.Handshake, "42s")
	}
	if !strings.Contains(c.Transfer, "/") {
		t.Errorf("Transfer = %q, want both counters", c.Transfer)
	}
	if c.Ping != "81ms" {
		t.Errorf("Ping = %q, want %q", c.Ping, "81ms")
	}
}

func TestStaleTunnelKeepsItsLiveValues(t *testing.T) {
	// Stale means the interface is there but silent; the counters still tell
	// the story.
	m := loadedModel(threeRows...)

	c := m.cells(liveRow("alpha", profile.GroupNeeded, wg.Stale))

	if c.Handshake == "" {
		t.Error("Handshake is empty, want the age of the last handshake")
	}
}

func TestUnprobedTunnelHasAnEmptyPingCell(t *testing.T) {
	m := loadedModel(threeRows...)

	c := m.cells(liveRow("alpha", profile.GroupNeeded, wg.Up))

	if c.Ping != "" {
		t.Errorf("Ping = %q, want empty before any probe ran", c.Ping)
	}
}

func TestFailedProbeShowsACrossOnALiveTunnel(t *testing.T) {
	m := loadedModel(threeRows...)
	m.pings["10.20.30.a"] = probe.Result{Err: errors.New("no reply")}

	c := m.cells(liveRow("alpha", profile.GroupNeeded, wg.Up))

	if !strings.Contains(c.Ping, "×") {
		t.Errorf("Ping = %q, want a cross", c.Ping)
	}
}

func TestUnknownMessagesLeaveTheModelAlone(t *testing.T) {
	m := loadedModel(threeRows...)

	next, cmd := m.Update("some message the model knows nothing about")

	if cmd != nil {
		t.Error("cmd != nil, want no command for an unknown message")
	}
	if len(next.(Model).view.Rows) != 3 {
		t.Error("rows changed on an unknown message")
	}
}

func TestTickRefreshesWhenIdle(t *testing.T) {
	m := loadedModel(threeRows...)

	next, cmd := m.Update(tickMsg(time.Now()))

	if !next.(Model).busy() {
		t.Error("busy = false after a tick, want the refresh started")
	}
	if cmd == nil {
		t.Error("cmd = nil, want the refresh and the next tick")
	}
}

func TestTickIsSkippedWhileBusy(t *testing.T) {
	// A refresh landing on top of a running operation would show a half-applied
	// state.
	m := loadedModel(threeRows...)
	m = key(m, "r")

	next, cmd := m.Update(tickMsg(time.Now()))

	if !next.(Model).busy() {
		t.Error("busy = false, want the running operation untouched")
	}
	if cmd == nil {
		t.Error("cmd = nil, want the tick rescheduled anyway")
	}
}

func TestCtrlCQuits(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !next.(Model).quitting {
		t.Error("quitting = false after ctrl+c, want true")
	}
}

func TestQuittingWorksEvenWhileBusy(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, "r")

	m = key(m, "q")

	if !m.quitting {
		t.Error("quitting = false, want q to work mid-operation")
	}
}

func TestQuittingRendersNothing(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "q")

	if m.View() != "" {
		t.Errorf("View = %q, want empty once quitting", m.View())
	}
}

func TestTargetsAreEmptyWithoutAnyRow(t *testing.T) {
	m := loadedModel()

	if got := m.targets(); len(got) != 0 {
		t.Errorf("targets = %v, want none", got)
	}
}

func TestEnterOnAnEmptyTableDoesNothing(t *testing.T) {
	m := loadedModel()

	m = key(m, "enter")

	if m.busy() {
		t.Error("busy = true, want enter ignored with nothing to act on")
	}
}

func TestEmptyTableSaysSo(t *testing.T) {
	m := loadedModel()

	if !strings.Contains(m.View(), "no tunnel found") {
		t.Errorf("View does not report an empty table:\n%s", m.View())
	}
}

func TestSkippedOperationsAreLogged(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(eventMsg{event: app.Event{Phase: app.Finished, Tunnel: "alpha", Action: "up", Result: wg.Result{Tunnel: "alpha", Action: "up", Skipped: true}}})
	m = next.(Model)

	if !strings.Contains(logText(m), "skipped") {
		t.Errorf("logs = %s, want the skip explained", logText(m))
	}
}

func TestAnEmptyPlanStartsNothing(t *testing.T) {
	// Everything already in the wanted state: there is no work, so there is no
	// batch and nothing claims to be busy.
	m := loadedModel(threeRows...)

	next, cmd := m.startBatch(nil)
	m = next.(Model)

	if cmd != nil {
		t.Error("a command was returned for an empty plan")
	}
	if m.busy() {
		t.Error("busy = true, want nothing started")
	}
}

func TestRefreshErrorIsShownInTheView(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(viewMsg{err: errors.New("no wireguard socket")})
	m = next.(Model)

	if !strings.Contains(m.View(), "no wireguard socket") {
		t.Errorf("View hides the error:\n%s", m.View())
	}
}

func TestCursorIsPulledBackWhenTheTableShrinks(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, "j")
	m = key(m, "j")

	next, _ := m.Update(viewMsg{view: app.View{Rows: threeRows[:1]}})
	m = next.(Model)

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want it clamped to the last row", m.cursor)
	}
}

func TestCursorSurvivesAnEmptyTable(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, "j")

	next, _ := m.Update(viewMsg{view: app.View{}})
	m = next.(Model)

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
}

func TestLogPaneSaysWhenItIsEmpty(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "l")

	if !strings.Contains(m.View(), "nothing yet") {
		t.Errorf("empty log pane says nothing:\n%s", m.View())
	}
}

func TestLogPaneKeepsAtLeastThreeLinesOnATinyScreen(t *testing.T) {
	m := loadedModel(threeRows...)
	m.height = 1

	if got := m.logLines(); got != 3 {
		t.Errorf("logLines = %d, want 3", got)
	}
}

func TestLogPaneGrowsWithTheScreen(t *testing.T) {
	m := loadedModel(threeRows...)
	m.height = 60

	if got := m.logLines(); got != 20 {
		t.Errorf("logLines = %d, want a third of the screen", got)
	}
}

func TestHeaderReportsTheRefreshCountdown(t *testing.T) {
	m := loadedModel(threeRows...)
	m.lastRefresh = time.Now()

	if !strings.Contains(m.View(), "next ⟳") {
		t.Errorf("header shows no countdown:\n%s", m.View())
	}
}

func TestHeaderReportsWork(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "r")

	if !strings.Contains(m.View(), "refreshing") {
		t.Errorf("header does not report the running refresh:\n%s", m.View())
	}
}

func TestHeaderReportsProbing(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "p")

	if !strings.Contains(m.View(), "pinging") {
		t.Errorf("header does not report the running probes:\n%s", m.View())
	}
}

func TestHeaderReportsTheFirstLoad(t *testing.T) {
	m := New(nil, nil)
	m.width, m.height = 120, 30

	if !strings.Contains(m.View(), "loading") {
		t.Errorf("header does not report the first load:\n%s", m.View())
	}
}

func TestAModelWithoutAnApplicationUsesTheDefaultInterval(t *testing.T) {
	m := New(nil, nil)

	if got := m.refreshInterval(); got != profile.DefaultRefresh {
		t.Errorf("refreshInterval = %v, want %v", got, profile.DefaultRefresh)
	}
}

func TestNarrowTerminalsStillRenderARule(t *testing.T) {
	m := loadedModel(threeRows...)
	m.width = 5

	if m.View() == "" {
		t.Error("View is empty on a narrow terminal")
	}
}

// notifierTo builds a notifier whose command records its arguments, so a test
// can see what the update loop would have posted.
func notifierTo(t *testing.T) (*notify.Notifier, func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	binary := filepath.Join(dir, "osascript")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + log + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", binary, err)
	}
	read := func() []string {
		data, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("read %s: %v", log, err)
		}
		return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	return &notify.Notifier{Enabled: true, Binary: binary}, read
}

func viewOf(rows ...app.Row) app.View {
	return app.View{Rows: rows, Taken: time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)}
}

func TestAStateChangeIsNotified(t *testing.T) {
	notifier, recorded := notifierTo(t)
	m := New(nil, notifier)
	m.width, m.height = 120, 30

	// First refresh: nothing to compare against, so nothing is posted.
	next, cmd := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Up))})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("the first refresh produced a command, want silence with no previous state")
	}

	// Second refresh: alpha went down.
	_, cmd = m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Down))})
	if cmd == nil {
		t.Fatal("no command for a tunnel going down, want a notification")
	}
	cmd()

	got := recorded()
	if len(got) != 2 {
		t.Fatalf("recorded %v, want one call of two arguments", got)
	}
	if !strings.Contains(got[1], "alpha down") {
		t.Errorf("script = %q, want it to describe alpha going down", got[1])
	}
}

func TestAnUnchangedStateIsNotNotified(t *testing.T) {
	notifier, recorded := notifierTo(t)
	m := New(nil, notifier)

	rows := viewOf(row("alpha", profile.GroupNeeded, wg.Up))
	next, _ := m.Update(viewMsg{view: rows})
	m = next.(Model)
	_, cmd := m.Update(viewMsg{view: rows})

	if cmd != nil {
		t.Error("a command was produced for an unchanged state")
	}
	if got := recorded(); len(got) != 0 {
		t.Errorf("recorded %v, want nothing", got)
	}
}

func TestNKeyStartsOneStepPerTunnelOfTheNeededGroup(t *testing.T) {
	a := testApp(t, &fakeRunner{})
	m := New(a, nil)
	m.width, m.height = 120, 30
	next, _ := m.Update(viewMsg{view: viewOf(
		row("alpha", profile.GroupNeeded, wg.Down),
		row("bravo", profile.GroupNeeded, wg.Down),
	)})
	m = next.(Model)

	m = key(m, "n")

	if !m.busy() {
		t.Error("busy = false after `n`, want the group being started")
	}
	if m.batches != 1 {
		t.Errorf("batches = %d, want one", m.batches)
	}
}

func TestCellsPreferTheLiveEndpoint(t *testing.T) {
	// The resolved endpoint is more useful than a DNS name when something is
	// wrong, so it wins over the configured one while the tunnel is up.
	m := loadedModel(threeRows...)
	r := liveRow("alpha", profile.GroupNeeded, wg.Up)
	r.Peer.Endpoint = "192.0.2.10:51820"

	if got := m.cells(r).Endpoint; got != "192.0.2.10:51820" {
		t.Errorf("Endpoint = %q, want the live one", got)
	}
}

func TestASelectionShowsUnderTheCursorThatMadeIt(t *testing.T) {
	// Selecting happens on the row the cursor is on, so a mark the cursor hides
	// means pressing space looks like it did nothing.
	m := loadedModel(threeRows...)

	m = key(m, " ")

	if !strings.Contains(m.View(), "✓") {
		t.Errorf("no selection mark in the table:\n%s", m.View())
	}
}

func TestTheCursorAndTheSelectionAreBothVisible(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, " ")

	if !strings.Contains(m.View(), "▸✓") {
		t.Errorf("the cursor and the mark are not shown together:\n%s", m.View())
	}
}

func TestASelectionSurvivesTheCursorMovingAway(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, " ")

	m = key(m, "j")

	view := m.View()
	if !strings.Contains(view, "✓") {
		t.Errorf("the mark vanished when the cursor left:\n%s", view)
	}
	if strings.Contains(view, "▸✓") {
		t.Errorf("the cursor is still drawn on the selected row:\n%s", view)
	}
}

func TestTheLogPaneKeepsOnlyItsTail(t *testing.T) {
	m := loadedModel(threeRows...)
	m.height = 12 // four log lines
	m = key(m, "l")

	for i := 0; i < 10; i++ {
		next, _ := m.Update(finished(fmt.Sprintf("tunnel%d", i), "up", nil))
		m = next.(Model)
	}

	view := m.View()
	if strings.Contains(view, "tunnel0") {
		t.Errorf("the oldest entry is still on screen:\n%s", view)
	}
	if !strings.Contains(view, "tunnel9") {
		t.Errorf("the newest entry is missing:\n%s", view)
	}
}

func TestAFailedOperationIsRenderedInTheLogPane(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(finished("alpha", "up", errors.New("exit status 1")))
	m = next.(Model)

	// The failure opens the pane on its own.
	if !strings.Contains(m.View(), "exit status 1") {
		t.Errorf("the failure is not on screen:\n%s", m.View())
	}
}

func TestCommandsOfAModelWithoutAnApplicationAreHarmless(t *testing.T) {
	// New(nil, nil) is how the pure Update tests build a model. The commands it
	// returns must not dereference the application that is not there.
	m := New(nil, nil)

	if msg, ok := m.refresh()().(viewMsg); !ok || msg.err != nil {
		t.Errorf("refresh returned %#v, want an empty viewMsg", msg)
	}
	if msg, ok := m.ping()().(pingMsg); !ok || len(msg.results) != 0 {
		t.Errorf("ping returned %#v, want an empty pingMsg", msg)
	}
	if _, cmd := m.startBatch([]app.Step{{Action: app.ActionUp}}); cmd != nil {
		t.Error("a batch was started without an application to run it")
	}
}

func TestTheCountdownIsMeasuredFromTheLastRefresh(t *testing.T) {
	at := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	m := loadedModel(threeRows...)
	m.now = func() time.Time { return at.Add(42 * time.Second) }
	m.lastRefresh = at

	if got := m.countdown(); got != "next ⟳ 4m18s" {
		t.Errorf("status = %q, want the remainder of the interval", got)
	}
}

func TestTheCountdownStopsAtZero(t *testing.T) {
	// A refresh that overran its interval must not display a negative delay.
	at := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	m := loadedModel(threeRows...)
	m.now = func() time.Time { return at.Add(time.Hour) }
	m.lastRefresh = at

	if got := m.countdown(); got != "next ⟳ 0s" {
		t.Errorf("status = %q, want it clamped to zero", got)
	}
}

func TestLogEntriesAreStampedFromTheSameClock(t *testing.T) {
	at := time.Date(2026, 8, 16, 14, 30, 5, 0, time.UTC)
	m := loadedModel(threeRows...)
	m.now = func() time.Time { return at }

	next, _ := m.Update(finished("alpha", "up", nil))
	m = next.(Model)

	if got := m.logs[0].At; !got.Equal(at) {
		t.Errorf("At = %v, want %v", got, at)
	}
}

func TestASingleLongStepStillAnimates(t *testing.T) {
	// One tunnel means one step, so the progress count never moves and the
	// frame would stay identical for as long as wg-quick takes. Something has
	// to change on screen or the interface reads as hung.
	m := loadedModel(threeRows...)
	m.batches = 1

	first := m.activity()
	next, cmd := m.Update(heartbeatMsg{})
	m = next.(Model)

	if cmd == nil {
		t.Error("no command returned, want the heartbeat to schedule the next one")
	}
	if m.activity() == first {
		t.Errorf("activity is still %q, want the frame to change", first)
	}
}

func TestTheHeartbeatStopsWhenTheWorkDoes(t *testing.T) {
	// A timer that keeps firing on an idle interface wakes the process for
	// nothing.
	m := loadedModel(threeRows...)

	_, cmd := m.Update(heartbeatMsg{})

	if cmd != nil {
		t.Error("the heartbeat rescheduled itself while idle")
	}
}

func TestStartingWorkStartsTheHeartbeat(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "r")

	if !m.busy() {
		t.Fatal("busy = false after `r`")
	}
	if _, cmd := m.Update(heartbeatMsg{}); cmd == nil {
		t.Error("the heartbeat does not run while busy")
	}
}

func TestAModelWithoutAnApplicationPlansNothing(t *testing.T) {
	// New(nil, nil) is how the pure Update tests build a model; asking it for a
	// group must yield no steps rather than dereference the application.
	m := New(nil, nil)

	if got := m.groupMembers(profile.GroupNeeded); got != nil {
		t.Errorf("groupMembers = %v, want none", got)
	}
	if steps := m.upNeeded(); len(steps) != 0 {
		t.Errorf("upNeeded planned %d step(s), want none", len(steps))
	}
}

func TestTheActivitySitsBesideTheContext(t *testing.T) {
	// Far right is where the eye goes last. Work in progress belongs next to
	// what it is happening to.
	m := loadedModel(threeRows...)
	m.batches = 1
	m.inFlight = map[string]string{"alpha": app.ActionUp, "bravo": app.ActionUp, "charlie": app.ActionUp}

	header := strings.Split(m.View(), "\n")[0]
	ctx := strings.Index(header, "ctx:")
	work := strings.Index(header, "running")

	if ctx < 0 || work < 0 {
		t.Fatalf("header has no context or no activity:\n%s", header)
	}
	if work < ctx {
		t.Errorf("the activity comes before the context:\n%s", header)
	}
	if gap := work - ctx; gap > 60 {
		t.Errorf("the activity is %d columns from the context, want it beside:\n%s", gap, header)
	}
}

func TestTheCountdownStaysOnTheRight(t *testing.T) {
	m := loadedModel(threeRows...)
	m.now = func() time.Time { return m.lastRefresh.Add(42 * time.Second) }

	header := strings.Split(m.View(), "\n")[0]

	if !strings.Contains(header, "next ⟳") {
		t.Fatalf("no countdown in the header:\n%s", header)
	}
	if at := strings.Index(header, "next ⟳"); at < len([]rune(header))/2 {
		t.Errorf("the countdown is at column %d, want it on the right:\n%s", at, header)
	}
}

func TestAnIdleHeaderShowsNoActivity(t *testing.T) {
	m := loadedModel(threeRows...)

	if got := m.activity(); got != "" {
		t.Errorf("activity = %q, want none while idle", got)
	}
}

func TestTheActivitySaysWhatIsHappening(t *testing.T) {
	m := loadedModel(threeRows...)

	m.inFlight = map[string]string{"alpha": app.ActionUp, "bravo": app.ActionUp, "charlie": app.ActionUp}
	if got := m.activity(); !strings.Contains(got, "3 running") {
		t.Errorf("activity = %q, want the tunnels in flight counted", got)
	}

	m.inFlight = map[string]string{}
	m.refreshing = true
	if got := m.activity(); !strings.Contains(got, "refreshing") {
		t.Errorf("activity = %q, want it to say it is refreshing", got)
	}

	m.refreshing, m.pinging = false, true
	if got := m.activity(); !strings.Contains(got, "pinging") {
		t.Errorf("activity = %q, want it to say it is pinging", got)
	}
}

func TestTheActivityCarriesTheSpinner(t *testing.T) {
	m := loadedModel(threeRows...)
	m.batches = 1

	first := m.activity()
	m.beat++
	if second := m.activity(); second == first {
		t.Errorf("activity is still %q after a beat, want the spinner to turn", first)
	}
}

func TestTheCountdownIsStillShownWhileWorking(t *testing.T) {
	// The next automatic refresh is no less true for something being in flight.
	m := loadedModel(threeRows...)
	m.batches = 1
	m.now = func() time.Time { return m.lastRefresh.Add(time.Second) }

	if !strings.Contains(m.View(), "next ⟳") {
		t.Errorf("the countdown vanished while working:\n%s", m.View())
	}
}

func TestAToggleAnnouncesWhatEachTunnelWillDo(t *testing.T) {
	// The batch is planned from the table, so what a row will do is known
	// before the work starts rather than after it reports.
	a := testApp(t, &fakeRunner{})
	m := New(a, nil)
	next, _ := m.Update(viewMsg{view: viewOf(
		row("alpha", profile.GroupNeeded, wg.Up),
		row("bravo", profile.GroupNeeded, wg.Down),
	)})
	m = next.(Model)
	m.selected = map[string]bool{"alpha": true, "bravo": true}

	steps := m.toggleTargets()

	if len(steps) != 2 {
		t.Fatalf("got %d step(s), want one per tunnel", len(steps))
	}
	if steps[0].Tunnel.Name != "alpha" || steps[0].Action != app.ActionDown {
		t.Errorf("step 0 = %+v, want alpha going down", steps[0])
	}
	if steps[1].Tunnel.Name != "bravo" || steps[1].Action != app.ActionUp {
		t.Errorf("step 1 = %+v, want bravo coming up", steps[1])
	}
}

func TestAGroupBatchAnnouncesTheSameActionThroughout(t *testing.T) {
	a := testApp(t, &fakeRunner{})
	m := New(a, nil)
	next, _ := m.Update(viewMsg{view: viewOf(
		row("alpha", profile.GroupNeeded, wg.Down),
		row("bravo", profile.GroupNeeded, wg.Down),
	)})
	m = next.(Model)

	for _, s := range m.upNeeded() {
		if s.Action != app.ActionUp {
			t.Errorf("step %+v, want every step of an up group to be an up", s)
		}
	}
}

func TestSeveralRowsCanBeInFlightAtOnce(t *testing.T) {
	// Tunnels start at the same time, so more than one row is waiting at any
	// moment. A single "current tunnel" cannot say that.
	m := loadedModel(threeRows...)
	m.batches = 1

	for _, e := range []app.Event{
		{Phase: app.Started, Tunnel: "alpha", Action: app.ActionDown},
		{Phase: app.Started, Tunnel: "bravo", Action: app.ActionUp},
	} {
		next, _ := m.Update(eventMsg{event: e})
		m = next.(Model)
	}

	if got := m.cells(m.view.Rows[0]).State; !strings.Contains(got, "stopping") {
		t.Errorf("alpha shows %q, want it stopping", got)
	}
	if got := m.cells(m.view.Rows[1]).State; !strings.Contains(got, "starting") {
		t.Errorf("bravo shows %q, want it starting", got)
	}
	if got := m.cells(m.view.Rows[2]).State; strings.Contains(got, "ing") {
		t.Errorf("charlie shows %q, want it untouched", got)
	}
}

func TestARowClearsOnItsOwnResult(t *testing.T) {
	// One tunnel finishing says nothing about the others, so it must not clear
	// their marks.
	m := loadedModel(threeRows...)
	m.batches = 1
	for _, e := range []app.Event{
		{Phase: app.Started, Tunnel: "alpha", Action: app.ActionDown},
		{Phase: app.Started, Tunnel: "bravo", Action: app.ActionUp},
	} {
		next, _ := m.Update(eventMsg{event: e})
		m = next.(Model)
	}

	next, _ := m.Update(eventMsg{event: app.Event{
		Phase: app.Finished, Tunnel: "alpha", Action: app.ActionDown,
		Result: wg.Result{Tunnel: "alpha", Action: "down"},
	}})
	m = next.(Model)

	if got := m.cells(m.view.Rows[0]).State; strings.Contains(got, "stopping") {
		t.Errorf("alpha still shows %q after reporting", got)
	}
	if got := m.cells(m.view.Rows[1]).State; !strings.Contains(got, "starting") {
		t.Errorf("bravo shows %q, want it still starting", got)
	}
}

func TestATunnelIsInFlightBetweenItsTwoEvents(t *testing.T) {
	m := loadedModel(threeRows...)
	m.batches = 1

	next, _ := m.Update(started("alpha", app.ActionUp))
	m = next.(Model)
	if len(m.inFlight) != 1 {
		t.Errorf("inFlight = %v after a start, want alpha in it", m.inFlight)
	}

	next, _ = m.Update(finished("alpha", app.ActionUp, nil))
	m = next.(Model)
	if len(m.inFlight) != 0 {
		t.Errorf("inFlight = %v after a finish, want it empty", m.inFlight)
	}
}

func TestAFinishedEventIsLogged(t *testing.T) {
	m := loadedModel(threeRows...)
	m.batches = 1

	next, _ := m.Update(eventMsg{event: app.Event{
		Phase: app.Finished, Tunnel: "alpha", Action: app.ActionUp,
		Result: wg.Result{Tunnel: "alpha", Action: "up", Err: errors.New("exit status 1")},
	}})
	m = next.(Model)

	if !strings.Contains(logText(m), "up alpha") {
		t.Errorf("logs = %s, want the outcome recorded", logText(m))
	}
	if !m.showLogs {
		t.Error("showLogs = false after a failure, want the pane opened")
	}
}

func TestOneBatchEndingLeavesAnotherAlone(t *testing.T) {
	// Every tunnel clears its own mark when it reports, so a batch closing has
	// nothing left of its own to clear - and must not touch what is still
	// running elsewhere.
	m := loadedModel(threeRows...)
	m.batches = 2
	next, _ := m.Update(started("alpha", app.ActionDown))
	m = next.(Model)

	next, _ = m.Update(batchDoneMsg{})
	m = next.(Model)

	if _, running := m.inFlight["alpha"]; !running {
		t.Errorf("inFlight = %v, want alpha still marked", m.inFlight)
	}
	if m.batches != 1 {
		t.Errorf("batches = %d, want the other one still counted", m.batches)
	}
}

func TestTheRowMarksTurnWithTheSpinner(t *testing.T) {
	m := loadedModel(threeRows...)
	m.batches = 1
	next, _ := m.Update(eventMsg{event: app.Event{Phase: app.Started, Tunnel: "alpha", Action: app.ActionDown}})
	m = next.(Model)

	first := m.cells(m.view.Rows[0]).State
	m.beat++

	if second := m.cells(m.view.Rows[0]).State; second == first {
		t.Errorf("the mark is still %q after a beat, want it to turn", first)
	}
}

func TestAnotherTunnelCanBeActedOnWhileOneRuns(t *testing.T) {
	// Tunnels no longer wait for each other, so neither should the person
	// driving them: enter on a second row while a first one is still starting.
	a := testApp(t, &fakeRunner{})
	m := New(a, nil)
	next, _ := m.Update(viewMsg{view: viewOf(
		row("alpha", profile.GroupNeeded, wg.Down),
		row("bravo", profile.GroupNeeded, wg.Down),
	)})
	m = next.(Model)

	m = key(m, "enter") // alpha
	next, _ = m.Update(started("alpha", app.ActionUp))
	m = next.(Model)

	m = key(m, "j")
	before := m.batches
	m = key(m, "enter") // bravo, while alpha is still going

	if m.batches != before+1 {
		t.Errorf("batches = %d, want a second one started", m.batches)
	}
}

func TestATunnelAlreadyRunningIsNotStartedTwice(t *testing.T) {
	// Two wg-quick runs on the same tunnel is the one overlap that is never
	// wanted.
	a := testApp(t, &fakeRunner{})
	m := New(a, nil)
	next, _ := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Down))})
	m = next.(Model)
	next, _ = m.Update(started("alpha", app.ActionUp))
	m = next.(Model)

	before := m.batches
	m = key(m, "enter")

	if m.batches != before {
		t.Errorf("batches = %d, want no batch for a tunnel already running", m.batches)
	}
}

func TestABatchSkipsTheTunnelsAlreadyRunning(t *testing.T) {
	a := testApp(t, &fakeRunner{})
	m := New(a, nil)
	next, _ := m.Update(viewMsg{view: viewOf(
		row("alpha", profile.GroupNeeded, wg.Down),
		row("bravo", profile.GroupNeeded, wg.Down),
	)})
	m = next.(Model)
	next, _ = m.Update(started("alpha", app.ActionUp))
	m = next.(Model)

	steps := m.idle(m.upNeeded())

	if len(steps) != 1 || steps[0].Tunnel.Name != "bravo" {
		t.Errorf("steps = %+v, want alpha left alone", steps)
	}
}

func TestTheRefreshIsNotFiredTwiceAtOnce(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "r")
	if !m.refreshing {
		t.Fatal("refreshing = false after `r`")
	}

	// A second press must not stack a second read of the whole system.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); cmd != nil {
		t.Error("a second refresh was started while one was in flight")
	}
}

func TestTheLastBatchToFinishRefreshes(t *testing.T) {
	// One refresh for however many batches were running, not one each.
	m := loadedModel(threeRows...)
	m.batches = 2

	next, cmd := m.Update(batchDoneMsg{})
	m = next.(Model)
	if cmd != nil {
		t.Error("the first batch to finish asked for a refresh")
	}

	_, cmd = m.Update(batchDoneMsg{})
	if cmd == nil {
		t.Error("the last batch to finish did not refresh")
	}
}

func TestTheActivityCountsWhatIsRunning(t *testing.T) {
	m := loadedModel(threeRows...)
	for _, e := range []eventMsg{started("alpha", app.ActionDown), started("bravo", app.ActionUp)} {
		next, _ := m.Update(e)
		m = next.(Model)
	}

	if got := m.activity(); !strings.Contains(got, "2 running") {
		t.Errorf("activity = %q, want the number in flight", got)
	}
}

func TestTheLastBatchDoesNotRefreshOverAnotherRefresh(t *testing.T) {
	// A batch ending while the periodic tick is already reading would be two
	// reads of the same thing, and the second would land on top of the first.
	m := loadedModel(threeRows...)
	m.batches, m.refreshing = 1, true

	_, cmd := m.Update(batchDoneMsg{})

	if cmd != nil {
		t.Error("a refresh was started while one was already in flight")
	}
}
