package feed

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
)

// fatView makes a state message big enough that the kernel's socket buffer
// cannot quietly swallow the whole backlog. Without it a "client that never
// reads" is absorbed by the buffer and never falls behind at all, and the test
// passes for the wrong reason.
func fatView() app.View {
	names := make([]string, 0, 40)
	for i := range 40 {
		names = append(names, fmt.Sprintf("tunnel-%02d", i))
	}
	return aView(names...)
}

// publishUntil publishes until the server is down to want clients. It is two
// assertions at once: a Publish that blocks never returns, and a client that is
// never dropped never brings the count down.
func publishUntil(t *testing.T, s *Server, want int) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for s.clientCount() > want && time.Now().Before(deadline) {
			s.Publish(fatView())
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Publish blocked on a client that never reads")
	}
	if got := s.clientCount(); got != want {
		t.Fatalf("clients = %d, want %d", got, want)
	}
}

func TestAClientThatNeverReadsIsDroppedRatherThanWaitedFor(t *testing.T) {
	// The program that manages the tunnels must not block on the program that
	// draws them. Sixteen messages is several seconds of sampling; a client
	// further behind than that is not coming back.
	s := serving(t, nil)

	silent, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer silent.Close()

	publishUntil(t, s, 0)
}

func TestDroppingOneClientLeavesTheOthersServed(t *testing.T) {
	s := serving(t, nil)

	silent, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer silent.Close()

	// Create a reader connection that will actively read messages.
	readerConn := dial(t, s)
	readerConn.next(t) // consume hello

	// In a separate goroutine, have the reader actively consume messages
	// to keep its buffer drained.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			readerConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)) //nolint:errcheck
			if !readerConn.lines.Scan() {
				return
			}
		}
	}()

	// Publish large messages until the silent client is dropped.
	publishUntil(t, s, 1)

	// Give the reader a moment to drain any final messages.
	time.Sleep(50 * time.Millisecond)

	// Stop the reader goroutine.
	readerConn.Close()
	<-readerDone

	// The point is that publishing didn't block and we dropped one client while keeping the other.
	// This is verified by publishUntil succeeding and ending with 1 client.
}

func TestPublishingWithNobodyConnectedIsHarmless(t *testing.T) {
	s := serving(t, nil)

	s.Publish(aView("alpha"))

	if got := s.clientCount(); got != 0 {
		t.Errorf("clients = %d, want none", got)
	}
}

func TestAViewPublishedBeforeServeIsStillRemembered(t *testing.T) {
	// Publish is safe on a server whose Serve has not started: the composition
	// root starts them in the order it likes.
	s := &Server{Path: socketPath(t)}

	s.Publish(app.View{})

	if !s.haveView {
		t.Error("the view was not remembered")
	}
}

func TestListenFailsWhenRemoveFailsBeforeBind(t *testing.T) {
	// If removing a stale socket fails for a reason other than "not exists",
	// Listen fails and does not leave a half-built socket behind.
	testErr := errors.New("permission denied")
	s := &Server{
		Path: socketPath(t),
		remove: func(string) error {
			return testErr
		},
	}

	err := s.Listen()

	if err == nil || !errors.Is(err, testErr) {
		t.Errorf("Listen = %v, want the remove error", err)
	}
	if _, err := os.Stat(s.Path); err == nil {
		t.Error("socket exists after Listen failed, want no leftover")
	}
}

func TestListenFailsWhenChmodFails(t *testing.T) {
	// If chmod fails after the socket is bound, Listen fails and removes the
	// socket before returning, leaving no half-built socket behind.
	testErr := errors.New("permission denied")
	s := &Server{
		Path: socketPath(t),
		chmod: func(string, os.FileMode) error {
			return testErr
		},
	}

	err := s.Listen()

	if err == nil || !errors.Is(err, testErr) {
		t.Errorf("Listen = %v, want the chmod error", err)
	}
	if _, err := os.Stat(s.Path); err == nil {
		t.Error("socket exists after Listen failed, want no leftover")
	}
}

func TestCloseReturnsRemoveError(t *testing.T) {
	// If Close's remove fails, Close returns the remove error. The socket
	// was successfully closed (listener closed), only the cleanup failed.
	s := &Server{Path: socketPath(t)}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	testErr := errors.New("I/O error")
	s.remove = func(string) error {
		return testErr
	}

	err := s.Close()

	if err == nil || !errors.Is(err, testErr) {
		t.Errorf("Close = %v, want the remove error", err)
	}
}
