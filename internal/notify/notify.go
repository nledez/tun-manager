// Package notify raises a macOS notification when a tunnel changes state.
//
// The program runs as root, and root has no GUI session: osascript is therefore
// run back as the pre-sudo user.
package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/wg"
)

// Transition is a tunnel whose health changed between two refreshes.
type Transition struct {
	Tunnel string
	From   wg.Health
	To     wg.Health
}

// Message renders the notification title and body.
func (t Transition) Message() (title, body string) {
	title = fmt.Sprintf("%s %s", t.Tunnel, t.To)
	body = fmt.Sprintf("tunnel went from %s to %s", t.From, t.To)
	return title, body
}

// Diff lists the tunnels whose health changed, sorted by name.
//
// Tunnels absent from either side are ignored: on the first refresh there is
// nothing to compare against, and a removed config is not an outage.
func Diff(prev, next map[string]wg.Health) []Transition {
	var out []Transition
	for name, to := range next {
		from, ok := prev[name]
		if !ok || from == to {
			continue
		}
		out = append(out, Transition{Tunnel: name, From: from, To: to})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tunnel < out[j].Tunnel })
	return out
}

// OsascriptPath is how a notification reaches the desktop, and the only way.
//
// Absolute, and never looked up in PATH. sudo on macOS does not reset PATH -
// there is no secure_path in the shipped sudoers - so a lookup here would let
// anything named on that PATH be started by a program running as root. It is
// also why terminal-notifier is gone: it was preferred when installed, which
// meant the tool root reached for was chosen by whatever was on the PATH of the
// person who typed sudo. The only thing it bought was a thumbnail.
// A variable, and only so that a test suite can point it at a stand-in:
// nothing in the program ever writes to it, and a suite that forgets to set
// Notifier.Binary would otherwise post a real notification onto the screen of
// whoever is running it.
var OsascriptPath = "/usr/bin/osascript"

// Notifier posts notifications to the user's session.
type Notifier struct {
	User    privdrop.User
	Enabled bool

	// Binary is the command that posts a notification. It is a field so a test
	// can point it at a script that records its arguments rather than at the
	// real thing, which would reach the desktop. Empty means osascript, at its
	// absolute path.
	Binary string
}

// New returns a notifier that posts as the pre-sudo user.
func New(u privdrop.User, enabled bool) Notifier {
	return Notifier{User: u, Enabled: enabled}
}

// command picks how to post one transition.
func (n Notifier) command(t Transition) (string, []string) {
	path := n.Binary
	if path == "" {
		path = OsascriptPath
	}
	return path, osascriptArgs(t)
}

// Notify posts one notification per transition. Failures are silent: a missing
// GUI session must never disturb the TUI.
func (n Notifier) Notify(ctx context.Context, transitions []Transition) {
	if !n.Enabled || len(transitions) == 0 {
		return
	}
	for _, t := range transitions {
		path, args := n.command(t)
		// Demoted to the pre-sudo user: root has no GUI session to talk to.
		_ = n.User.CommandContext(ctx, path, args...).Run()
	}
}

// osascriptArgs builds the AppleScript for a transition.
func osascriptArgs(t Transition) []string {
	title, body := t.Message()
	script := fmt.Sprintf(
		`display notification "%s" with title "tun-manager" subtitle "%s"`,
		escape(body), escape(title),
	)
	return []string{"-e", script}
}

// escape makes a string safe inside an AppleScript double-quoted literal.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}
