package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"ledez.net/tun-manager/internal/wg"
)

// WriteResults renders the outcome of a batch of up/down operations and returns
// an error when any of them failed, so the process exit code is meaningful.
//
// The report is built whole and written once: a partial write followed by a
// closed pipe would otherwise be reported as a success.
func WriteResults(w io.Writer, results []wg.Result) error {
	if len(results) == 0 {
		_, err := io.WriteString(w, "nothing to do\n")
		return err
	}

	var b strings.Builder
	var failed []string
	for _, r := range results {
		switch {
		case r.Skipped:
			fmt.Fprintf(&b, "%s %s: skipped, check address already reachable\n", r.Action, r.Tunnel)
		case r.Err != nil:
			failed = append(failed, r.Tunnel)
			fmt.Fprintf(&b, "%s %s: FAILED: %v\n", r.Action, r.Tunnel, r.Err)
			if out := strings.TrimSpace(r.Output); out != "" {
				fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(out, "\n", "\n  "))
			}
		default:
			fmt.Fprintf(&b, "%s %s: ok\n", r.Action, r.Tunnel)
		}
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	if len(failed) > 0 {
		return errors.New("failed: " + strings.Join(failed, ", "))
	}
	return nil
}
