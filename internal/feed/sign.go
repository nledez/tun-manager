package feed

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
)

// NonceLen is how many bytes a client's challenge must carry.
//
// Thirty-two, drawn by the client and never by the publisher: what a signature
// proves is that whoever made it holds the key *now*, and that only follows if
// the thing signed is something the client has just invented. A publisher that
// chose the nonce would be a publisher whose old signatures could be replayed
// at it by anybody who kept one.
const NonceLen = 32

// domain is the first field of everything this key signs.
//
// It says which protocol the signature belongs to, so that a signature made
// here cannot be presented as one made for something else that happens to sign
// with the same key. Ed25519 signs bytes and has no idea what they mean; saying
// what they mean is the caller's job.
const domain = "tun-manager-feed-v1"

// separator goes between the fields. A nul byte, because it cannot appear in a
// version, a path or a decimal number: without one, a version ending in what
// the path begins with would sign the same bytes as a shorter version and a
// longer path, and two different publishers would produce one signature.
const separator = 0

// SignedMessage is what a publisher proves it can sign.
//
// Every field is in it for a reason. The schema and the version, so a signature
// cannot be lifted from a publisher speaking an older protocol. The socket
// path, so a relay listening somewhere else cannot forward a challenge to the
// real publisher and pass its answer off as its own — the answer names the path
// it came from, and the client compares that with the one it dialled. The
// nonce, so that answer is good once.
func SignedMessage(schema int, version, path string, nonce []byte) []byte {
	message := []byte(domain)
	for _, field := range [][]byte{
		[]byte(strconv.Itoa(schema)),
		[]byte(version),
		[]byte(path),
		nonce,
	} {
		message = append(message, separator)
		message = append(message, field...)
	}
	return message
}

// Sign answers a challenge, base64-encoded the way it goes on the wire.
func Sign(seed string, schema int, version, path string, nonce []byte) (string, error) {
	raw, err := seedBytes(seed)
	if err != nil {
		return "", err
	}
	key := ed25519.NewKeyFromSeed(raw)
	signature := ed25519.Sign(key, SignedMessage(schema, version, path, nonce))
	return base64.StdEncoding.EncodeToString(signature), nil
}

// Nonce decodes what a client sent, refusing anything that is not the right
// size.
//
// A short nonce is a client with less randomness than it claims, and there is
// no reason to sign one: the vocabulary has exactly one shape here, and
// anything else is a client this publisher cannot talk to.
func Nonce(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("the nonce is not base64")
	}
	if len(raw) != NonceLen {
		return nil, fmt.Errorf("the nonce is %d bytes, want %d", len(raw), NonceLen)
	}
	return raw, nil
}
