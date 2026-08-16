// Package wg reads and drives the live WireGuard state.
//
// Reading is pure Go through wgctrl, which talks to the userspace UAPI sockets
// under /var/run/wireguard. Those sockets are root-only, which is why the whole
// program runs under sudo.
package wg

import (
	"fmt"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// StaleAfter is how long a tunnel may go without a handshake before it is
// considered stale. WireGuard rekeys every two minutes while traffic flows.
const StaleAfter = 3 * time.Minute

// Health is the coarse state of a tunnel.
type Health int

const (
	// Down means no live device carries the tunnel.
	Down Health = iota
	// Up means the device exists and handshook recently.
	Up
	// Stale means the device exists but has not handshook lately: the tunnel is
	// nominally up yet nothing is getting through.
	Stale
)

func (h Health) String() string {
	switch h {
	case Up:
		return "up"
	case Stale:
		return "stale"
	default:
		return "down"
	}
}

// Peer is the live state of one peer on one device.
type Peer struct {
	// PublicKey identifies the remote end. It is not unique across tunnels:
	// two configs reaching the same server through different endpoints share it.
	PublicKey string
	// Device is the interface carrying the peer, e.g. "utun7".
	Device string
	// Endpoint is the resolved peer endpoint, which may differ from the
	// configured one when the config used a DNS name.
	Endpoint      string
	LastHandshake time.Time
	RxBytes       int64
	TxBytes       int64
}

// Health reports the state of a live peer. It never returns Down: a peer that
// is in the state at all has an interface.
func (p Peer) Health(now time.Time, staleAfter time.Duration) Health {
	if p.LastHandshake.IsZero() || now.Sub(p.LastHandshake) > staleAfter {
		return Stale
	}
	return Up
}

// State is every live peer, in device order.
//
// It is a list rather than a map because peer public keys are not unique: two
// configs pointing at the same server (an IPv4 and an IPv6 endpoint, say) carry
// the same key, and a map would silently merge them.
type State []Peer

// Snapshot converts wgctrl devices into a state.
func Snapshot(devices []*wgtypes.Device) State {
	var state State
	for _, dev := range devices {
		for _, peer := range dev.Peers {
			p := Peer{
				PublicKey:     peer.PublicKey.String(),
				Device:        dev.Name,
				LastHandshake: peer.LastHandshakeTime,
				RxBytes:       peer.ReceiveBytes,
				TxBytes:       peer.TransmitBytes,
			}
			if peer.Endpoint != nil {
				p.Endpoint = peer.Endpoint.String()
			}
			state = append(state, p)
		}
	}
	return state
}

// ByDevice returns the peer carried by an interface.
func (s State) ByDevice(device string) (Peer, bool) {
	for _, p := range s {
		if p.Device == device {
			return p, true
		}
	}
	return Peer{}, false
}

// ByPublicKey returns the first peer with that public key.
func (s State) ByPublicKey(key string) (Peer, bool) {
	for _, p := range s {
		if p.PublicKey == key {
			return p, true
		}
	}
	return Peer{}, false
}

// Resolve finds the live peer of a tunnel.
//
// The locator is consulted first, because it maps a tunnel name to the exact
// interface wg-quick gave it — the only way to tell apart two configs that
// share a peer public key. Matching by public key is the fallback for when the
// locator cannot answer.
func (s State) Resolve(tunnel, peerPublicKey string, loc Locator) (Peer, bool) {
	if loc != nil {
		if device, authoritative := loc.Device(tunnel); authoritative {
			if device == "" {
				return Peer{}, false
			}
			return s.ByDevice(device)
		}
	}
	return s.ByPublicKey(peerPublicKey)
}

// Reader returns the live WireGuard state.
type Reader interface {
	Read() (State, error)
}

// CtrlReader reads the state through wgctrl.
type CtrlReader struct {
	client *wgctrl.Client
}

// NewReader opens the WireGuard control client. It fails when the process
// cannot reach the UAPI sockets, which in practice means it is not root.
func NewReader() (*CtrlReader, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("open wireguard control socket (are you root?): %w", err)
	}
	return &CtrlReader{client: c}, nil
}

// Read returns the current state of every live device.
func (r *CtrlReader) Read() (State, error) {
	devices, err := r.client.Devices()
	if err != nil {
		return nil, fmt.Errorf("list wireguard devices: %w", err)
	}
	return Snapshot(devices), nil
}

// Close releases the control client.
func (r *CtrlReader) Close() error { return r.client.Close() }
