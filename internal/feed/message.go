// Package feed publishes tunnel state over a unix socket, so that a program
// with no privileges can show it.
//
// The exchange, in full:
//
//	S→C  {"type":"hello","schema":2,"version":"…","pubkey":"<base64, 32 bytes>"}
//	C→S  {"type":"challenge","nonce":"<base64, 32 bytes the client invented>"}
//	S→C  {"type":"auth","nonce":"<the same>","signature":"<base64, 64 bytes>"}
//
// and then state, sample, ping and bye as they happen, in whatever order they
// happen. A client may ask its question at any point or never; the publisher
// sends what it has either way, because there is nothing on this socket worth
// hiding from somebody who is already allowed to read it. What the question is
// for is the other direction: the client deciding whether the thing at the end
// of the socket is the tun-manager it pinned.
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
//
// Two, because what a connection means changed. A client can now ask the
// publisher to prove which one it is, and a client that pins a key needs to
// know it is talking to something that can be asked. Left at one, a publisher
// with no idea what a challenge is would look exactly like one refusing to
// answer, and an application would have to choose between refusing every older
// publisher and accepting anything that stayed quiet.
const Schema = 2

// helloMsg is the first line on every connection.
type helloMsg struct {
	Type    string `json:"type"`
	Schema  int    `json:"schema"`
	Version string `json:"version"`
	// PublicKey is the half of the feed key that can be shown, base64. Left out
	// when there is no key rather than sent empty: a client can then say there
	// is none instead of showing the fingerprint of nothing.
	PublicKey string `json:"pubkey,omitempty"`
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

// pingMsg is a round of probes. A list rather than a map keyed by tunnel: it
// arrives in the order the view holds, and JSON object keys have none.
type pingMsg struct {
	Type    string      `json:"type"`
	Results []wire.Ping `json:"results"`
}

// refusedMsg is the last line to a client the publisher will not hold, and the
// only message that carries a reason. A client that reconnects forever against
// a publisher that will not have it is a client whose author has no way of
// finding out why.
type refusedMsg struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// byeMsg is the last line before the publisher goes away, so that a client can
// tell a shutdown from a crash.
type byeMsg struct {
	Type string `json:"type"`
}

// clientMsg is anything a client sends. One shape for all of them: the
// vocabulary is five verbs wide and carries a name or a nonce, never both.
type clientMsg struct {
	Type   string `json:"type"`
	Tunnel string `json:"tunnel,omitempty"`
	// Nonce is the thirty-two bytes a client invents for a challenge, base64.
	Nonce string `json:"nonce,omitempty"`
}

// authMsg answers a challenge: the nonce that was asked, and a signature over
// what the publisher is - its schema, its version and the socket it is bound
// to - together with that nonce.
//
// The nonce comes back so a client with two challenges in flight knows which
// answer is which, and because checking a signature means knowing what was
// signed.
type authMsg struct {
	Type      string `json:"type"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}
