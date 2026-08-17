package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

var taken = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

func TestAViewCarriesItsContextAndEveryTunnel(t *testing.T) {
	got := Of(app.View{
		Context: netctx.Context{Name: "office", Interface: "en0", Address: "198.51.100.42"},
		Taken:   taken,
		Rows: []app.Row{{
			Tunnel: wgconf.Tunnel{Name: "alpha", Endpoint: "alpha.example:51820", CheckIP: "192.0.2.1"},
			Group:  "needed",
			Health: wg.Up,
			Peer:   wg.Peer{Device: "utun7", LastHandshake: taken, RxBytes: 2048, TxBytes: 512},
		}},
	})

	if got.Context.Name != "office" || got.Context.Interface != "en0" {
		t.Errorf("context = %+v, want the one from the view", got.Context)
	}
	if len(got.Tunnels) != 1 {
		t.Fatalf("tunnels = %+v, want one", got.Tunnels)
	}
	tun := got.Tunnels[0]
	if tun.Name != "alpha" || tun.Group != "needed" || tun.Health != "up" {
		t.Errorf("tunnel = %+v, want alpha/needed/up", tun)
	}
	if tun.Device != "utun7" || tun.RxBytes != 2048 || tun.TxBytes != 512 {
		t.Errorf("tunnel = %+v, want the live counters", tun)
	}
	if tun.LastHandshake != taken.Format(time.RFC3339) {
		t.Errorf("last_handshake = %q, want RFC 3339", tun.LastHandshake)
	}
}

func TestATunnelThatNeverHandshookHasNoHandshake(t *testing.T) {
	// An empty string is omitted from the JSON; a zero timestamp would render
	// as the year 1, which reads like a real reading.
	got := Of(app.View{Rows: []app.Row{{
		Tunnel: wgconf.Tunnel{Name: "charlie"},
		Health: wg.Down,
	}}})

	if got.Tunnels[0].LastHandshake != "" {
		t.Errorf("last_handshake = %q, want nothing", got.Tunnels[0].LastHandshake)
	}
}

func TestHealthIsWhatSaysATunnelIsCarryingNothing(t *testing.T) {
	// Counters are always present, zero included: the consumer draws blank
	// because the tunnel is down, not because a field went missing.
	out, err := json.Marshal(Of(app.View{Rows: []app.Row{{
		Tunnel: wgconf.Tunnel{Name: "charlie", Endpoint: "charlie.example:51820"},
		Health: wg.Down,
	}}}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"health":"down"`, `"rx_bytes":0`, `"tx_bytes":0`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("%s missing from %s", want, out)
		}
	}
}

func TestTheLiveEndpointWinsOverTheConfiguredOne(t *testing.T) {
	// wg-quick resolves a DNS endpoint; the resolved one is what traffic uses.
	got := Endpoint(app.Row{
		Tunnel: wgconf.Tunnel{Endpoint: "alpha.example:51820"},
		Health: wg.Up,
		Peer:   wg.Peer{Endpoint: "192.0.2.10:51820"},
	})

	if got != "192.0.2.10:51820" {
		t.Errorf("endpoint = %q, want the resolved one", got)
	}
}

func TestADownTunnelKeepsItsConfiguredEndpoint(t *testing.T) {
	got := Endpoint(app.Row{
		Tunnel: wgconf.Tunnel{Endpoint: "charlie.example:51820"},
		Health: wg.Down,
		Peer:   wg.Peer{Endpoint: "192.0.2.10:51820"},
	})

	if got != "charlie.example:51820" {
		t.Errorf("endpoint = %q, want the configured one", got)
	}
}

func TestAViewWithNoRowsMarshalsAsAnEmptyListNotNull(t *testing.T) {
	// A decoder that gets null where it expects a list is a decoder that
	// crashes on a machine with no tunnels configured.
	out, err := json.Marshal(Of(app.View{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"tunnels":[]`) {
		t.Errorf("got %s, want an empty list", out)
	}
}
