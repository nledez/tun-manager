package wg

import (
	"net"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func mustKey(t *testing.T, s string) wgtypes.Key {
	t.Helper()
	k, err := wgtypes.ParseKey(s)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", s, err)
	}
	return k
}

// Invented keys and addresses; the addresses come from the ranges reserved for
// documentation (RFC 5737).
const (
	alphaKey = "JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g="
	bravoKey = "SldkcX6LmKWyv8zZ5vMADRonNEFOW2h1go+cqbbD0N0="
)

func TestSnapshotReadsAPeer(t *testing.T) {
	handshake := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	devices := []*wgtypes.Device{{
		Name: "utun7",
		Peers: []wgtypes.Peer{{
			PublicKey:         mustKey(t, alphaKey),
			Endpoint:          &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 51820},
			LastHandshakeTime: handshake,
			ReceiveBytes:      1234,
			TransmitBytes:     567,
		}},
	}}

	peer, ok := Snapshot(devices).ByPublicKey(alphaKey)
	if !ok {
		t.Fatalf("peer %q missing from the snapshot", alphaKey)
	}

	if peer.Device != "utun7" {
		t.Errorf("Device = %q, want %q", peer.Device, "utun7")
	}
	if peer.Endpoint != "192.0.2.10:51820" {
		t.Errorf("Endpoint = %q", peer.Endpoint)
	}
	if !peer.LastHandshake.Equal(handshake) {
		t.Errorf("LastHandshake = %v, want %v", peer.LastHandshake, handshake)
	}
	if peer.RxBytes != 1234 || peer.TxBytes != 567 {
		t.Errorf("Rx/Tx = %d/%d, want 1234/567", peer.RxBytes, peer.TxBytes)
	}
}

func TestSnapshotCoversEveryDevice(t *testing.T) {
	devices := []*wgtypes.Device{
		{Name: "utun7", Peers: []wgtypes.Peer{{PublicKey: mustKey(t, alphaKey)}}},
		{Name: "utun8", Peers: []wgtypes.Peer{{PublicKey: mustKey(t, bravoKey)}}},
	}

	state := Snapshot(devices)

	if len(state) != 2 {
		t.Fatalf("len(state) = %d, want 2", len(state))
	}
	if peer, _ := state.ByDevice("utun8"); peer.PublicKey != bravoKey {
		t.Errorf("utun8 public key = %q, want %q", peer.PublicKey, bravoKey)
	}
}

func TestSnapshotKeepsTwoDevicesSharingAPublicKey(t *testing.T) {
	// A map keyed by public key would silently drop one of these.
	devices := []*wgtypes.Device{
		{Name: "utun4", Peers: []wgtypes.Peer{{PublicKey: mustKey(t, alphaKey)}}},
		{Name: "utun9", Peers: []wgtypes.Peer{{PublicKey: mustKey(t, alphaKey)}}},
	}

	state := Snapshot(devices)

	if len(state) != 2 {
		t.Fatalf("len(state) = %d, want 2", len(state))
	}
	if _, ok := state.ByDevice("utun9"); !ok {
		t.Error("utun9 missing, want both devices kept")
	}
}

func TestSnapshotToleratesMissingEndpoint(t *testing.T) {
	devices := []*wgtypes.Device{{
		Name:  "utun7",
		Peers: []wgtypes.Peer{{PublicKey: mustKey(t, alphaKey)}},
	}}

	peer, _ := Snapshot(devices).ByPublicKey(alphaKey)

	if peer.Endpoint != "" {
		t.Errorf("Endpoint = %q, want empty when the peer has none", peer.Endpoint)
	}
}

func TestByDeviceMissesAnUnknownInterface(t *testing.T) {
	state := Snapshot(nil)

	if _, ok := state.ByDevice("utun7"); ok {
		t.Error("ByDevice found a peer in an empty state")
	}
}

