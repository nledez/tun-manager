package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/fsx"
	"ledez.net/tun-manager/internal/profile"
)

// A seed of a known shape, so a test can name the fingerprint it expects
// without computing one by hand.
const knownSeed = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

// aPrivilegedFile lays out the root-only configuration the way init-privileged
// leaves it, and returns it loaded.
func aPrivilegedFile(t *testing.T, seed string) *profile.Privileged {
	t.Helper()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	path := filepath.Join(dir, "tun-manager.yaml")
	body := "wg_quick: /usr/bin/wg-quick\n"
	if seed != "" {
		body += "feed_key: \"" + seed + "\"\n"
	}
	if err := os.WriteFile(path, []byte(body), TunnelFileMode); err != nil {
		t.Fatalf("write: %v", err)
	}
	ownedBy(t, 0)

	priv, err := profile.LoadPrivileged(path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}
	return priv
}

func TestFeedKeyPrintsTheFingerprintAndNotTheKey(t *testing.T) {
	// It is read out loud, pasted into issues and left on a screen. The
	// fingerprint is derived from the public half, which is what makes that
	// safe; the seed is what must never be beside it.
	priv := aPrivilegedFile(t, knownSeed)
	want, err := feed.FingerprintOfSeed(knownSeed)
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}
	var out strings.Builder

	if err := FeedKey(&out, priv); err != nil {
		t.Fatalf("FeedKey: %v", err)
	}

	if !strings.Contains(out.String(), want) {
		t.Errorf("output %q does not show the fingerprint", out.String())
	}
	if strings.Contains(out.String(), knownSeed) {
		t.Errorf("output %q prints the key itself", out.String())
	}
	if !strings.Contains(out.String(), priv.Path) {
		t.Errorf("output %q does not say which file it read", out.String())
	}
}

func TestFeedKeySaysWhenThereIsNoKeyAtAll(t *testing.T) {
	priv := aPrivilegedFile(t, "")

	err := FeedKey(&strings.Builder{}, priv)

	if err == nil {
		t.Fatal("FeedKey reported a key that is not there")
	}
	if !strings.Contains(err.Error(), "--rotate") {
		t.Errorf("error %q does not say how to get one", err)
	}
}

func TestFeedKeySaysWhenTheKeyIsNotOne(t *testing.T) {
	// Truncated by a copy and paste. The message names the problem and never
	// the value.
	priv := aPrivilegedFile(t, "bm90IGEga2V5")

	err := FeedKey(&strings.Builder{}, priv)

	if err == nil {
		t.Fatal("FeedKey reported a fingerprint of something that is not a key")
	}
	if strings.Contains(err.Error(), "bm90IGEga2V5") {
		t.Errorf("error %q prints what was in the file", err)
	}
}

func TestRotatingWritesANewKeyAndKeepsTheOldFile(t *testing.T) {
	// The old key is what every menu bar out there has pinned, and the file is
	// the only place it exists.
	priv := aPrivilegedFile(t, knownSeed)
	var out strings.Builder

	if err := RotateFeedKey(&out, priv, Assumed(true)); err != nil {
		t.Fatalf("RotateFeedKey: %v", err)
	}

	after, err := profile.LoadPrivileged(priv.Path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}
	if after.FeedKey.Reveal() == knownSeed {
		t.Error("the key did not change")
	}
	kept, err := os.ReadFile(priv.Path + rotateBackupSuffix)
	if err != nil {
		t.Fatalf("the previous configuration was not kept: %v", err)
	}
	if !strings.Contains(string(kept), knownSeed) {
		t.Error("what was kept does not hold the old key, which is the only copy of it")
	}
}

