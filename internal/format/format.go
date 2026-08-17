// Package format renders the values the CLI table and the TUI table share.
//
// Both surfaces show the same numbers and must show them the same way, so the
// rendering lives here rather than being written once per surface.
package format

import (
	"fmt"
	"time"
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

// OrNone replaces an empty string with the absent-value marker.
func OrNone(s string) string {
	if s == "" {
		return None
	}
	return s
}
