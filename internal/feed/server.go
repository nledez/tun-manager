package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/fsx"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/wgconf"
	"ledez.net/tun-manager/internal/wire"
)

const (
	// SocketMode is what the socket is chmodded to. The feed says which
	// tunnels exist and where they connect; it is for one person.
	SocketMode os.FileMode = 0o600

	// maxClients is how many connections the publisher will hold at once.
	//
	// One person runs one menu bar, and a second window is the same process; a
	// handful more covers a script watching the feed and somebody with `nc`
	// open. What the number is really for is the other case: a client that
	// opens connections and never closes them costs a goroutine and a
	// descriptor each, and root running out of descriptors takes the interface
	// down with it.
	maxClients = 32

	// maxWatch is how many tunnels one client may follow at once.
	//
	// A machine with more than this many tunnels is not one this program has
	// met, and a client can ask to watch a name before any view has arrived to
	// say whether it exists - which is the window where a map with no bound
	// could be grown from the wire.
	maxWatch = 64

	// maxLine is the longest line a client may send. The vocabulary is four
	// verbs and a tunnel name - a few dozen bytes - so eight kilobytes is
	// generous by two orders of magnitude. What it stops is a peer that opens a
	// connection and writes without ever sending a newline, which would
	// otherwise grow a buffer until the process died.
	//
	// Lower than bufio's own default, and that is the point: left at the
	// default, this would be a limit nobody chose, that no test could tell from
	// its absence, and that a later version of Go could move.
	maxLine = 8 << 10

	// sendQueue is how many messages a client may fall behind by. Sixteen is
	// several seconds of sampling: a client that cannot keep up with one
	// message a second is not going to recover.
	sendQueue = 16

	// sampleInterval is how often a watched tunnel's counters are read.
	sampleInterval = time.Second

	// refreshFloor is the shortest gap between two refreshes a client can ask
	// for. Without it a client could turn the menu bar into a way of hammering
	// the WireGuard control socket.
	refreshFloor = 2 * time.Second

	// pingFloor is the shortest gap between two rounds of probes a client can
	// ask for. Its own, rather than shared with the refresh: the two verbs cost
	// different things, and asking for a fresh view must not silence a ping.
	pingFloor = 2 * time.Second

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
//
// Tunnel is the name a request applies to, and is empty for one that applies
// to everything. It has already been checked against the last view, so it names
// a tunnel that exists or nothing at all.
type Request struct {
	Kind   string
	Tunnel string
}

// RequestRefresh asks whoever owns the refresh to take a fresh view.
const RequestRefresh = "refresh"

// RequestPing asks whoever owns the network to probe a tunnel's check address.
//
// This is the one verb with an effect outside the process: honouring it makes
// the publisher, which runs as root, send packets. What keeps it bounded is
// that the client chooses a tunnel, never an address — the address comes from
// the configuration, so no name on the wire can turn the publisher into a way
// of reaching somewhere it was not already told to reach.
const RequestPing = "ping"

// Server publishes views and samples to whoever connects.
//
// The zero value is not usable: Path is required, and Listen must be called
// before Serve.
type Server struct {
	// Path is where the socket is bound.
	Path string
	// Simulated says this feed belongs to a run pointed at the simulator rather
	// than at this machine. It is what turns off the demand that the directory
	// be root's: a demo binds where the flags said, under a directory belonging
	// to whoever started it. The zero value is the strict one.
	Simulated bool
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
	// socket is what Listen bound. Close removes only this, never whatever
	// happens to be at the path: a second tun-manager unlinks ours and binds
	// its own, and taking that one with us would leave it listening on a
	// socket nobody can reach.
	socket os.FileInfo

	mu          sync.Mutex
	closed      bool
	closing     bool
	clients     map[*client]struct{}
	view        app.View
	haveView    bool
	sampling    chan struct{}
	requests    chan Request
	lastRefresh time.Time
	lastPing    time.Time
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

// full reports whether the publisher is already holding as many clients as it
// will.
func (s *Server) full() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients) >= maxClients
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
	if err := s.checkDirectory(); err != nil {
		return err
	}
	if err := s.clearStaleSocket(); err != nil {
		return err
	}

	ln, err := bindPrivately(s.Path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.Path, err)
	}
	// net unlinks by path on Close by default, which would remove whatever is
	// at Path even if a second tun-manager has since replaced it. Close below
	// does its own, identity-checked removal instead.
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}

	// Kept although the umask above has already done it. An umask takes bits
	// out and never puts them in, so this can only ever tighten what is there -
	// and a filesystem that ignores the umask, or a Go that stops applying it
	// to a bind, would leave the socket readable with nothing to say so.
	if err := fsx.Chmod(s.Path, SocketMode); err != nil {
		return s.abandon(ln, fmt.Errorf("chmod %s: %w", s.Path, err))
	}
	if s.Owner.Demotable {
		if err := fsx.Chown(s.Path, s.Owner.UID, s.Owner.GID); err != nil {
			return s.abandon(ln, fmt.Errorf("hand %s to %s: %w", s.Path, s.Owner.Username, err))
		}
	}

	// Recorded so Close can tell this socket apart from whatever might be at
	// the path by the time it runs: a stat failure here is not fatal to
	// Listen, it just means Close falls back to removing by path alone.
	info, _ := fsx.Lstat(s.Path)

	s.mu.Lock()
	s.ln = ln
	s.socket = info
	s.mu.Unlock()
	return nil
}

