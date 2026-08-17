package app

import (
	"time"

	"ledez.net/tun-manager/internal/wgconf"
)

// Sample is one reading of a tunnel's cumulative counters.
type Sample struct {
	At time.Time
	Rx int64
	Tx int64
}

// Sample reads the counters of one tunnel.
//
// It touches the WireGuard control socket and nothing else - no configuration
// from disk, no network detection - because a graph asks for this every second
// and View is far too much work to repeat at that rate.
//
// A tunnel that is down has no counters, which is a fact rather than a failure,
// and so is a socket that has gone away underneath: a graph missing a point
// does not need an error path of its own.
func (a *App) Sample(tun wgconf.Tunnel) (Sample, bool) {
	state, err := a.Reader.Read()
	if err != nil {
		return Sample{}, false
	}
	peer, ok := state.Resolve(tun.Name, tun.PeerPublicKey, a.locator())
	if !ok {
		return Sample{}, false
	}
	return Sample{At: a.now(), Rx: peer.RxBytes, Tx: peer.TxBytes}, true
}
