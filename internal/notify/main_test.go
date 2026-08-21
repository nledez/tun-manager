package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the notification command at a stub, so that no test in this
// package can reach the desktop.
//
// Notifier.Binary already lets a test point at a script of its own, and every
// test here does. The problem is what happens when one does not: it falls
// through to osascript and posts a real notification
// onto the screen of whoever is running the suite. Nothing fails, nothing is
// logged, and it is noticed only by the person it lands on — which on this
// project is the maintainer, running the suite under sudo beside a session
// that will happily display it.
//
// A default that has to be remembered is not a guarantee. This one does not.
func TestMain(m *testing.M) {
	dir, err := stubTools()
	if err != nil {
		fmt.Fprintln(os.Stderr, "notify: stub the notification tools:", err)
		os.Exit(1)
	}

	code := m.Run()

	// Not deferred: os.Exit does not run deferred calls.
	os.RemoveAll(dir) //nolint:errcheck // the suite is over either way
	os.Exit(code)
}

// stubTools writes a harmless stand-in and points the command at it.
func stubTools() (string, error) {
	dir, err := os.MkdirTemp("", "notify-stubs")
	if err != nil {
		return "", err
	}
	script := filepath.Join(dir, "osascript")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		os.RemoveAll(dir) //nolint:errcheck // the error being returned is the one that matters
		return "", err
	}
	OsascriptPath = script
	return dir, nil
}
