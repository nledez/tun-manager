package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/fsx"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

// serving starts a server on a temporary socket and stops it when the test
// ends. The interval is short so no test waits on a real second.
//
// Anything a test needs to change on the server goes in a tweak, applied
// before Serve starts: once the accept loop is running, writing a field is a
// data race that -race will find.
func serving(t *testing.T, sampler Sampler, tweaks ...func(*Server)) *Server {
	t.Helper()

	s := &Server{
		Path:     socketPath(t),
		Sampler:  sampler,
		Version:  "v0.0.0-test",
		Interval: 5 * time.Millisecond,
	}
	for _, tweak := range tweaks {
		tweak(s)
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after the context was cancelled")
		}
	})
	return s
}

// conn is a client connection with a line reader on it.
type conn struct {
	net.Conn
	lines *bufio.Scanner
}

func dial(t *testing.T, s *Server) *conn {
	t.Helper()

	c, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	// Every read is bounded: a test that hangs waiting for a line nobody sends
	// is a test that has to be killed by the suite timeout.
	if err := c.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	return &conn{Conn: c, lines: bufio.NewScanner(c)}
}

// next reads one message and returns it decoded into a map, which is what a
// consumer in another language sees.
func (c *conn) next(t *testing.T) map[string]any {
	t.Helper()

	if !c.lines.Scan() {
		t.Fatalf("no line: %v", c.lines.Err())
	}
	var msg map[string]any
	if err := json.Unmarshal(c.lines.Bytes(), &msg); err != nil {
		t.Fatalf("decode %q: %v", c.lines.Bytes(), err)
	}
	return msg
}

func (c *conn) send(t *testing.T, line string) {
	t.Helper()

	if _, err := c.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write %s: %v", line, err)
	}
}

func aView(names ...string) app.View {
	v := app.View{Taken: taken}
	for _, n := range names {
		v.Rows = append(v.Rows, app.Row{
			Tunnel: wgconf.Tunnel{Name: n, Endpoint: n + ".example:51820"},
			Health: wg.Up,
			Peer:   wg.Peer{Device: "utun7", LastHandshake: taken},
		})
	}
	return v
}

func TestAClientIsGreetedWithTheSchemaItMustUnderstand(t *testing.T) {
	c := dial(t, serving(t, nil))

	msg := c.next(t)

	if msg["type"] != "hello" {
		t.Fatalf("first line = %v, want hello", msg)
	}
	if msg["schema"] != float64(Schema) {
		t.Errorf("schema = %v, want %d", msg["schema"], Schema)
	}
	if msg["version"] != "v0.0.0-test" {
		t.Errorf("version = %v, want the server's", msg["version"])
	}
}

func TestAPublishedViewReachesEveryClient(t *testing.T) {
	s := serving(t, nil)
	first, second := dial(t, s), dial(t, s)
	first.next(t)
	second.next(t)

	s.Publish(aView("alpha"))

	for i, c := range []*conn{first, second} {
		msg := c.next(t)
		if msg["type"] != "state" {
			t.Fatalf("client %d got %v, want state", i, msg)
		}
		tunnels, _ := msg["tunnels"].([]any)
		if len(tunnels) != 1 {
			t.Errorf("client %d got %v, want one tunnel", i, msg["tunnels"])
		}
	}
}

func TestAClientArrivingLateIsToldWhatIsAlreadyKnown(t *testing.T) {
	// A menu bar opened after the program started must not sit blank until the
	// next refresh, which is five minutes away by default.
	s := serving(t, nil)
	s.Publish(aView("alpha", "bravo"))

	c := dial(t, s)

	if msg := c.next(t); msg["type"] != "hello" {
		t.Fatalf("first line = %v, want hello", msg)
	}
	msg := c.next(t)
	if msg["type"] != "state" {
		t.Fatalf("second line = %v, want the state already known", msg)
	}
	if tunnels, _ := msg["tunnels"].([]any); len(tunnels) != 2 {
		t.Errorf("tunnels = %v, want both", msg["tunnels"])
	}
}

func TestAClientArrivingBeforeAnyViewGetsOnlyHello(t *testing.T) {
	// There is nothing to say yet, and an empty state would read as "no
	// tunnels" rather than "not known yet".
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)

	s.Publish(aView("alpha"))

	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("second line = %v, want the first real state", msg)
	}
}

