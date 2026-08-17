package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/wgconf"
	"ledez.net/tun-manager/internal/wire"
)

const (
	// SocketMode is what the socket is chmodded to. The feed says which
	// tunnels exist and where they connect; it is for one person.
	SocketMode os.FileMode = 0o600

	// sendQueue is how many messages a client may fall behind by. Sixteen is
	// several seconds of sampling: a client that cannot keep up with one
	// message a second is not going to recover.
	sendQueue = 16

	// sampleInterval is how often a watched tunnel's counters are read.
	sampleInterval = time.Second //nolint:unused

	// refreshFloor is the shortest gap between two refreshes a client can ask
	// for. Without it a client could turn the menu bar into a way of hammering
	// the WireGuard control socket.
	refreshFloor = 2 * time.Second //nolint:unused

	// byeGrace is how long goodbye is worth waiting for. A client that cannot
	// take a handful of small messages over a local socket in that time has
	// stopped reading, and waiting on it forever costs a goroutine and a file
	// descriptor rather than delivering anything.
	byeGrace = 200 * time.Millisecond
)

// Sampler reads one tunnel's cumulative counters. *app.App satisfies it.
type Sampler interface {
	Sample(tun wgconf.Tunnel) (app.Sample, bool)
}

// Request is something a client asked for that the feed cannot do itself.
type Request struct{ Kind string }

// RequestRefresh asks whoever owns the refresh to take a fresh view.
const RequestRefresh = "refresh"

// Server publishes views and samples to whoever connects.
//
// The zero value is not usable: Path is required, and Listen must be called
// before Serve.
type Server struct {
	// Path is where the socket is bound.
	Path string
	// Owner is who the socket is handed to. When it is not demotable the
	// socket stays root-owned: there is no user session to serve.
	Owner privdrop.User
	// Sampler reads counters for watched tunnels.
	Sampler Sampler
	// Version is reported in the hello line.
	Version string

	// Interval between readings while a tunnel is watched. Zero means
	// sampleInterval; tests set it short so nothing waits on a real clock.
	Interval time.Duration
	// Now is the clock the refresh floor reads. Zero means time.Now.
	Now func() time.Time

	ln net.Listener

	mu          sync.Mutex
	closed      bool
	closing     bool
	clients     map[*client]struct{}
	view        app.View
	haveView    bool
	sampling    chan struct{}
	requests    chan Request
	lastRefresh time.Time

	// chmod and remove are the filesystem calls Listen and Close depend on.
	// They are fields so a test can make them fail: the paths where the
	// filesystem misbehaves between two operations are the ones a real
	// failure would take, and there is no other way to reach them.
	chmod  func(string, os.FileMode) error
	remove func(string) error
}

func (s *Server) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return sampleInterval
}

func (s *Server) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) chmodFn() func(string, os.FileMode) error {
	if s.chmod != nil {
		return s.chmod
	}
	return os.Chmod
}

func (s *Server) removeFn() func(string) error {
	if s.remove != nil {
		return s.remove
	}
	return os.Remove
}

// Requests yields what clients asked for that the feed cannot do itself. It is
// a Request rather than a bare signal so a second verb does not change every
// signature.
//
// The channel is buffered by one and written without blocking: a refresh that
// cannot be delivered is dropped, because the next one carries the same
// meaning and nothing guarantees anybody is listening.
func (s *Server) Requests() <-chan Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestsLocked()
}

func (s *Server) requestsLocked() chan Request {
	if s.requests == nil {
		s.requests = make(chan Request, 1)
	}
	return s.requests
}

// clientCount reports how many clients are connected. It exists for the tests:
// backpressure is only observable as a client that is no longer there.
func (s *Server) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Listen binds the socket and hands it to Owner. Call it before Serve.
//
// Any failure after the bind removes the socket again: a path left behind
// would look like a feed that works and serve nobody.
func (s *Server) Listen() error {
	// A socket left by a killed process makes bind fail with EADDRINUSE. Two
	// tun-manager processes cannot usefully coexist - they would both be
	// driving the same tunnels - so the path is ours to take.
	if err := s.removeFn()(s.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", s.Path, err)
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "unix", s.Path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.Path, err)
	}

	if err := s.chmodFn()(s.Path, SocketMode); err != nil {
		return s.abandon(ln, fmt.Errorf("chmod %s: %w", s.Path, err))
	}
	if s.Owner.Demotable {
		if err := os.Chown(s.Path, s.Owner.UID, s.Owner.GID); err != nil {
			return s.abandon(ln, fmt.Errorf("hand %s to %s: %w", s.Path, s.Owner.Username, err))
		}
	}

	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	return nil
}