func TestRotatingShowsBothFingerprintsAndWhatItMeans(t *testing.T) {
	// Somebody has to compare the new one against what the application shows,
	// and know why the application is about to complain.
	priv := aPrivilegedFile(t, knownSeed)
	was, err := feed.FingerprintOfSeed(knownSeed)
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}
	var out strings.Builder

	if rotateErr := RotateFeedKey(&out, priv, Assumed(true)); rotateErr != nil {
		t.Fatalf("RotateFeedKey: %v", rotateErr)
	}

	report := out.String()
	if !strings.Contains(report, was) {
		t.Errorf("the report does not show the fingerprint being replaced:\n%s", report)
	}
	after, err := profile.LoadPrivileged(priv.Path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}
	now, err := feed.FingerprintOfSeed(after.FeedKey.Reveal())
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}
	if !strings.Contains(report, now) {
		t.Errorf("the report does not show the new fingerprint:\n%s", report)
	}
	if !strings.Contains(report, "pinned") {
		t.Errorf("the report does not say what the application will do:\n%s", report)
	}
	if strings.Contains(report, after.FeedKey.Reveal()) {
		t.Errorf("the report prints the new key itself:\n%s", report)
	}
}

func TestRotatingAsksBeforeItReplacesAnything(t *testing.T) {
	// It breaks every pinned connection there is. That is not something to do
	// because somebody typed a command with a typo in it.
	priv := aPrivilegedFile(t, knownSeed)

	err := RotateFeedKey(&strings.Builder{}, priv, Assumed(false))

	if err == nil {
		t.Fatal("the key was rotated without being agreed to")
	}
	after, loadErr := profile.LoadPrivileged(priv.Path)
	if loadErr != nil {
		t.Fatalf("LoadPrivileged: %v", loadErr)
	}
	if after.FeedKey.Reveal() != knownSeed {
		t.Error("the key changed anyway")
	}
	if _, statErr := os.Stat(priv.Path + rotateBackupSuffix); !os.IsNotExist(statErr) {
		t.Error("a copy was kept of a rotation that did not happen")
	}
}

func TestRotatingNamesTheKeyItIsAboutToReplace(t *testing.T) {
	// The question has to say which key, or agreeing to it means nothing.
	priv := aPrivilegedFile(t, knownSeed)
	was, err := feed.FingerprintOfSeed(knownSeed)
	if err != nil {
		t.Fatalf("FingerprintOfSeed: %v", err)
	}
	var question string

	if err := RotateFeedKey(&strings.Builder{}, priv, func(q string) (bool, error) {
		question = q
		return true, nil
	}); err != nil {
		t.Fatalf("RotateFeedKey: %v", err)
	}

	if !strings.Contains(question, was) {
		t.Errorf("the question %q does not name the key being replaced", question)
	}
}

func TestRotatingWithNoKeyToReplaceStillWrites(t *testing.T) {
	// A privileged file written before there was a key to put in it. Refusing
	// would leave the one case that needs a key without a way to get one.
	priv := aPrivilegedFile(t, "")

	if err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true)); err != nil {
		t.Fatalf("RotateFeedKey: %v", err)
	}

	after, err := profile.LoadPrivileged(priv.Path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}
	if after.FeedKey.Reveal() == "" {
		t.Error("no key was written")
	}
}

func TestRotatingSaysSoWhenTheOldKeyCannotBeRead(t *testing.T) {
	// Replacing something unreadable is exactly the case somebody rotates for.
	priv := aPrivilegedFile(t, "bm90IGEga2V5")
	var out strings.Builder

	if err := RotateFeedKey(&out, priv, Assumed(true)); err != nil {
		t.Fatalf("RotateFeedKey: %v", err)
	}

	if !strings.Contains(out.String(), "unreadable") {
		t.Errorf("the report does not say what was there:\n%s", out.String())
	}
}

func TestRotatingPassesOnAQuestionThatCouldNotBeAsked(t *testing.T) {
	priv := aPrivilegedFile(t, knownSeed)
	boom := errors.New("there is nobody to answer")

	err := RotateFeedKey(&strings.Builder{}, priv, func(string) (bool, error) { return false, boom })

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the reason it could not be asked", err)
	}
}

func TestRotatingReportsAKeyItCannotDraw(t *testing.T) {
	priv := aPrivilegedFile(t, knownSeed)
	previous := generateSeed
	generateSeed = func() (string, error) { return "", errSeam }
	t.Cleanup(func() { generateSeed = previous })

	if err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true)); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the failure to draw a key", err)
	}
}

