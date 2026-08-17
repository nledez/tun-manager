package notify

import (
	"bytes"
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

func TestAZeroNotifierStillNamesACommand(t *testing.T) {
	// Nothing runs here: the point is that it resolves to something installed
	// rather than to an empty string.
	path, args := (Notifier{}).command(Transition{Tunnel: "alpha"})

	if path == "" {
		t.Error("no command resolved")
	}
	if len(args) == 0 {
		t.Error("no arguments built")
	}
}

func TestTerminalNotifierIsHandedTheIcon(t *testing.T) {
	// -contentImage rather than -appIcon: since macOS 11 the icon of a
	// notification comes from the bundle that sent it, and -appIcon is accepted
	// but ignored. -contentImage puts the image beside the text, which shows.
	n := Notifier{Enabled: true, Binary: "/somewhere/terminal-notifier", Icon: "/tmp/icon.png"}

	_, args := n.command(Transition{Tunnel: "alpha", From: wg.Down, To: wg.Up})

	if !argsContain(args, "-contentImage", "/tmp/icon.png") {
		t.Errorf("args = %v, want the image passed", args)
	}
	for _, a := range args {
		if a == "-appIcon" {
			t.Errorf("args = %v, want no -appIcon: macOS ignores it", args)
		}
	}
	if !argsContain(args, "-title", "tun-manager") {
		t.Errorf("args = %v, want a title", args)
	}
	if !argsContain(args, "-subtitle", "alpha up") {
		t.Errorf("args = %v, want the transition as the subtitle", args)
	}
}

func TestTerminalNotifierWithoutAnIconStillPosts(t *testing.T) {
	n := Notifier{Enabled: true, Binary: "/somewhere/terminal-notifier"}

	_, args := n.command(Transition{Tunnel: "alpha", From: wg.Down, To: wg.Up})

	for _, a := range args {
		if a == "-contentImage" {
			t.Errorf("args = %v, want no image flag when there is no image", args)
		}
	}
}

func TestOsascriptIsUsedWhenTerminalNotifierIsNot(t *testing.T) {
	n := Notifier{Enabled: true, Binary: "/usr/bin/osascript", Icon: "/tmp/icon.png"}

	path, args := n.command(Transition{Tunnel: "alpha", From: wg.Down, To: wg.Up})

	if path != "/usr/bin/osascript" {
		t.Errorf("path = %q", path)
	}
	if len(args) != 2 || args[0] != "-e" {
		t.Fatalf("args = %v, want an osascript -e invocation", args)
	}
	if !contains(args[1], "alpha up") {
		t.Errorf("script = %q, want it to describe the transition", args[1])
	}
}

func TestTheCommandIsChosenByItsName(t *testing.T) {
	// The tool is identified by what it is called, which is how a test can
	// point either backend at a script that records its arguments.
	tn := Notifier{Binary: "/opt/homebrew/bin/terminal-notifier"}
	if _, args := tn.command(Transition{Tunnel: "a"}); args[0] != "-title" {
		t.Errorf("args = %v, want the terminal-notifier form", args)
	}

	osa := Notifier{Binary: "/usr/bin/osascript"}
	if _, args := osa.command(Transition{Tunnel: "a"}); args[0] != "-e" {
		t.Errorf("args = %v, want the osascript form", args)
	}
}

func TestNewMaterialisesTheIconUnderTheUserCache(t *testing.T) {
	home := t.TempDir()
	u := privdrop.User{Username: "operator", HomeDir: home}

	n := New(u, true)

	if n.Icon == "" {
		t.Fatal("Icon is empty, want the embedded image written out")
	}
	if !strings.HasPrefix(n.Icon, home) {
		t.Errorf("Icon = %q, want it under %q", n.Icon, home)
	}
	written, err := os.ReadFile(n.Icon)
	if err != nil {
		t.Fatalf("read the icon: %v", err)
	}
	if len(written) == 0 || !bytes.HasPrefix(written, []byte("\x89PNG")) {
		t.Errorf("the icon is not a PNG (%d bytes)", len(written))
	}
}

func TestNewWithoutAWritableCacheStillNotifies(t *testing.T) {
	// A notification with no icon beats no notification.
	u := privdrop.User{Username: "operator", HomeDir: "/dev/null/nowhere"}

	n := New(u, true)

	if n.Icon != "" {
		t.Errorf("Icon = %q, want none when it could not be written", n.Icon)
	}
	if !n.Enabled {
		t.Error("Enabled = false, want the notifier still usable")
	}
}

func argsContain(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestNewWithAnUnwritableIconPathStillNotifies(t *testing.T) {
	// The directory is there but the file cannot be written: a directory in its
	// place is the simplest way to arrange that.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cache", "tun-manager", "icon.png"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	n := New(privdrop.User{HomeDir: home}, true)

	if n.Icon != "" {
		t.Errorf("Icon = %q, want none when the file could not be written", n.Icon)
	}
}

func TestTheInstalledCommandIsFoundOnThePath(t *testing.T) {
	// With no Binary set, the notifier resolves whichever of the two tools is
	// installed rather than assuming a path.
	dir := t.TempDir()
	fake := filepath.Join(dir, preferred)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", dir)

	path, args := Notifier{Icon: "/tmp/icon.png"}.command(Transition{Tunnel: "alpha"})

	if path != fake {
		t.Errorf("path = %q, want the one on PATH %q", path, fake)
	}
	if !argsContain(args, "-contentImage", "/tmp/icon.png") {
		t.Errorf("args = %v, want the image passed", args)
	}
}

func TestWithoutTerminalNotifierItFallsBackToOsascript(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	path, args := Notifier{}.command(Transition{Tunnel: "alpha"})

	if path != fallback {
		t.Errorf("path = %q, want %q", path, fallback)
	}
	if len(args) != 2 || args[0] != "-e" {
		t.Errorf("args = %v, want the osascript form", args)
	}
}

func TestPreviewPostsThroughTheResolvedCommand(t *testing.T) {
	binary, log := recorder(t)
	n := Notifier{Binary: binary, Icon: "/tmp/icon.png", User: privdrop.User{HomeDir: t.TempDir()}}

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
