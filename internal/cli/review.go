package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"ledez.net/tun-manager/internal/wgconf"
)

// newRenderer builds the renderer the styles below come from.
//
// A variable so a test can force a colour profile: lipgloss decides whether to
// emit escape codes by looking at what it is writing to, and a test writes to a
// strings.Builder, which is not a terminal. Never assigned outside the tests.
var newRenderer = lipgloss.NewRenderer

// Review shows a configuration to whoever is importing it, before anything is
// written.
//
// What is being imported is not a set of fields: it is a file that wg-quick
// will read as root. So the file is shown whole, with its line numbers, rather
// than summarised — a summary is a decision about what matters, made by the
// program, on behalf of the person who is about to take the risk.
//
// Two things are called out beside it. The address this program will send
// packets to on its own, because it comes from the file and from nowhere else.
// And the hooks, in red, because they are the one thing on the screen that can
// hand somebody else root on this machine.
//
// Private and preshared keys are hidden. The output is scrolled through and
// pasted into issues, and nothing in it needs to be the key.
func Review(w io.Writer, source string, body []byte, tun wgconf.Tunnel) error {
	var out strings.Builder

	fmt.Fprintf(&out, "%s\n\n", source) //nolint:errcheck // a Builder does not fail
	writeNumbered(&out, wgconf.Redact(body))

	fmt.Fprintf(&out, "\n  ping address  %s\n", tun.CheckIP) //nolint:errcheck // likewise
	writeHooks(&out, w, tun)

	_, err := io.WriteString(w, out.String())
	return err
}

// writeNumbered prints the file with its line numbers, so that the warning
// below can point at one and so that somebody can open the file and find what
// they just read.
func writeNumbered(out *strings.Builder, body []byte) {
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	width := len(strconv.Itoa(len(lines)))

	for i, line := range lines {
		fmt.Fprintf(out, "  %*d │ %s\n", width, i+1, line) //nolint:errcheck // a Builder does not fail
	}
}

// writeHooks says what wg-quick would run as root, or says nothing at all.
//
// Nothing at all is the important half: a warning that appears on every import
// is a warning nobody reads by the third one.
//
// The style is built from the writer the review is going to, not from the
// Builder it is assembled in: lipgloss decides whether to colour by looking at
// what it writes to, and colouring a file somebody piped this into would leave
// escape codes in the middle of a configuration they are trying to read.
func writeHooks(out *strings.Builder, w io.Writer, tun wgconf.Tunnel) {
	if len(tun.Hooks) == 0 {
		return
	}

	alarm := newRenderer(w).NewStyle().Bold(true).Foreground(lipgloss.Color("9"))

	// One Render per line rather than one for the block: lipgloss pads a
	// multi-line string out to its widest line, which puts trailing spaces
	// nobody asked for in the middle of a warning.
	said := []string{
		"",
		"  ⚠ this configuration asks for " + commands(len(tun.Hooks)) + " to be run as root",
		"    wg-quick runs them every time this tunnel goes up or down, with every privilege",
		"    tun-manager has. Read them, and be sure:",
		"",
	}
	for _, hook := range tun.Hooks {
		said = append(said, fmt.Sprintf("    line %d │ %s = %s", hook.Line, hook.Key, hook.Value))
	}

	for _, line := range said {
		if line == "" {
			out.WriteString("\n") //nolint:errcheck // a Builder does not fail
			continue
		}
		fmt.Fprintf(out, "%s\n", alarm.Render(line)) //nolint:errcheck // likewise
	}
}

// commands keeps the sentence above grammatical without two copies of it.
func commands(n int) string {
	if n == 1 {
		return "a command"
	}
	return fmt.Sprintf("%d commands", n)
}