// abandon undoes a half-built listener and returns the reason it was given.
func (s *Server) abandon(ln net.Listener, cause error) error {
	ln.Close()        //nolint:errcheck
	os.Remove(s.Path) //nolint:errcheck
	return cause
}

// Close stops listening and removes the socket. It is safe to call on a server
// that never listened, and safe to call twice.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed || s.ln == nil {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	s.mu.Unlock()

	err := ln.Close()
	if rmErr := s.removeFn()(s.Path); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
		err = rmErr
	}
	return err
}

// client is one connection, with the queue its writer drains.
//
// A client's watch set lives under the server's mutex rather than one of its
// own: the sampler reads every client's set at once, and a second lock ordering
// is a second thing to get wrong.
type client struct {
	conn  net.Conn
	out   chan any
	watch map[string]bool
}

// Serve accepts connections until ctx is cancelled, then says goodbye to
// everyone and removes the socket.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return fmt.Errorf("feed: Serve on %s before Listen", s.Path)
	}

	// The watcher must not outlive Serve: cancelling on the way out also means
	// an accept failure shuts the clients down cleanly rather than leaving
	// them connected to a server that has stopped.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Closing the listener is what unblocks Accept; there is no deadline on it.
	go func() {
		<-ctx.Done()
		s.shutdown()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // deliberate: cancellation is not an error
			}
			return fmt.Errorf("accept on %s: %w", s.Path, err)
		}
		s.add(conn)
	}
}

// Publish fans a view out to every client and remembers it for whoever
// connects next. Safe to call from any goroutine.
func (s *Server) Publish(v app.View) {
	s.mu.Lock()
	s.view, s.haveView = v, true
	msg := stateMsg{Type: "state", View: wire.Of(v)}
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		s.sendTo(c, msg)
	}
}

func (s *Server) add(conn net.Conn) {
	c := &client{conn: conn, out: make(chan any, sendQueue), watch: map[string]bool{}}

	s.mu.Lock()
	if s.closing || s.closed {
		s.mu.Unlock()
		conn.Close() //nolint:errcheck
		return
	}
	if s.clients == nil {
		s.clients = map[*client]struct{}{}
	}
	s.clients[c] = struct{}{}
	view, have := s.view, s.haveView
	s.mu.Unlock()

	go c.write()
	go s.read(c)

	s.sendTo(c, helloMsg{Type: "hello", Schema: Schema, Version: s.Version})
	if have {
		// Whoever connects between two refreshes must not sit blank until the
		// next one, which is five minutes away by default.
		s.sendTo(c, stateMsg{Type: "state", View: wire.Of(view)})
	}
}

// write drains the queue onto the connection. Encode appends a newline, which
// is the framing.
func (c *client) write() {
	defer c.conn.Close() //nolint:errcheck

	enc := json.NewEncoder(c.conn)
	for msg := range c.out {
		if err := enc.Encode(msg); err != nil {
			return
		}
	}
}

// read turns each line a client sends into an action. A line that does not
// parse, or whose type is unknown, is ignored: it is how a newer application
// talks to an older publisher.
func (s *Server) read(c *client) {
	defer s.drop(c)

	lines := bufio.NewScanner(c.conn)
	for lines.Scan() {
		var msg clientMsg
		if err := json.Unmarshal(lines.Bytes(), &msg); err != nil {
			continue
		}
		s.onMessage(c, msg)
	}
}

// onMessage acts on one line from a client. Nothing here can act on a tunnel:
// the vocabulary is watch, unwatch and refresh, and no more.
func (s *Server) onMessage(c *client, msg clientMsg) {
	switch msg.Type {
	case "watch":
		if msg.Tunnel == "" {
			return
		}
		s.mu.Lock()
		c.watch[msg.Tunnel] = true
		s.mu.Unlock()
		s.retick()
	case "unwatch":
		s.mu.Lock()
		delete(c.watch, msg.Tunnel)
		s.mu.Unlock()
		s.retick()
	case "refresh":
		s.mu.Lock()
		now := s.clock()
		if !s.lastRefresh.IsZero() && now.Sub(s.lastRefresh) < refreshFloor {
			s.mu.Unlock()
			return
		}
		s.lastRefresh = now
		reqs := s.requestsLocked()
		s.mu.Unlock()

		select {
		case reqs <- Request{Kind: RequestRefresh}:
		default:
		}
	}
}

