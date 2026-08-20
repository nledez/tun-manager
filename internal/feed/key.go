package feed

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SeedLen is the size of an Ed25519 seed, which is what the configuration
// stores. The private key itself is derived from it and never written down.
const SeedLen = ed25519.SeedSize

// fingerprintLen is how much of the digest a fingerprint shows.
//
// Sixteen bytes, which is what ssh settled on for the same job: enough that two
// keys will not collide in any population this program will ever see, short
// enough that somebody can compare it by eye against a window on the other side
// of the desk. A fingerprint nobody reads is a fingerprint nobody checks.
const fingerprintLen = 16

// GenerateSeed draws a new Ed25519 seed and returns it base64-encoded, the way
// the configuration stores it.
//
// The reader is a parameter so a test can hand it one that runs out: a short
// read would otherwise yield a key with fewer bits than it claims, and nothing
// downstream could tell. nil means crypto/rand.
func GenerateSeed(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}

	seed := make([]byte, SeedLen)
	if _, err := io.ReadFull(random, seed); err != nil {
		return "", fmt.Errorf("draw %d bytes for a feed key: %w", SeedLen, err)
	}
	return base64.StdEncoding.EncodeToString(seed), nil
}

// PublicKeyOfSeed returns the public half of the key a seed stands for.
func PublicKeyOfSeed(seed string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil {
		// The seed itself is never in the message: an error is the one place a
		// secret reaches a log line without anybody deciding it should.
		return nil, errors.New("the feed key is not base64")
	}
	if len(raw) != SeedLen {
		return nil, fmt.Errorf("the feed key is %d bytes, want %d", len(raw), SeedLen)
	}
	// The public half is the second half of the private key, which is what
	// Public() hands back. Taken directly rather than through a type assertion
	// on an interface: an assertion that cannot fail is a branch that cannot be
	// tested, and one that could fail would panic in a permission check.
	key := ed25519.NewKeyFromSeed(raw)
	return ed25519.PublicKey(key[ed25519.SeedSize:]), nil
}

// Fingerprint renders a public key as something a person can compare: the
// first bytes of its SHA-256, in hex, in pairs.
//
// It is derived from the public half, so printing it gives nothing away — which
// is the point, since it is meant to be printed, logged and shown in a window.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)

	parts := make([]string, fingerprintLen)
	for i, b := range sum[:fingerprintLen] {
		parts[i] = hex.EncodeToString([]byte{b})
	}
	return strings.Join(parts, ":")
}

// FingerprintOfSeed is the pair of the two above, which is what every caller
// wants: the configuration holds a seed and the report shows a fingerprint.
func FingerprintOfSeed(seed string) (string, error) {
	pub, err := PublicKeyOfSeed(seed)
	if err != nil {
		return "", err
	}
	return Fingerprint(pub), nil
}
