package tui

import (
	"context"
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
	if m.busy {
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

	if !m.busy {
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

func TestOpMsgLogsEveryResult(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(opMsg{results: []wg.Result{
		{Tunnel: "bravo", Action: "up"},
		{Tunnel: "alpha", Action: "down", Err: errors.New("exit status 1")},
	}})
	m = next.(Model)

	joined := logText(m)
	for _, want := range []string{"up bravo", "down alpha", "exit status 1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("logs missing %q:\n%s", want, joined)
		}
	}
}

func TestOpMsgOpensTheLogPaneOnFailure(t *testing.T) {
	// A failure the user cannot see is a failure they will not act on.
	m := loadedModel(threeRows...)

	next, _ := m.Update(opMsg{results: []wg.Result{
		{Tunnel: "alpha", Action: "up", Err: errors.New("exit status 1")},
	}})
	m = next.(Model)

	if !m.showLogs {
		t.Error("showLogs = false after a failed operation, want the pane opened")
	}
}

func TestOpMsgClearsTheSelection(t *testing.T) {
	m := loadedModel(threeRows...)
	m = key(m, " ")

	next, _ := m.Update(opMsg{results: []wg.Result{{Tunnel: "alpha", Action: "down"}}})
	m = next.(Model)

	if len(m.selected) != 0 {
		t.Errorf("selected = %v, want it cleared once the operation ran", m.selected)
	}
}

func TestLogsAreCapped(t *testing.T) {
	m := loadedModel(threeRows...)

	for i := 0; i < maxLogs+20; i++ {
		next, _ := m.Update(opMsg{results: []wg.Result{{Tunnel: "alpha", Action: "up"}}})
		m = next.(Model)
	}

	if len(m.logs) > maxLogs {
		t.Errorf("len(logs) = %d, want at most %d", len(m.logs), maxLogs)
	}
}

func TestLogsNeverKeepWireGuardKeys(t *testing.T) {
	// wg-quick can echo a config line; a base64 key must not reach the pane.
	m := loadedModel(threeRows...)

	next, _ := m.Update(opMsg{results: []wg.Result{{
		Tunnel: "alpha",
		Action: "up",
		Err:    errors.New("bad config"),
		Output: "PrivateKey = +JTI/TFlmc4CNmqe0wc7b/BhyJm3vQWnYbqNO246QsOWI=",
	}}})
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

func TestKeysAreIgnoredWhileBusy(t *testing.T) {
	// Firing a second wg-quick batch over a running one is how routing tables
	// get corrupted.
	m := loadedModel(threeRows...)
	m = key(m, "r")

	before := m.cursor
	m = key(m, "j")

	if m.cursor != before {
		t.Errorf("cursor moved while busy: %d -> %d", before, m.cursor)
	}
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

	if m.busy {
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

	if !next.(Model).busy {
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

	if !next.(Model).busy {
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

	if m.busy {
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

	next, _ := m.Update(opMsg{results: []wg.Result{{Tunnel: "alpha", Action: "up", Skipped: true}}})
	m = next.(Model)

	if !strings.Contains(logText(m), "skipped") {
		t.Errorf("logs = %s, want the skip explained", logText(m))
	}
}

func TestAFailingBatchIsLoggedAsAWhole(t *testing.T) {
	m := loadedModel(threeRows...)

	next, _ := m.Update(opMsg{err: errors.New("unknown tunnel \"ghost\"")})
	m = next.(Model)

	if !strings.Contains(logText(m), "ghost") {
		t.Errorf("logs = %s, want the batch error kept", logText(m))
	}
	if !m.showLogs {
		t.Error("showLogs = false, want the pane opened on a batch failure")
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

	if !strings.Contains(m.View(), "working") {
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

func TestNKeyStartsTheNeededGroup(t *testing.T) {
	m := loadedModel(threeRows...)

	m = key(m, "n")

	if !m.busy {
		t.Error("busy = false after `n`, want the group being started")
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
		next, _ := m.Update(opMsg{results: []wg.Result{
			{Tunnel: fmt.Sprintf("tunnel%d", i), Action: "up"},
		}})
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

	next, _ := m.Update(opMsg{results: []wg.Result{
		{Tunnel: "alpha", Action: "up", Err: errors.New("exit status 1")},
	}})
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
	cmd := m.operate(func(context.Context, *app.App) ([]wg.Result, error) {
		t.Fatal("the operation ran without an application")
		return nil, nil
	})
	if msg, ok := cmd().(opMsg); !ok || len(msg.results) != 0 {
		t.Errorf("operate returned %#v, want an empty opMsg", msg)
	}
}