func TestShuttingDownSaysGoodbye(t *testing.T) {
	// A client has to tell a publisher that quit from one that crashed: one is
	// "tun-manager is not running", the other is worth retrying immediately.
	s := &Server{Path: socketPath(t), Interval: 5 * time.Millisecond}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	c := dial(t, s)
	c.next(t)
	cancel()

	if msg := c.next(t); msg["type"] != "bye" {
		t.Errorf("last line = %v, want bye", msg)
	}
	if err := <-done; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

func TestServeRemovesTheSocketOnItsWayOut(t *testing.T) {
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	dial(t, s)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if _, err := net.Dial("unix", s.Path); err == nil {
		t.Error("the socket still accepts connections after Serve returned")
	}
}

func TestServeWithoutListenIsAnError(t *testing.T) {
	// Rather than a nil dereference three frames deep.
	if err := (&Server{Path: "/nonexistent"}).Serve(context.Background()); err == nil {
		t.Error("Serve without Listen succeeded")
	}
}

func TestALineThatMakesNoSenseDoesNotEndTheConnection(t *testing.T) {
	// It is how a newer application talks to an older publisher, not an attack.
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)

	c.send(t, "this is not json")
	c.send(t, `{"type":"nonsense"}`)
	s.Publish(aView("alpha"))

	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("got %v, want the connection still serving", msg)
	}
}

func TestAClientThatHangsUpIsForgotten(t *testing.T) {
	// Publishing to a closed connection must not wedge the publisher.
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)
	c.Close()

	// Two publishes: the first discovers the connection is gone, the second
	// proves the server carried on.
	s.Publish(aView("alpha"))
	s.Publish(aView("alpha", "bravo"))

	other := dial(t, s)
	if msg := other.next(t); msg["type"] != "hello" {
		t.Errorf("got %v, want the server still accepting", msg)
	}
}

func TestServeReportsAnAcceptFailureThatIsNotAShutdown(t *testing.T) {
	// Accept can fail for a reason other than the context being cancelled -
	// closing the raw listener out from under Serve, rather than through ctx, is
	// what tells the two apart.
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("Serve = nil, want the accept failure reported")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the listener closed")
	}
}

func TestAnAcceptFailureAlsoSaysGoodbye(t *testing.T) {
	// Serve derives its own cancellable context and cancels it on every return
	// path, including a real accept failure. Without that, the ctx watcher
	// goroutine stays parked on the caller's context forever and shutdown
	// never runs, leaving an already-connected client talking to a server
	// that has quietly stopped rather than told to go away.
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background()) }()

	c := dial(t, s)
	c.next(t) // hello

	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve = nil, want the accept failure reported")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the listener closed")
	}

	if msg := c.next(t); msg["type"] != "bye" {
		t.Errorf("last line = %v, want bye", msg)
	}
}

func TestAddClosesAConnectionArrivingAfterShutdown(t *testing.T) {
	// A connection can be accepted in the moment between shutdown marking the
	// server closed and Accept noticing the listener is gone; add must not
	// register it.
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close()

	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	// A pipe has no credentials to read, and the feed refuses what it cannot
	// identify. What this test is about is what happens after that check.
	asking(t, func(net.Conn) (int, error) { return os.Getuid(), nil })
	server, peer := net.Pipe()
	defer peer.Close()

	s.add(server)

	if _, err := peer.Write([]byte("x")); err == nil {
		t.Error("write to the peer succeeded, want add to have closed its end")
	}

	s.mu.Lock()
	n := len(s.clients)
	s.mu.Unlock()
	if n != 0 {
		t.Errorf("clients = %d, want none registered", n)
	}
}

func TestAddRejectsAConnectionWhileShutdownIsClosing(t *testing.T) {
	// shutdown clears s.clients and marks the server closing in its first
	// critical section, well before Close() sets s.closed at the very end. A
	// connection Accept hands to add inside that window must still be turned
	// away - otherwise it is registered and greeted by a server that has
	// already walked past it and will never say goodbye.
	s := &Server{}
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()

	// A pipe has no credentials to read, and the feed refuses what it cannot
	// identify. What this test is about is what happens after that check.
	asking(t, func(net.Conn) (int, error) { return os.Getuid(), nil })
	server, peer := net.Pipe()
	defer peer.Close()

	s.add(server)

	if _, err := peer.Write([]byte("x")); err == nil {
		t.Error("write to the peer succeeded, want add to have closed its end")
	}

	s.mu.Lock()
	n := len(s.clients)
	s.mu.Unlock()
	if n != 0 {
		t.Errorf("clients = %d, want none registered", n)
	}
}

