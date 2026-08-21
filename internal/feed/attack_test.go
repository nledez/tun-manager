package feed

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"
)

// The tests in this file are named after the attack rather than after the
// function, because what they are worth is the sentence in the name. A test
// called TestSign is a test somebody deletes when Sign is refactored; a test
// called "a signature is only good for the nonce it answers" is a test that has
// to keep being true.

// verifies reports whether a base64 signature is one the key made over that
// message. The client does this in Swift; doing it here in Go is what keeps the
// two from drifting apart without anybody noticing.
func verifies(t *testing.T, seed, signature string, schema int, version, path string, nonce []byte) bool {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString(seed)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("signature: %v", err)
	}
	pub, ok := ed25519.NewKeyFromSeed(raw).Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("the key this package generates is not an ed25519 public key")
	}
	return ed25519.Verify(pub, SignedMessage(schema, version, path, nonce), sig)
}

func TestASignatureIsOnlyGoodForTheNonceItAnswers(t *testing.T) {
	// Somebody who recorded an earlier session has a real signature made by the
	// real key. Presenting it to a client that asked a different question is the
	// whole of a replay attack, and it fails because the question is in what was
	// signed.
	answered, err := Sign(knownSeed, Schema, "v1", "/run/x.sock", aNonce(1))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if !verifies(t, knownSeed, answered, Schema, "v1", "/run/x.sock", aNonce(1)) {
		t.Fatal("the answer does not verify against the question it answered")
	}
	if verifies(t, knownSeed, answered, Schema, "v1", "/run/x.sock", aNonce(2)) {
		t.Error("an answer to one nonce verified against another: a recording would be enough")
	}
}

func TestASignatureIsOnlyGoodForTheSocketItWasMadeFor(t *testing.T) {
	// The relay: something listens on one socket, forwards the challenge to the
	// real publisher, and hands back the genuine answer. The answer names the
	// path it was made for, and the client compares it with the one it dialled.
	answered, err := Sign(knownSeed, Schema, "v1", "/run/real.sock", aNonce(1))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if verifies(t, knownSeed, answered, Schema, "v1", "/tmp/relay.sock", aNonce(1)) {
		t.Error("an answer made for one socket verified for another: a relay would be believed")
	}
	// The schema and the version are in there for the same reason, and
	// TestEveryPartOfWhatIsSignedChangesTheSignature walks every field of the
	// message rather than the two attacks that motivated them.
}

func TestTheOnlyThingAClientCanGetSignedIsThirtyTwoBytesItChose(t *testing.T) {
	// The shape of a signing oracle is "sign this for me", and the defence is
	// that there is nothing worth having in what comes back: a fixed domain, the
	// publisher's own schema and version, its own socket path, and exactly
	// NonceLen bytes. Nothing a client sends reaches any other field.
	s := serving(t, nil, func(s *Server) { s.FeedKey = knownSeed })
	c := dial(t, s)
	c.next(t)

	for _, nonce := range []string{
		base64.StdEncoding.EncodeToString(aNonce(1)[:NonceLen-1]),
		base64.StdEncoding.EncodeToString(append(aNonce(1), 0)),
		"",
		"not base64 at all!",
	} {
		c.send(t, `{"type":"challenge","nonce":"`+nonce+`"}`)
	}
	s.Publish(aView("alpha"))

	if got := c.next(t)["type"]; got != "state" {
		t.Errorf("next line = %v, want every malformed challenge to have been ignored", got)
	}
}

func TestAClientCannotUseThePublisherAsASigningOracle(t *testing.T) {
	// One signature a second, per client. Asking again straight away gets
	// silence rather than a queue, so the cost of a client that asks in a loop
	// is bounded by the clock and not by how fast it can write.
	s := serving(t, nil, func(s *Server) { s.FeedKey = knownSeed })
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"challenge","nonce":"`+base64.StdEncoding.EncodeToString(aNonce(1))+`"}`)
	if got := c.next(t)["type"]; got != "auth" {
		t.Fatalf("first answer = %v, want auth", got)
	}
	for i := range 8 {
		c.send(t, `{"type":"challenge","nonce":"`+base64.StdEncoding.EncodeToString(aNonce(byte(i+2)))+`"}`)
	}
	s.Publish(aView("alpha"))

	if got := c.next(t)["type"]; got != "state" {
		t.Errorf("next line = %v, want every further challenge dropped", got)
	}
}

func TestTheFloorOnSigningIsNotAWayToSilenceEverybodyElse(t *testing.T) {
	// The other half of that rule: a client asking once a second must not be
	// able to keep every other client from ever being answered. What one client
	// spends is its own.
	s := serving(t, nil, func(s *Server) {
		s.FeedKey = knownSeed
		s.ChallengeFloor = time.Hour
	})
	greedy := dial(t, s)
	greedy.next(t)
	greedy.send(t, `{"type":"challenge","nonce":"`+base64.StdEncoding.EncodeToString(aNonce(1))+`"}`)
	greedy.next(t)

	honest := dial(t, s)
	honest.next(t)
	honest.send(t, `{"type":"challenge","nonce":"`+base64.StdEncoding.EncodeToString(aNonce(2))+`"}`)

	if got := honest.next(t)["type"]; got != "auth" {
		t.Errorf("answer = %v, want auth: another client's question is not this one's", got)
	}
}
