package notify

import (
	"context"
	"errors"
	"testing"

	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/wg"
)

func TestDiffReportsAnUpTunnelGoingDown(t *testing.T) {
	prev := map[string]wg.Health{"alpha": wg.Up}
	next := map[string]wg.Health{"alpha": wg.Down}

	got := Diff(prev, next)

	if len(got) != 1 {
		t.Fatalf("got %d transitions, want 1: %+v", len(got), got)
	}
	if got[0].Tunnel != "alpha" || got[0].From != wg.Up || got[0].To != wg.Down {
		t.Errorf("transition = %+v, want alpha up->down", got[0])
	}
}

func TestDiffIgnoresUnchangedTunnels(t *testing.T) {
	prev := map[string]wg.Health{"alpha": wg.Up, "bravo": wg.Down}
	next := map[string]wg.Health{"alpha": wg.Up, "bravo": wg.Down}

	if got := Diff(prev, next); len(got) != 0 {
		t.Errorf("got %+v, want no transition", got)
	}
}

func TestDiffStaysSilentOnTheFirstSnapshot(t *testing.T) {
	// Without a previous state everything looks like a change; notifying then
	// would fire a burst at startup.
	next := map[string]wg.Health{"alpha": wg.Up, "bravo": wg.Down}

	if got := Diff(nil, next); len(got) != 0 {
		t.Errorf("got %+v, want no transition against an empty previous state", got)
	}
}

func TestDiffIgnoresTunnelsThatDisappeared(t *testing.T) {
	// A config file removed between two refreshes is not an outage.
	prev := map[string]wg.Health{"alpha": wg.Up}
	next := map[string]wg.Health{}

	if got := Diff(prev, next); len(got) != 0 {
		t.Errorf("got %+v, want no transition", got)
	}
}

func TestDiffIsSortedByTunnelName(t *testing.T) {
	prev := map[string]wg.Health{"bravo": wg.Up, "alpha": wg.Up, "charlie": wg.Up}
	next := map[string]wg.Health{"bravo": wg.Down, "alpha": wg.Down, "charlie": wg.Down}

	got := Diff(prev, next)

	want := []string{"alpha", "bravo", "charlie"}
	for i := range want {
		if got[i].Tunnel != want[i] {
			t.Errorf("transitions[%d] = %q, want %q", i, got[i].Tunnel, want[i])
		}
	}
}

func TestMessageDescribesRecovery(t *testing.T) {
	title, body := Transition{Tunnel: "alpha", From: wg.Down, To: wg.Up}.Message()

	if title != "alpha up" {
		t.Errorf("title = %q, want %q", title, "alpha up")
	}
	if body == "" {
		t.Error("body is empty, want a human-readable description")
	}
}

func TestMessageDescribesAnOutage(t *testing.T) {
	title, _ := Transition{Tunnel: "bravo", From: wg.Up, To: wg.Down}.Message()

	if title != "bravo down" {
		t.Errorf("title = %q, want %q", title, "bravo down")
	}
}

func TestArgvBuildsAnOsascriptNotification(t *testing.T) {
	got := argv(Transition{Tunnel: "alpha", From: wg.Down, To: wg.Up})

	if len(got) != 2 || got[0] != "-e" {
		t.Fatalf("argv = %q, want an osascript -e invocation", got)
	}
	if want := `display notification "`; got[1][:len(want)] != want {
		t.Errorf("script = %q, want it to start with %q", got[1], want)
	}
}

func TestArgvEscapesQuotesInTunnelNames(t *testing.T) {
	// A quote in a tunnel name would otherwise break out of the AppleScript
	// string literal.
	got := argv(Transition{Tunnel: `al"pha`, From: wg.Up, To: wg.Down})

	script := got[1]
	for i := 1; i < len(script); i++ {
		if script[i] == '"' && script[i-1] != '\\' {
			continue // the literal delimiters are legitimate
		}
	}
	if !contains(script, `al\"pha`) {
		t.Errorf("script = %q, want the quote escaped", script)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestNotifyPostsOneNotificationPerTransition(t *testing.T) {
	var got [][]string
	n := Notifier{Enabled: true, run: func(_ context.Context, args []string) error {
		got = append(got, args)
		return nil
	}}

	n.Notify(context.Background(), []Transition{
		{Tunnel: "alpha", From: wg.Up, To: wg.Down},
		{Tunnel: "bravo", From: wg.Down, To: wg.Up},
	})

	if len(got) != 2 {
		t.Fatalf("posted %d notification(s), want 2", len(got))
	}
	if !contains(got[0][1], "alpha down") {
		t.Errorf("first script = %q, want it to describe alpha going down", got[0][1])
	}
}

func TestNotifyStaysSilentWhenDisabled(t *testing.T) {
	var called bool
	n := Notifier{Enabled: false, run: func(context.Context, []string) error {
		called = true
		return nil
	}}

	n.Notify(context.Background(), []Transition{{Tunnel: "alpha", From: wg.Up, To: wg.Down}})

	if called {
		t.Error("a notification was posted while notifications are disabled")
	}
}

func TestNotifySwallowsFailures(t *testing.T) {
	// No GUI session means osascript fails; that must never disturb the TUI.
	n := Notifier{Enabled: true, run: func(context.Context, []string) error {
		return errors.New("no session")
	}}

	n.Notify(context.Background(), []Transition{{Tunnel: "alpha", From: wg.Up, To: wg.Down}})
}

func TestNotifyDoesNothingWithoutTransitions(t *testing.T) {
	var called bool
	n := Notifier{Enabled: true, run: func(context.Context, []string) error {
		called = true
		return nil
	}}

	n.Notify(context.Background(), nil)

	if called {
		t.Error("a notification was posted for an empty transition list")
	}
}

func TestNotifyFallsBackToOsascript(t *testing.T) {
	// A zero Notifier must still have somewhere to send its notifications.
	n := Notifier{Enabled: true, User: privdrop.User{HomeDir: t.TempDir()}}

	if n.runner() == nil {
		t.Error("runner = nil, want the osascript default")
	}
}
