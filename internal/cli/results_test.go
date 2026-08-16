package cli

import (
	"errors"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/wg"
)

func TestWriteResultsReportsSuccess(t *testing.T) {
	var out strings.Builder

	err := WriteResults(&out, []wg.Result{{Tunnel: "alpha", Action: "up"}})
	if err != nil {
		t.Fatalf("WriteResults: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "up alpha") || !strings.Contains(got, "ok") {
		t.Errorf("output = %q, want it to report `up alpha ok`", got)
	}
}

func TestWriteResultsReportsASkippedPreCheck(t *testing.T) {
	var out strings.Builder

	if err := WriteResults(&out, []wg.Result{{Tunnel: "delta", Action: "up", Skipped: true}}); err != nil {
		t.Fatalf("WriteResults: %v", err)
	}

	if !strings.Contains(out.String(), "already reachable") {
		t.Errorf("output = %q, want it to explain the skip", out.String())
	}
}

func TestWriteResultsReturnsAnErrorWhenAnOperationFailed(t *testing.T) {
	var out strings.Builder
	results := []wg.Result{
		{Tunnel: "alpha", Action: "up"},
		{Tunnel: "bravo", Action: "up", Err: errors.New("exit status 1"), Output: "resolvconf not found"},
	}

	err := WriteResults(&out, results)

	if err == nil {
		t.Fatal("WriteResults returned nil, want an error when an operation failed")
	}
	if !strings.Contains(out.String(), "resolvconf not found") {
		t.Errorf("output = %q, want the command output included", out.String())
	}
}

func TestWriteResultsSaysNothingHappenedOnAnEmptyRun(t *testing.T) {
	var out strings.Builder

	if err := WriteResults(&out, nil); err != nil {
		t.Fatalf("WriteResults: %v", err)
	}

	if out.String() == "" {
		t.Error("output is empty, want an explicit no-op message")
	}
}

// failingWriter refuses every write, like a closed pipe.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriteResultsReportsAWriteFailure(t *testing.T) {
	// `tun-manager down --all | head` closes the pipe early; the exit code must
	// not claim success.
	boom := errors.New("broken pipe")

	err := WriteResults(failingWriter{err: boom}, []wg.Result{{Tunnel: "alpha", Action: "up"}})

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestWriteResultsReportsAWriteFailureOnAnEmptyRun(t *testing.T) {
	boom := errors.New("broken pipe")

	if err := WriteResults(failingWriter{err: boom}, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}
