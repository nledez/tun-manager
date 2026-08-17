// Package feed publishes tunnel state over a unix socket, so that a program
// with no privileges can show it.
//
// Everything here is read-only by construction: no message a client can send
// starts or stops a tunnel. That is what keeps the socket cheap to reason
// about — there is no authorisation to design, because there is nothing to
// authorise.
package feed

import (
	"time"

	"ledez.net/tun-manager/internal/wire"
)

// Schema is the version of the wire contract. It changes only when an existing
// field changes meaning or disappears; adding a field does not bump it, because
// a client that has never heard of a field ignores it.
const Schema = 1

// helloMsg is the first line on every connection.
type helloMsg struct {
	Type    string `json:"type"`
	Schema  int    `json:"schema"`
	Version string `json:"version"`
}

// stateMsg is a whole view. The view is embedded rather than nested: one JSON
// object per line, with the type as one more field of it.
type stateMsg struct {
	Type string `json:"type"`
	wire.View
}

// sampleMsg is one reading of a watched tunnel's cumulative counters.
type sampleMsg struct {
	Type   string    `json:"type"`
	Tunnel string    `json:"tunnel"`
	At     time.Time `json:"at"`
	Rx     int64     `json:"rx"`
	Tx     int64     `json:"tx"`
}

// byeMsg is the last line before the publisher goes away, so that a client can
// tell a shutdown from a crash.
type byeMsg struct {
	Type string `json:"type"`
}

// clientMsg is anything a client sends. There is one shape for all of them:
// the vocabulary is three verbs wide and will not grow a payload.
type clientMsg struct {
	Type   string `json:"type"`
	Tunnel string `json:"tunnel,omitempty"`
}