// retick starts the sampling loop when the first tunnel is watched and stops it
// when the last one is released. A timer waking every second for a graph nobody
// is looking at is a timer waking for nothing.
func (s *Server) retick() {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch watched := s.watchedLocked(); {
	case watched && s.sampling == nil:
		stop := make(chan struct{})
		s.sampling = stop
		go s.sampleLoop(stop)
	case !watched && s.sampling != nil:
		close(s.sampling)
		s.sampling = nil
	}
}

// watchedLocked reports whether anybody is watching anything. The caller holds
// the mutex.
func (s *Server) watchedLocked() bool {
	for c := range s.clients {
		if len(c.watch) > 0 {
			return true
		}
	}
	return false
}

func (s *Server) sampleLoop(stop <-chan struct{}) {
	t := time.NewTicker(s.interval())
	defer t.Stop()

	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.sampleOnce()
		}
	}
}

// sampleOnce reads the union of what is watched and delivers each reading only
// to the clients that asked for that tunnel.
func (s *Server) sampleOnce() {
	s.mu.Lock()
	view := s.view
	watchers := map[string][]*client{}
	for c := range s.clients {
		for name := range c.watch {
			watchers[name] = append(watchers[name], c)
		}
	}
	s.mu.Unlock()

	for name, cs := range watchers {
		row, known := view.Row(name)
		if !known {
			// Either the tunnel is gone or no view has named it yet. The watch
			// stands; the next round looks again.
			continue
		}
		sample, taken := s.Sampler.Sample(row.Tunnel)
		if !taken {
			// A tunnel that is down has no counters. That is a fact rather
			// than a failure, and a zero would draw as a tunnel doing nothing.
			continue
		}
		msg := sampleMsg{
			Type: "sample", Tunnel: name,
			At: sample.At, Rx: sample.Rx, Tx: sample.Tx,
		}
		for _, c := range cs {
			s.sendTo(c, msg)
		}
	}
}

// sendTo queues one message, dropping the client if it has fallen too far
// behind.
//
// The queue is only ever closed by whoever removed the client from s.clients
// while holding the mutex, so a send that finds the client live cannot land on
// a closed channel.
func (s *Server) sendTo(c *client, msg any) {
	s.mu.Lock()
	if _, live := s.clients[c]; !live {
		s.mu.Unlock()
		return
	}
	select {
	case c.out <- msg:
		s.mu.Unlock()
		return
	default:
	}
	s.mu.Unlock()

	s.drop(c)
}

// drop forgets a client and lets its writer finish.
func (s *Server) drop(c *client) {
	s.mu.Lock()
	if _, live := s.clients[c]; !live {
		s.mu.Unlock()
		return
	}
	delete(s.clients, c)
	s.mu.Unlock()

	close(c.out)
	c.conn.Close() //nolint:errcheck
	// The client's watches went with it; the loop stops if it held the last.
	s.retick()
}

// shutdown says goodbye to everyone, stops listening and removes the socket.
func (s *Server) shutdown() {
	s.mu.Lock()
	s.closing = true
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = map[*client]struct{}{}
	s.mu.Unlock()

	for _, c := range clients {
		// Queued rather than written: the writer owns the connection, and the
		// bye goes out behind whatever it has not flushed yet.
		select {
		case c.out <- byeMsg{Type: "bye"}:
		default:
		}
		close(c.out)

		// The writer drains what is queued and then closes the connection. The
		// deadline is what guarantees it gets that far: without one, a peer
		// that stopped reading leaves it blocked in a write syscall.
		c.conn.SetWriteDeadline(time.Now().Add(byeGrace)) //nolint:errcheck // the connection is going away regardless
	}

	s.mu.Lock()
	if s.sampling != nil {
		close(s.sampling)
		s.sampling = nil
	}
	s.mu.Unlock()

	s.Close() //nolint:errcheck
}
