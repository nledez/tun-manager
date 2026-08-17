// Package notify raises a macOS notification when a tunnel changes state.
//
// The program runs as root, and root has no GUI session: osascript is therefore
// run back as the pre-sudo user.
package notify

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// The two ways a notification reaches the desktop. terminal-notifier can be
// told which icon to show; osascript cannot - "display notification" has no
// clause for one, so it shows whatever icon the sender happens to have.
const (
	preferred = "terminal-notifier"
	fallback  = "/usr/bin/osascript"
)

// icon is carried in the binary so an installed tun-manager has one without an
// install step and without a file that can go missing. Regenerate it from the
// full-size image with `make icon`.
//
//go:embed icon.png
var icon []byte

// Notifier posts notifications to the user's session.
type Notifier struct {
	User    privdrop.User
	Enabled bool

	// Icon is the image the notification shows, when the command in use can
	// display one.
	Icon string

	// Binary is the command that posts a notification. It is a field so a test
	// can point it at a script that records its arguments rather than at the
	// real thing, which would reach the desktop. Empty means whichever of the
	// two is installed.
	Binary string
}

// New writes the icon out and returns a notifier that can show it.
func New(u privdrop.User, enabled bool) Notifier {
	return Notifier{User: u, Enabled: enabled, Icon: writeIcon(u)}
}

// writeIcon puts the embedded image where the notification command can read it,
// as the pre-sudo user who will be running that command. A failure is not worth
// reporting: a notification with no icon beats no notification.
func writeIcon(u privdrop.User) string {
	dir := u.CacheDir("tun-manager")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "icon.png")
	if err := os.WriteFile(path, icon, 0o644); err != nil {
		return ""
	}
	_ = u.Chown(dir)
	_ = u.Chown(path)
	return path
}

// command picks how to post one transition. The tool is identified by what it
// is called, which is also how a test points either form at a script of its own.
func (n Notifier) command(t Transition) (string, []string) {
	path := n.Binary
	if path == "" {
		if found, err := exec.LookPath(preferred); err == nil {
			path = found
		} else {
			path = fallback
		}
	}

	if strings.Contains(filepath.Base(path), preferred) {
		return path, n.notifierArgs(t)
	}
	return path, osascriptArgs(t)
}

func (n Notifier) notifierArgs(t Transition) []string {
	title, body := t.Message()
	args := []string{"-title", "tun-manager", "-subtitle", title, "-message", body}
	if n.Icon != "" {
		// -contentImage, not -appIcon. Since macOS 11 the icon of a
		// notification is the icon of the .app bundle that sent it, and
		// -appIcon is accepted but ignored: notifications sent through
		// terminal-notifier show a terminal whatever is passed. -contentImage
		// attaches the image beside the text instead, which does display.
		args = append(args, "-contentImage", n.Icon)
	}
	return args
}

// Preview posts a sample notification and reports which command carried it, so
// that whether the icon shows up can be seen rather than assumed.
//
// It ignores Enabled, because asking for one is the point, and it returns the
// failure rather than swallowing it, because finding out is the point too.
func (n Notifier) Preview(ctx context.Context) (string, error) {
	path, args := n.command(Transition{Tunnel: "alpha", From: wg.Down, To: wg.Up})
	return path, n.User.CommandContext(ctx, path, args...).Run()
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