func TestSendToOnAClientAlreadyGoneIsANoop(t *testing.T) {
	// The read loop's drop can win the race against a publish already headed
	// for the same client. sendTo must find it missing and do nothing, rather
	// than queue onto a channel nobody drains.
	s := &Server{}
	c := &client{out: make(chan any, sendQueue)}

	s.sendTo(c, helloMsg{Type: "hello"})

	select {
	case <-c.out:
		t.Error("message queued for a client the server does not track")
	default:
	}
}

func TestSendToDropsAClientThatFellBehind(t *testing.T) {
	// A queue with no room left means the client cannot keep up; it is dropped
	// rather than the publisher blocking on it.
	server, peer := net.Pipe()
	defer peer.Close()
	c := &client{conn: server, out: make(chan any, 1)}
	c.out <- helloMsg{Type: "hello"} // fill the one slot

	s := &Server{clients: map[*client]struct{}{c: {}}}
	s.sendTo(c, byeMsg{Type: "bye"})

	s.mu.Lock()
	_, live := s.clients[c]
	s.mu.Unlock()
	if live {
		t.Error("client still registered after falling behind")
	}

	<-c.out // the message that was already queued
	if _, ok := <-c.out; ok {
		t.Error("out channel still open after the client was dropped")
	}
}

func TestShutdownDoesNotBlockOnAClientThatFellBehind(t *testing.T) {
	// The goodbye is best-effort: a client already at its queue limit must not
	// wedge shutdown for everyone else.
	server, peer := net.Pipe()
	defer peer.Close()
	c := &client{conn: server, out: make(chan any, 1)}
	c.out <- helloMsg{Type: "hello"} // fill the one slot

	s := &Server{Path: socketPath(t), clients: map[*client]struct{}{c: {}}}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		s.shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked on a client that had fallen behind")
	}
}

func TestShutdownClosesAConnectionThatNeverReads(t *testing.T) {
	// A client that stops reading leaves the writer's Encode blocked in a
	// write syscall on a synchronous connection: net.Pipe makes any write
	// block until a matching read consumes it, and this peer never reads.
	// Without a deadline that goroutine, and the queued bye, never come back.
	// The deadline shutdown sets is what forces the write to fail and the
	// connection to close underneath it.
	s := &Server{}
	server, peer := net.Pipe()
	defer peer.Close()

	c := &client{conn: server, out: make(chan any, sendQueue)}
	s.clients = map[*client]struct{}{c: {}}
	go c.write()

	// Set before shutdown, not after: once the fix closes the connection,
	// setting a deadline on the peer's end fails outright rather than doing
	// nothing. This is the safety net against a regression hanging the suite.
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}

	s.shutdown()

	// Wait past byeGrace without reading: reading any earlier would pair with
	// the still-pending write and let it succeed, which is not what this test
	// is checking. By now the fixed code has already forced the write to fail
	// on its deadline and closed the connection; the read below observes that
	// rather than racing it.
	time.Sleep(500 * time.Millisecond)

	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("read succeeded, want the connection already closed")
	}
}

func TestServeDoesNotReturnUntilShutdownHasFinished(t *testing.T) {
	// Serve's own ctx watcher used to run shutdown() in a goroutine nobody
	// waited for, so Serve (and whoever called it) could carry on before the
	// clients had been told goodbye and the socket removed. remove is the
	// last thing shutdown does through Close, so blocking it pins exactly the
	// window Serve must not return inside.
	var calls int32
	unblock := make(chan struct{})
	blocked := make(chan struct{})
	previousRemove := fsx.Remove
	fsx.Remove = func(path string) error {
		// Listen removes nothing on a fresh path, so the first call here is the
		// one shutdown makes through Close - which is the window Serve must not
		// return inside.
		if atomic.AddInt32(&calls, 1) > 1 {
			return previousRemove(path)
		}
		close(blocked)
		<-unblock
		return previousRemove(path)
	}
	t.Cleanup(func() { fsx.Remove = previousRemove })
	s := &Server{
		Path: socketPath(t),
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	cancel()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown never reached the blocked remove")
	}

	select {
	case err := <-done:
		t.Fatalf("Serve returned (err=%v) before shutdown's remove finished", err)
	case <-time.After(100 * time.Millisecond):
		// Still running, which is what this test is pinning.
	}

	close(unblock)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v, want nil once shutdown finished", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after shutdown finished")
	}
}
