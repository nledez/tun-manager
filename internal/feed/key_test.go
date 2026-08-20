package feed

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateSeedIsThirtyTwoBytesOfBase64(t *testing.T) {
	seed, err := GenerateSeed(nil)
	if err != nil {
		t.Fatalf("GenerateSeed: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(seed)
	if err != nil {
		t.Fatalf("the seed is not base64: %v", err)
	}
	if len(raw) != SeedLen {
		t.Errorf("seed is %d bytes, want %d", len(raw), SeedLen)
	}
}

func TestTwoGeneratedSeedsDiffer(t *testing.T) {
	// The one property that matters and that a constant would satisfy every
	// other test: a key generated twice must not be the same key twice.
	first, err := GenerateSeed(nil)
	if err != nil {
		t.Fatalf("GenerateSeed: %v", err)
	}
	second, err := GenerateSeed(nil)
	if err != nil {
		t.Fatalf("GenerateSeed: %v", err)
	}

	if first == second {
		t.Error("two generated seeds are identical, so the randomness is not")
	}
}

func TestGenerateSeedReportsARandomSourceThatFails(t *testing.T) {
	// A short read is the failure that matters: it would yield a key with
	// fewer bits than it claims, and nothing downstream could tell.
	if _, err := GenerateSeed(bytes.NewReader([]byte("too short"))); err == nil {
		t.Fatal("GenerateSeed accepted a random source that ran out")
	}
}

func TestTheFingerprintIsStableForASeed(t *testing.T) {
	// Fixed seed, fixed answer: the fingerprint is what a person compares
	// between `tun-manager feed-key` and the application's About window, so it
	// must not depend on anything but the key.
	const seed = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

	first, err := FingerprintOfSeed(seed)
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}
	second, err := FingerprintOfSeed(seed)
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}

	if first != second {
		t.Errorf("the same seed gave %q then %q", first, second)
	}
	// Readable out loud: pairs of hex digits, separated, and short enough to
	// compare by eye. Sixteen bytes is what ssh settled on for the same job.
	if got := strings.Count(first, ":"); got != 15 {
		t.Errorf("fingerprint %q has %d separators, want 15", first, got)
	}
	if len(first) != 47 {
		t.Errorf("fingerprint %q is %d characters, want 47", first, len(first))
	}
}

func TestDifferentSeedsHaveDifferentFingerprints(t *testing.T) {
	mine, err := FingerprintOfSeed("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}
	theirs, err := FingerprintOfSeed("HwEBAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}

	if mine == theirs {
		t.Error("two different keys share a fingerprint, so it identifies nothing")
	}
}

func TestASeedThatIsNotAKeyIsRefused(t *testing.T) {
	for name, seed := range map[string]string{
		"empty":      "",
		"not base64": "not base64 at all!",
		"too short":  base64.StdEncoding.EncodeToString([]byte("sixteen bytes...")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FingerprintOfSeed(seed); err == nil {
				t.Errorf("FingerprintOfSeed(%q) succeeded, want a refusal", seed)
			}
		})
	}
}

func TestTheFingerprintNeverContainsTheSeed(t *testing.T) {
	// The fingerprint is printed, logged and shown in a window. It is derived
	// from the public half, so it cannot carry the private one - and this is
	// the test that keeps it that way.
	const seed = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

	got, err := FingerprintOfSeed(seed)
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}

	if strings.Contains(got, seed) || strings.Contains(got, "AAECAwQ") {
		t.Errorf("the fingerprint %q carries the seed", got)
	}
}
