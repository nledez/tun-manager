// Package format renders the values the CLI table and the TUI table share.
//
// Both surfaces show the same numbers and must show them the same way, so the
// rendering lives here rather than being written once per surface.
package format

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// None is what an absent value looks like. It reads as "nothing to see" where
// a zero would read as a measurement.
const None = "—"

// Bytes renders a byte count compactly. Zero reads as no traffic at all.
func Bytes(n int64) string {
	if n == 0 {
		return None
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGT"[exp])
}

// Age renders how long ago something happened.
//
// The minutes and seconds are zero-padded so the column keeps a stable width,
// and a timestamp in the future — which a clock adjustment can produce — reads
// as zero rather than as a negative duration.
func Age(t, now time.Time) string {
	if t.IsZero() {
		return None
	}
	d := now.Sub(t).Round(time.Second)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// Rate renders a transfer rate.
//
// Zero reads as "0B/s" rather than as the absent-value marker: unlike a byte
// count, a rate of zero is something that was measured.
func Rate(bytesPerSecond float64) string {
	n := int64(bytesPerSecond)
	if n <= 0 {
		return "0B/s"
	}
	return Bytes(n) + "/s"
}

// DisplayLimit is how much of a string is worth putting on a line. Long enough
// for a hostname and a port, short enough that one value cannot own the screen.
const DisplayLimit = 96

// Display makes a string safe to put on a terminal.
//
// Three different things, and each is a different kind of damage:
//
//   - Control characters are removed. An escape sequence in a value is a value
//     that moves the cursor, clears the screen, or repaints a row that says
//     something else - and this program prints values it was given, from
//     configuration files and from the output of commands it runs. A terminal
//     is an interpreter, and everything it is handed is a program until proved
//     otherwise.
//   - The characters that reorder text are removed: the bidirectional
//     overrides and isolates, and the invisible marks that go with them. They
//     are how "moc.elpmaxe" is drawn as "example.com", which matters here
//     because most of what this shows is somewhere traffic goes.
//   - What is left is cut to DisplayLimit runes, on a rune boundary, with an
//     ellipsis. A ten thousand character name is not a lie, but it takes the
//     table apart.
//
// It is not an escape and not a quote: what comes out is meant to be read, not
// parsed back. Anything that needs the original value - a name sent on the
// wire, a path opened - must use the original.
func Display(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			// A tab is the one control character with a defensible meaning in a
			// value, and it still cannot be allowed to shift a column.
			return ' '
		case unicode.IsControl(r):
			return -1
		case r >= 0x200E && r <= 0x200F, // left-to-right and right-to-left marks
			r >= 0x202A && r <= 0x202E, // embeddings and overrides
			r >= 0x2066 && r <= 0x2069: // isolates
			return -1
		}
		return r
	}, s)

	runes := []rune(cleaned)
	if len(runes) <= DisplayLimit {
		return cleaned
	}
	// The ellipsis is part of the budget, so the result never exceeds it.
	return string(runes[:DisplayLimit-1]) + "…"
}

// OrNone replaces an empty string with the absent-value marker.
func OrNone(s string) string {
	if s == "" {
		return None
	}
	return s
}
