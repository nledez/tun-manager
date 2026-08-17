// Package cli renders the non-interactive output of tun-manager.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/format"
	"ledez.net/tun-manager/internal/wire"
)

// WriteStatus renders a view, as an aligned table or as JSON.
func WriteStatus(w io.Writer, view app.View, asJSON bool) error {
	if asJSON {
		return writeJSON(w, view)
	}
	return writeTable(w, view)
}

func writeJSON(w io.Writer, view app.View) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(wire.Of(view))
}

func writeTable(w io.Writer, view app.View) error {
	if _, err := fmt.Fprintf(w, "context: %s\n\n", view.Context); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	// tabwriter keeps the first write error and returns it from Flush.
	fmt.Fprintln(tw, "NAME\tGROUP\tSTATE\tDEVICE\tHANDSHAKE\tRX/TX\tENDPOINT") //nolint:errcheck
	for _, r := range view.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s/%s\t%s\n", //nolint:errcheck
			r.Tunnel.Name,
			format.OrNone(r.Group),
			r.Health,
			format.OrNone(r.Peer.Device),
			format.Age(r.Peer.LastHandshake, view.Taken),
			format.Bytes(r.Peer.RxBytes),
			format.Bytes(r.Peer.TxBytes),
			format.OrNone(wire.Endpoint(r)),
		)
	}
	return tw.Flush()
}