// checkDirectory refuses to bind under a directory anybody can write.
//
// The mode of the socket is not the whole story. Somebody who can write the
// directory holding it can unlink it and bind their own in its place, and the
// menu bar would then be listening to whatever they chose to say — while
// tun-manager, root, went on running with nobody reading its feed.
//
// What that comes down to is the owner and the world bit. A directory root owns
// and only its group can write is somewhere a plain user already cannot touch:
// /var/run is 0775 root:daemon on darwin, `touch /var/run/anything` is refused,
// and anybody who is in that group has other ways in. Refusing it would refuse
// the documented socket path, and the advice that came with the refusal - chmod
// a system directory - was worse than the thing it was guarding against.
//
// Off for a simulated run, whose socket goes wherever the flags said and whose
// directory belongs to whoever is running the demo. Those flags are refused
// under sudo, so the two never meet.
func (s *Server) checkDirectory() error {
	if s.Simulated {
		return nil
	}

	dir := filepath.Dir(s.Path)
	info, err := fsx.Stat(dir)
	if err != nil {
		return fmt.Errorf("the status feed cannot bind in %s: %w", dir, err)
	}
	if uid, _ := fsx.Owner(dir, info); uid != fsx.Root {
		return fmt.Errorf(
			"the status feed will not bind in %s: it is owned by uid %d rather than root, "+
				"so that user can unlink the socket and bind their own in its place: "+
				"`sudo chown 0:0 %s`, or point feed_socket at a directory root owns", dir, uid, dir)
	}
	if info.Mode().Perm()&0o002 != 0 && info.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf(
			"the status feed will not bind in %s: it is %04o, so anybody at all can replace the "+
				"socket with one of their own: `sudo chmod o-w %s`", dir, info.Mode().Perm(), dir)
	}
	return nil
}

// clearStaleSocket unlinks what a killed tun-manager left behind, and nothing
// else.
//
// A socket left by a crash makes bind fail with EADDRINUSE, and two
// tun-manager processes cannot usefully coexist - they would both be driving
// the same tunnels - so that path is ours to take. Anything that is not a
// socket is not ours: feed_socket is read from the root-only configuration and
// unlinked by root, and a typo there was a way to have root delete a file for
// somebody. Lstat rather than Stat, so a symbolic link is judged as a link
// rather than as whatever it points at.
func (s *Server) clearStaleSocket() error {
	info, err := fsx.Lstat(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look at %s: %w", s.Path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf(
			"%s exists and is not a socket, so tun-manager will not remove it: "+
				"point feed_socket somewhere else, or move that file out of the way", s.Path)
	}
	if err := fsx.Remove(s.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stale socket %s: %w", s.Path, err)
	}
	return nil
}

// umaskMu serialises the umask around a bind. The umask is process-wide, and
// this is the only place that touches it: whatever else is creating a file at
// that instant gets a stricter mode than it asked for, for the length of one
// syscall, which is the harmless direction to be wrong in.
var umaskMu sync.Mutex

// bindPrivately binds the socket with a umask that makes it 0600 from the
// moment it exists.
//
// Binding and then chmodding leaves a window, and the window matters here in a
// way it would not for an ordinary file: the permissions of a unix socket are
// consulted at connect(2) and never again. Somebody who connects while the
// socket is still world-readable keeps that connection after the chmod, and
// reads the feed for as long as tun-manager runs.
func bindPrivately(path string) (net.Listener, error) {
	umaskMu.Lock()
	defer umaskMu.Unlock()

	previous := fsx.Umask(0o177)
	defer fsx.Umask(previous)

	lc := net.ListenConfig{}
	return lc.Listen(context.Background(), "unix", path)
}

// abandon undoes a half-built listener and returns the reason it was given.
func (s *Server) abandon(ln net.Listener, cause error) error {
	ln.Close()        //nolint:errcheck
	os.Remove(s.Path) //nolint:errcheck
	return cause
}

// Close stops listening and removes the socket, but only if the file still at
// Path is the one Listen bound. It is safe to call on a server that never
// listened, and safe to call twice.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed || s.ln == nil {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	socket := s.socket
	s.mu.Unlock()

	err := ln.Close()
	if s.ours(socket) {
		if rmErr := fsx.Remove(s.Path); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
			err = rmErr
		}
	}
	return err
}

