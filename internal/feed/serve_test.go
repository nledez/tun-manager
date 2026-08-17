package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
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
