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

// Notifier posts notifications to the user's session.
type Notifier struct {
	User    privdrop.User
	Enabled bool

	// run posts one notification. It is a field so tests can observe what would
	// be sent without spawning osascript; nil means the real thing.
	run func(ctx context.Context, args []string) error
}

// runner returns the configured poster, or the osascript one.
func (n Notifier) runner() func(context.Context, []string) error {
	if n.run != nil {
		return n.run
	}
	return func(ctx context.Context, args []string) error {
		// Demoted to the pre-sudo user: root has no GUI session to talk to.
		return n.User.CommandContext(ctx, "/usr/bin/osascript", args...).Run()
	}
}

// Notify posts one notification per transition. Failures are silent: a missing
// GUI session must never disturb the TUI.
func (n Notifier) Notify(ctx context.Context, transitions []Transition) {
	if !n.Enabled || len(transitions) == 0 {
		return
	}
	run := n.runner()
	for _, t := range transitions {
		_ = run(ctx, argv(t))
	}
}

// argv builds the osascript arguments for a transition.
func argv(t Transition) []string {
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
