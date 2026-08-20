package feed

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestASignatureVerifiesAgainstThePublishedKey(t *testing.T) {
	// The whole exchange in one line: what the publisher signs is what the
	// application checks with the key it pinned.
	nonce := make([]byte, NonceLen)
	nonce[0] = 7
	pub, err := PublicKeyOfSeed(knownSeed)
	if err != nil {
		t.Fatalf("PublicKeyOfSeed: %v", err)
	}

	signature, err := Sign(knownSeed, 2, "v0.6.0", "/var/run/tun-manager.sock", nonce)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("the signature is not base64: %v", err)
	}
	message := SignedMessage(2, "v0.6.0", "/var/run/tun-manager.sock", nonce)
	if !ed25519.Verify(pub, message, raw) {
		t.Error("the signature does not verify against the published key")
	}
}

func TestEveryPartOfWhatIsSignedChangesTheSignature(t *testing.T) {
	// Each field is in there for a reason, and a field that made no difference
	// would be a field somebody could change.
	nonce := make([]byte, NonceLen)
	base := SignedMessage(2, "v0.6.0", "/var/run/tun-manager.sock", nonce)

	other := make([]byte, NonceLen)
	other[31] = 1
	for name, changed := range map[string][]byte{
		"the schema":  SignedMessage(3, "v0.6.0", "/var/run/tun-manager.sock", nonce),
		"the version": SignedMessage(2, "v0.7.0", "/var/run/tun-manager.sock", nonce),
		"the path":    SignedMessage(2, "v0.6.0", "/tmp/somewhere-else.sock", nonce),
		"the nonce":   SignedMessage(2, "v0.6.0", "/var/run/tun-manager.sock", other),
	} {
		t.Run(name, func(t *testing.T) {
			if string(changed) == string(base) {
				t.Errorf("%s does not change what is signed", name)
			}
		})
	}
}

func TestWhatIsSignedSaysWhatItIsFor(t *testing.T) {
	// A signature that could be replayed into another protocol is a signature
	// this key did not mean to make. The first field says which protocol, and
	// the separator is a byte that cannot appear in any of the others.
	message := SignedMessage(2, "v0.6.0", "/var/run/tun-manager.sock", make([]byte, NonceLen))

	if !strings.HasPrefix(string(message), "tun-manager-feed-v1\x00") {
		t.Errorf("what is signed does not name the protocol: %q", message)
	}
}

func TestTheFieldsCannotBeSlidIntoOneAnother(t *testing.T) {
	// Without a separator, a version ending in the path's first characters
	// would sign the same bytes as a shorter version and a longer path.
	withVersion := SignedMessage(2, "v0.6.0/var/run", "/x.sock", make([]byte, NonceLen))
	withPath := SignedMessage(2, "v0.6.0", "/var/run/x.sock", make([]byte, NonceLen))

	if string(withVersion) == string(withPath) {
		t.Error("two different publishers sign the same bytes")
	}
}

func TestSigningWithSomethingThatIsNotAKeyFails(t *testing.T) {
	if _, err := Sign("not a key", 2, "v0.6.0", "/var/run/tun-manager.sock", nil); err == nil {
		t.Error("Sign produced a signature from something that is not a key")
	}
}