func TestRotatingSaysWhereThePreviousFileWentWhenItCannotWrite(t *testing.T) {
	// The window between moving the old file aside and writing the new one.
	// Somebody left with neither needs to be told where the one that exists is.
	priv := aPrivilegedFile(t, knownSeed)
	previous := fsx.OpenFile
	opened := 0
	fsx.OpenFile = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		// The first open is the copy, which has to work: what is being covered
		// is the write that comes after it.
		opened++
		if opened == 1 {
			return previous(path, flag, mode)
		}
		return nil, errSeam
	}
	t.Cleanup(func() { fsx.OpenFile = previous })

	err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true))

	if err == nil {
		t.Fatal("RotateFeedKey reported success without writing")
	}
	if !strings.Contains(err.Error(), rotateBackupSuffix) {
		t.Errorf("error %q does not say where the previous configuration is", err)
	}
}

func TestRotatingReportsACopyItCannotMake(t *testing.T) {
	// Nowhere to keep the old key means nowhere to get it back from, and the
	// rotation is about to replace the only copy there is.
	priv := aPrivilegedFile(t, knownSeed)
	previous := fsx.ReadFile
	fsx.ReadFile = func(string) ([]byte, error) { return nil, errSeam }
	t.Cleanup(func() { fsx.ReadFile = previous })

	if err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true)); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the failure to keep a copy", err)
	}
}

func TestRotatingReportsACopyItCannotWrite(t *testing.T) {
	priv := aPrivilegedFile(t, knownSeed)
	previous := fsx.OpenFile
	fsx.OpenFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errSeam }
	t.Cleanup(func() { fsx.OpenFile = previous })

	if err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true)); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the failure to write the copy", err)
	}
}

func TestRotatingReportsACopyItCannotFinish(t *testing.T) {
	// The close is where a full disk arrives on a filesystem that buffers, and
	// a copy of a key that never landed is worse than no copy: it looks like
	// one.
	priv := aPrivilegedFile(t, knownSeed)
	previous := fsx.CloseFile
	fsx.CloseFile = func(f *os.File) error {
		_ = f.Close()
		return errSeam
	}
	t.Cleanup(func() { fsx.CloseFile = previous })

	if err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true)); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the failure to finish the copy", err)
	}
}

func TestRotatingReportsACopyItCannotCloseToOthers(t *testing.T) {
	priv := aPrivilegedFile(t, knownSeed)
	previous := fsx.Chmod
	fsx.Chmod = func(string, os.FileMode) error { return errSeam }
	t.Cleanup(func() { fsx.Chmod = previous })

	if err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true)); !errors.Is(err, errSeam) {
		t.Errorf("err = %v, want the failure to close the copy to others", err)
	}
}

func TestRotatingReportsACopyItCannotFinishWriting(t *testing.T) {
	// The disk filling up between the create and the write. A copy of a key
	// that never landed is worse than no copy: it looks like one.
	priv := aPrivilegedFile(t, knownSeed)
	previous := fsx.OpenFile
	fsx.OpenFile = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		f, err := previous(path, flag, mode)
		if err != nil {
			return nil, err
		}
		// Closed underneath, so the write that follows fails the way a full
		// disk does.
		_ = f.Close()
		return f, nil
	}
	t.Cleanup(func() { fsx.OpenFile = previous })

	if err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true)); err == nil {
		t.Error("RotateFeedKey reported success on a descriptor that was gone")
	}
}

func TestRotatingRefusesAKeyItCannotTakeAFingerprintOf(t *testing.T) {
	// The generator and the reader disagreeing about what a seed is. Writing
	// one nobody can take a fingerprint of would leave a publisher nobody can
	// verify — and would say so only when the menu bar failed to connect.
	priv := aPrivilegedFile(t, knownSeed)
	previous := generateSeed
	generateSeed = func() (string, error) { return "not a seed", nil }
	t.Cleanup(func() { generateSeed = previous })

	err := RotateFeedKey(&strings.Builder{}, priv, Assumed(true))

	if err == nil {
		t.Fatal("RotateFeedKey wrote a key it could not read back")
	}
	after, loadErr := profile.LoadPrivileged(priv.Path)
	if loadErr != nil {
		t.Fatalf("LoadPrivileged: %v", loadErr)
	}
	if after.FeedKey.Reveal() != knownSeed {
		t.Error("the unusable key was written anyway")
	}
}
