// Package wire is the JSON vocabulary tun-manager speaks to anything outside
// itself: `status --json` on one side, the status feed on the other.
//
// It exists so there is one definition rather than two. The same view rendered
// two ways by two packages is two things to keep in step, and they drift.
//
// These types are not app.View with tags. Marshalling internal structs onto a
// wire turns every refactor into a breaking protocol change, and it is how a
// field nobody meant to publish gets published.
package wire

import (
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/wg"
)

// Context is the network the machine is on.
type Context struct {
	Name      string `json:"name"`
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address,omitempty"`
}

// Tunnel is one tunnel, seen from outside.
//
// Health is the authority on whether the tunnel is carrying anything. The
// counters are always present, zero included: a consumer draws blank because
// the tunnel is down, not because a field went missing.
type Tunnel struct {
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

// Ping is one probe of a tunnel's check address.
//
// Keyed by tunnel rather than by address: the publisher already knows which
// address belongs to which tunnel, and making every consumer redo that mapping
// is how the two drift apart.
type Ping struct {
	Tunnel string `json:"tunnel"`
	// Milliseconds. Absent when the probe failed, because zero is a
	// measurement and "no answer" is not.
	RTT float64 `json:"rtt_ms,omitempty"`
	// Why it failed, when it did.
	Error string `json:"error,omitempty"`
}

// View is the whole picture at one instant.
type View struct {
	Context Context   `json:"context"`
	Taken   time.Time `json:"taken"`
	Tunnels []Tunnel  `json:"tunnels"`
}

// Of renders a view for the wire.
func Of(v app.View) View {
	out := View{
		Context: Context{
			Name:      v.Context.Name,
			Interface: v.Context.Interface,
			Address:   v.Context.Address,
		},
		Taken: v.Taken,
		// Made rather than declared: a nil slice marshals as null, and a
		// decoder that gets null where it expects a list is a decoder that
		// crashes on a machine with no tunnels configured.
		Tunnels: make([]Tunnel, 0, len(v.Rows)),
	}

	for _, r := range v.Rows {
		t := Tunnel{
			Name:     r.Tunnel.Name,
			Group:    r.Group,
			Health:   r.Health.String(),
			Device:   r.Peer.Device,
			Endpoint: Endpoint(r),
			CheckIP:  r.Tunnel.CheckIP,
			RxBytes:  r.Peer.RxBytes,
			TxBytes:  r.Peer.TxBytes,
		}
		if !r.Peer.LastHandshake.IsZero() {
			t.LastHandshake = r.Peer.LastHandshake.Format(time.RFC3339)
		}
		out.Tunnels = append(out.Tunnels, t)
	}
	return out
}

// PingsOf pairs a round of probes with the tunnels they were run for.
//
// Only tunnels that were actually probed appear. A tunnel that is down, or that
// has no check address, was never asked — and an entry for it would read as a
// probe that found nothing.
func PingsOf(view app.View, results map[string]probe.Result) []Ping {
	out := make([]Ping, 0, len(results))
	for _, row := range view.Rows {
		result, probed := results[row.Tunnel.CheckIP]
		if row.Tunnel.CheckIP == "" || !probed {
			continue
		}
		ping := Ping{Tunnel: row.Tunnel.Name}
		if result.Err != nil {
			ping.Error = result.Err.Error()
		} else {
			ping.RTT = float64(result.RTT) / float64(time.Millisecond)
		}
		out = append(out, ping)
	}
	return out
}

// Endpoint prefers the live endpoint, which is the resolved one, and falls back
// to the configured one for tunnels that are down.
func Endpoint(r app.Row) string {
	if r.Health != wg.Down && r.Peer.Endpoint != "" {
		return r.Peer.Endpoint
	}
	return r.Tunnel.Endpoint
}
