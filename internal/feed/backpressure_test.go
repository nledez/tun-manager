package feed

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
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
//
// send publishes one view and reports whether the clients that are meant to
// survive kept up with it. A publisher running flat out outruns any reader on a
// machine with no spare core, and the queue is only sixteen deep, so a test
// that does not hold the publisher back drops the client it is watching for
// losing a race rather than for the policy under test.
func publishUntil(t *testing.T, s *Server, want int, send func() bool) {
	t.Helper()

	// Written on the publishing goroutine and read after done is closed, which
	// is what orders the two.
	kept := true
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for s.clientCount() > want && time.Now().Before(deadline) {
			if !send() {
				kept = false
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Publish blocked on a client that never reads")
	}
	if !kept {
		t.Fatal("a client that was reading stopped taking messages off the wire")
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

	// net.Dial returns once the connection is established, which is before the
	// accept loop has registered it. Publishing at that point sees no clients,
	// does nothing, and then fails when the client turns up a moment later.
	eventually(t, "the silent client to be registered", func() bool {
		return s.clientCount() == 1
	})

	// Nobody here is meant to survive, so there is nothing to hold back for.
	publishUntil(t, s, 0, func() bool {
		s.Publish(fatView())
		return true
	})
}

func TestDroppingOneClientLeavesTheOthersServed(t *testing.T) {
	s := serving(t, nil)

	silent, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial silent: %v", err)
	}
	defer silent.Close()

	attentive, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial attentive: %v", err)
	}
	defer attentive.Close()

	// The attentive client has to be drained for as long as the publishing
	// lasts. fatView is sized to defeat the kernel's socket buffer, so a
	// client nobody reads fills up whether or not it is the one under test,
	// and both would be dropped for reasons that have nothing to do with the
	// policy.
	//
	// One deadline for the whole drain rather than one per line: bufio.Scanner
	// latches its first error permanently, so a single timeout mid-loop would
	// end this goroutine silently and bring back the very flake it exists to
	// prevent.
	if err := attentive.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}

	var states atomic.Int64
	started := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		lines := bufio.NewScanner(attentive)

		// Read the hello before signalling. Closing a channel on the way into
		// the loop would only prove this goroutine was scheduled; having taken
		// a line off the connection proves it is reading, which is what the
		// burst below needs to be true.
		if !lines.Scan() {
			close(started)
			return
		}
		close(started)

		for lines.Scan() {
			var msg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(lines.Bytes(), &msg) == nil && msg.Type == "state" {
				states.Add(1)
			}
		}
	}()

	<-started

	// Waiting for the attentive client's hello says nothing about whether the
	// silent one has been registered yet; without this, publishUntil can
	// equally start against a count of 1 and finish against 2.
	eventually(t, "both clients to be registered", func() bool {
		return s.clientCount() == 2
	})

	publishUntil(t, s, 1, func() bool {
		// Snapshot before publishing: reading the count afterwards could
		// already include the message we are about to wait for, and the wait
		// would then be for one that is never sent.
		seen := states.Load()
		s.Publish(fatView())
		return waitFor(func() bool { return states.Load() > seen })
	})

	attentive.Close()
	<-drained

	// publishUntil already proved the attentive client survived. This proves it
	// was still being served rather than merely still counted.
	if states.Load() == 0 {
		t.Error("the attentive client received no state at all")
	}
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