func TestPeerHealthIsUpWithinStaleWindow(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	peer := Peer{LastHandshake: now.Add(-30 * time.Second)}

	if got := peer.Health(now, StaleAfter); got != Up {
		t.Errorf("Health = %v, want %v", got, Up)
	}
}

func TestPeerHealthIsStaleBeyondWindow(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	peer := Peer{LastHandshake: now.Add(-10 * time.Minute)}

	if got := peer.Health(now, StaleAfter); got != Stale {
		t.Errorf("Health = %v, want %v", got, Stale)
	}
}

func TestPeerHealthIsStaleWhenHandshakeNeverHappened(t *testing.T) {
	// The interface exists but no handshake ever completed: the tunnel is up
	// from wg-quick's point of view yet carries no traffic.
	if got := (Peer{}).Health(time.Now(), StaleAfter); got != Stale {
		t.Errorf("Health = %v, want %v", got, Stale)
	}
}

func TestHealthStringsAreStable(t *testing.T) {
	for health, want := range map[Health]string{Down: "down", Up: "up", Stale: "stale"} {
		if got := health.String(); got != want {
			t.Errorf("Health(%d).String() = %q, want %q", health, got, want)
		}
	}
}

// nameLocator answers like a populated /var/run/wireguard.
type nameLocator map[string]string

func (l nameLocator) Device(tunnel string) (string, bool) { return l[tunnel], true }

type blindLocator struct{}

func (blindLocator) Device(string) (string, bool) { return "", false }

func TestResolvePrefersTheLocator(t *testing.T) {
	// Two configs may share a public key; only the interface separates them.
	state := State{
		{PublicKey: alphaKey, Device: "utun4"},
		{PublicKey: alphaKey, Device: "utun9"},
	}
	loc := nameLocator{"delta": "utun9"}

	peer, ok := state.Resolve("delta", alphaKey, loc)

	if !ok {
		t.Fatal("Resolve found nothing, want the utun9 peer")
	}
	if peer.Device != "utun9" {
		t.Errorf("Device = %q, want %q", peer.Device, "utun9")
	}
}

func TestResolveReportsDownWhenTheLocatorHasNoInterface(t *testing.T) {
	state := State{{PublicKey: alphaKey, Device: "utun4"}}
	loc := nameLocator{"delta": "utun4"} // delta6 is absent from the locator

	if _, ok := state.Resolve("delta6", alphaKey, loc); ok {
		t.Error("Resolve found a peer for a tunnel with no interface of its own")
	}
}

func TestResolveFallsBackToThePublicKey(t *testing.T) {
	state := State{{PublicKey: alphaKey, Device: "utun4"}}

	peer, ok := state.Resolve("alpha", alphaKey, blindLocator{})

	if !ok {
		t.Fatal("Resolve found nothing, want the public key fallback to hit")
	}
	if peer.Device != "utun4" {
		t.Errorf("Device = %q, want %q", peer.Device, "utun4")
	}
}

func TestResolveWithoutALocatorUsesThePublicKey(t *testing.T) {
	state := State{{PublicKey: alphaKey, Device: "utun4"}}

	if _, ok := state.Resolve("alpha", alphaKey, nil); !ok {
		t.Error("Resolve found nothing with a nil locator, want the public key used")
	}
}

func TestByPublicKeyMissesAnUnknownKey(t *testing.T) {
	state := State{{PublicKey: alphaKey, Device: "utun4"}}

	if _, ok := state.ByPublicKey(bravoKey); ok {
		t.Error("ByPublicKey found a peer that is not there")
	}
}

func TestResolveMissesWhenTheLocatorNamesADeadInterface(t *testing.T) {
	// The name file survives a crash that took the interface with it.
	state := State{{PublicKey: alphaKey, Device: "utun4"}}
	loc := nameLocator{"alpha": "utun9"}

	if _, ok := state.Resolve("alpha", alphaKey, loc); ok {
		t.Error("Resolve found a peer on an interface that is gone")
	}
}
