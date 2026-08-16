// Package cli renders the non-interactive output of tun-manager.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/format"
	"ledez.net/tun-manager/internal/wg"
)

type jsonTunnel struct {
	Name          string `json:"name"`
	Group         string `json:"group"`
	Health        string `json:"health"`
	Device        string `json:"device,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	CheckIP       string `json:"check_ip,omitempty"`
	LastHandshake string `json:"last_handshake,omitempty"`
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
}

type jsonView struct {
	Context struct {
		Name      string `json:"name"`
		Interface string `json:"interface,omitempty"`
		Address   string `json:"address,omitempty"`
	} `json:"context"`
	Taken   time.Time    `json:"taken"`
	Tunnels []jsonTunnel `json:"tunnels"`
}

// WriteStatus renders a view, as an aligned table or as JSON.
func WriteStatus(w io.Writer, view app.View, asJSON bool) error {
	if asJSON {
		return writeJSON(w, view)
	}
	return writeTable(w, view)
}

func writeJSON(w io.Writer, view app.View) error {
	out := jsonView{Taken: view.Taken}
	out.Context.Name = view.Context.Name
	out.Context.Interface = view.Context.Interface
	out.Context.Address = view.Context.Address

	out.Tunnels = make([]jsonTunnel, 0, len(view.Rows))
	for _, r := range view.Rows {
		t := jsonTunnel{
			Name:     r.Tunnel.Name,
			Group:    r.Group,
			Health:   r.Health.String(),
			Device:   r.Peer.Device,
			Endpoint: endpoint(r),
			CheckIP:  r.Tunnel.CheckIP,
			RxBytes:  r.Peer.RxBytes,
			TxBytes:  r.Peer.TxBytes,
		}
		if !r.Peer.LastHandshake.IsZero() {
			t.LastHandshake = r.Peer.LastHandshake.Format(time.RFC3339)
		}
		out.Tunnels = append(out.Tunnels, t)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
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
			format.OrNone(endpoint(r)),
		)
	}
	return tw.Flush()
}

// endpoint prefers the live endpoint, which is the resolved one, and falls back
// to the configured one for tunnels that are down.
func endpoint(r app.Row) string {
	if r.Health != wg.Down && r.Peer.Endpoint != "" {
		return r.Peer.Endpoint
	}
	return r.Tunnel.Endpoint
}
