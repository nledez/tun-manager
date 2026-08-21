package tui

import (
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/wgconf"
)

func TestASkippedConfIsSaidOnceRatherThanOnEveryRefresh(t *testing.T) {
	// A refresh happens every few minutes. The same warning forty times an hour
	// is a warning somebody turns the log pane off to escape - and turning it
	// off is how the next real failure goes unseen.
	m := newModel(&app.App{}, nil, nil, nil)
	note := "/private/wireguard/config/two words.conf: ignored, " + wgconf.NameRule

	m.noteIgnored([]string{note})
	m.noteIgnored([]string{note})
	m.noteIgnored([]string{note, "other.conf: ignored, " + wgconf.NameRule})

	var said int
	for _, entry := range m.logs {
		if strings.Contains(entry.Text, "two words.conf") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("the same file was logged %d times, want once", said)
	}
	if len(m.logs) != 2 {
		t.Errorf("logged %d lines, want one per file", len(m.logs))
	}
	if !m.showLogs {
		t.Error("the log pane stayed shut on a file nobody will see in the table")
	}
}
