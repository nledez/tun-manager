package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain puts a stub of each notification tool on PATH, so that no test in
// this package can reach the desktop.
//
// Notifier.Binary already lets a test point at a script of its own, and every
// test here does. The problem is what happens when one does not: the notifier
// falls through to whichever tool is installed and posts a real notification
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

// stubTools writes a harmless stand-in for each tool and puts them on PATH,
// under both names the resolver tries.
func stubTools() (string, error) {
	dir, err := os.MkdirTemp("", "notify-stubs")
	if err != nil {
		return "", err
	}

	for _, name := range []string{preferred, fallbackName} {
		script := filepath.Join(dir, name)
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			os.RemoveAll(dir) //nolint:errcheck // the error being returned is the one that matters
			return "", err
		}
	}

	// Replacing PATH rather than prepending: a stub in front of the real tool
	// would still leave the real one reachable by absolute path from a test
	// that built one.
	if err := os.Setenv("PATH", dir); err != nil {
		os.RemoveAll(dir) //nolint:errcheck // the error being returned is the one that matters
		return "", err
	}
	return dir, nil
}