// ours reports whether the file currently at Path is the one bound, so Close
// never takes a second tun-manager's socket with it. A stat that fails, or one
// Listen never recorded, is treated as ours: there is nothing safer to compare
// against, and the alternative is a socket file left behind forever.
func (s *Server) ours(bound os.FileInfo) bool {
	if bound == nil {
		return true
	}
	now, err := fsx.Lstat(s.Path)
	if err != nil {
		return true
	}
	return os.SameFile(bound, now)
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

	// done closes once shutdown has finished, so Serve can wait for it below
	// instead of returning while it is still running in the background.
	// Closing the listener is what unblocks Accept; there is no deadline on it.
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		s.shutdown()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			// Whether this is a shutdown or a real failure, the clients are
			// owed their goodbye before Serve reports that it has stopped.
			// stopped is read before cancel, so it reflects the caller's
			// context rather than our own cancellation below.
			stopped := ctx.Err() != nil
			cancel()
			<-done
			if stopped {
				return nil
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

// PublishPings fans a round of probes out to every client.
//
// Unlike a view it is not remembered for whoever connects next: a view keeps
// its meaning for minutes, a round-trip time does not, and showing a client a
// measurement taken before it connected is worse than showing it none.
func (s *Server) PublishPings(pings []wire.Ping) {
	msg := pingMsg{Type: "ping", Results: pings}

	s.mu.Lock()
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
	// Asked before anything is sent, and before the client is remembered: a
	// connection this feed does not serve is closed having learnt nothing, not
	// even the version in the hello line.
	if !s.serves(conn) {
		conn.Close() //nolint:errcheck // there is nothing to say to it
		return
	}
	if s.full() {
		// Told why, rather than closed in silence: a client that reconnects
		// forever against a publisher that will not have it is a client whose
		// author has no way of finding out.
		refused, _ := json.Marshal(refusedMsg{Type: "refused", Reason: "too many clients"}) //nolint:errcheck // a struct of two strings
		conn.Write(append(refused, '\n'))                                                   //nolint:errcheck // it is going away either way
		conn.Close()                                                                        //nolint:errcheck // likewise
		return
	}

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
	// A line longer than this ends the scan, which drops the client. That is
	// the right end for it: nothing in the vocabulary is that long, so a peer
	// sending one is not a client this feed can talk to.
	//
	// No read deadline goes with it. The one client this program has says
	// nothing at all until somebody opens the menu, so an idle timeout would
	// disconnect the healthy case every few minutes and teach it to reconnect
	// in a loop. What bounds a peer that connects and goes quiet is maxClients,
	// which is a bound on the thing that actually costs something.
	lines.Buffer(make([]byte, 0, 4096), maxLine)
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
		// A name is only taken on trust while no view is known: a client may
		// watch before the first state lands. Once there is a view, a name
		// that is not in it will never produce a reading, and keeping it would
		// let a client grow this map without bound.
		if s.haveView {
			if _, known := s.view.Row(msg.Tunnel); !known {
				s.mu.Unlock()
				return
			}
		}
		if len(c.watch) >= maxWatch {
			// A client following more tunnels than this machine has is a client
			// that has stopped being one.
			s.mu.Unlock()
			return
		}
		c.watch[msg.Tunnel] = true
		s.mu.Unlock()
		s.retick()
	case "unwatch":
		s.mu.Lock()
		delete(c.watch, msg.Tunnel)
		s.mu.Unlock()
		s.retick()
	case "refresh":
		s.request(Request{Kind: RequestRefresh}, &s.lastRefresh, refreshFloor)
	case "ping":
		s.mu.Lock()
		// Same reasoning as watch: a name that is in no view names no address,
		// so there is nothing to probe. Dropping it here is what keeps a name
		// off the wire from ever reaching the code that resolves one.
		if msg.Tunnel != "" && s.haveView {
			if _, known := s.view.Row(msg.Tunnel); !known {
				s.mu.Unlock()
				return
			}
		}
		s.mu.Unlock()
		s.request(Request{Kind: RequestPing, Tunnel: msg.Tunnel}, &s.lastPing, pingFloor)
	}
}

// request passes something on unless it comes too soon after the last one of
// its kind. The floor is per verb, so last points at that verb's own stamp.
//
// Dropped rather than queued: a client that asked twice in a second wants the
// current answer, and a second one behind the first would only arrive late.
func (s *Server) request(req Request, last *time.Time, floor time.Duration) {
	s.mu.Lock()
	now := s.clock()
	if !last.IsZero() && now.Sub(*last) < floor {
		s.mu.Unlock()
		return
	}
	*last = now
	reqs := s.requestsLocked()
	s.mu.Unlock()

	select {
	case reqs <- req:
	default:
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
	if s.Sampler == nil {
		// A server with nothing to sample has nothing to do.
		return
	}

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
