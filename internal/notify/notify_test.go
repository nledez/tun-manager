package notify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	got := osascriptArgs(Transition{Tunnel: "alpha", From: wg.Down, To: wg.Up})

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
	got := osascriptArgs(Transition{Tunnel: `al"pha`, From: wg.Up, To: wg.Down})

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

// recorder stands in for osascript: it appends its arguments to a file, so a
// test can see exactly what would have reached the GUI session.
func recorder(t *testing.T) (binary, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	binary = filepath.Join(dir, "osascript")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + log + "\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", binary, err)
	}
	return binary, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", log, err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestNotifyRunsTheNotificationBinaryOncePerTransition(t *testing.T) {
	binary, log := recorder(t)
	n := Notifier{Enabled: true, Binary: binary, User: privdrop.User{HomeDir: t.TempDir()}}

	n.Notify(context.Background(), []Transition{
		{Tunnel: "alpha", From: wg.Up, To: wg.Down},
		{Tunnel: "bravo", From: wg.Down, To: wg.Up},
	})

	got := calls(t, log)
	// Two arguments per call: -e and the script.
	if len(got) != 4 {
		t.Fatalf("recorded %d argument(s), want 4:\n%v", len(got), got)
	}
	if got[0] != "-e" || got[2] != "-e" {
		t.Errorf("args = %v, want each call to start with -e", got)
	}
	if !strings.Contains(got[1], "alpha down") {
		t.Errorf("first script = %q, want it to describe alpha going down", got[1])
	}
	if !strings.Contains(got[3], "bravo up") {
		t.Errorf("second script = %q, want it to describe bravo coming up", got[3])
	}
}

func TestNotifyRunsNothingWhenDisabled(t *testing.T) {
	binary, log := recorder(t)
	n := Notifier{Enabled: false, Binary: binary}

	n.Notify(context.Background(), []Transition{{Tunnel: "alpha", From: wg.Up, To: wg.Down}})

	if got := calls(t, log); len(got) != 0 {
		t.Errorf("recorded %v, want nothing while notifications are disabled", got)
	}
}

func TestNotifyRunsNothingWithoutTransitions(t *testing.T) {
	binary, log := recorder(t)
	n := Notifier{Enabled: true, Binary: binary}

	n.Notify(context.Background(), nil)

	if got := calls(t, log); len(got) != 0 {
		t.Errorf("recorded %v, want nothing for an empty transition list", got)
	}
}

func TestNotifySurvivesABinaryThatFails(t *testing.T) {
	// No GUI session means osascript exits non-zero. That must never disturb
	// the TUI, so Notify swallows it.
	dir := t.TempDir()
	binary := filepath.Join(dir, "osascript")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	n := Notifier{Enabled: true, Binary: binary}

	n.Notify(context.Background(), []Transition{{Tunnel: "alpha", From: wg.Up, To: wg.Down}})
}

func TestNotifySurvivesAMissingBinary(t *testing.T) {
	n := Notifier{Enabled: true, Binary: filepath.Join(t.TempDir(), "absent")}

	n.Notify(context.Background(), []Transition{{Tunnel: "alpha", From: wg.Up, To: wg.Down}})
}

func TestNotifyStopsWithItsContext(t *testing.T) {
	binary, log := recorder(t)
	n := Notifier{Enabled: true, Binary: binary}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n.Notify(ctx, []Transition{{Tunnel: "alpha", From: wg.Up, To: wg.Down}})

	if got := calls(t, log); len(got) != 0 {
		t.Errorf("recorded %v, want nothing once the context is cancelled", got)
	}
}

func TestTheCommandIsAnAbsolutePathAndNeverALookup(t *testing.T) {
	// sudo on macOS does not reset PATH - there is no secure_path in the
	// sudoers it ships - so anything this program looks up by name is a name
	// chosen by whoever typed sudo, started by a process running as root. This
	// used to prefer terminal-notifier when it was on that PATH. It is now one
	// absolute path, and PATH is not consulted at all.
	//
	// The seam TestMain uses is put back for the length of this test, because
	// what the production value is happens to be the thing being asserted.
	stub := OsascriptPath
	OsascriptPath = "/usr/bin/osascript"
	t.Cleanup(func() { OsascriptPath = stub })

	planted := t.TempDir()
	if err := os.WriteFile(filepath.Join(planted, "osascript"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", planted)

	path, args := (Notifier{}).command(Transition{Tunnel: "alpha"})

	if path != "/usr/bin/osascript" {
		t.Errorf("command = %q, want the absolute osascript", path)
	}
	if len(args) == 0 {
		t.Error("no arguments built")
	}
}

func TestPreviewPostsThroughTheResolvedCommand(t *testing.T) {
	binary, log := recorder(t)
	n := Notifier{Binary: binary, User: privdrop.User{HomeDir: t.TempDir()}}

	got, err := n.Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if got != binary {
		t.Errorf("reported %q, want %q", got, binary)
	}
	if len(calls(t, log)) == 0 {
		t.Error("nothing was posted")
	}
}

func TestPreviewPostsEvenWhenNotificationsAreDisabled(t *testing.T) {
	// Asking for one is the point; the configuration setting is about the ones
	// the interface sends on its own.
	binary, log := recorder(t)
	n := Notifier{Enabled: false, Binary: binary}

	if _, err := n.Preview(context.Background()); err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if len(calls(t, log)) == 0 {
		t.Error("nothing was posted")
	}
}

func TestPreviewReportsAFailureRatherThanSwallowingIt(t *testing.T) {
	// Notify swallows failures so a missing session cannot disturb the TUI.
	// Preview is asked to find out, so it must not.
	n := Notifier{Binary: filepath.Join(t.TempDir(), "absent")}

	if _, err := n.Preview(context.Background()); err == nil {
		t.Fatal("Preview returned nil for a missing command, want the failure")
	}
}
