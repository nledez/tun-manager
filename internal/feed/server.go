package feed

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/wgconf"
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
	sampleInterval = time.Second

	// refreshFloor is the shortest gap between two refreshes a client can ask
	// for. Without it a client could turn the menu bar into a way of hammering
	// the WireGuard control socket.
	refreshFloor = 2 * time.Second
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

	mu     sync.Mutex
	closed bool
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

// Listen binds the socket and hands it to Owner. Call it before Serve.
//
// Any failure after the bind removes the socket again: a path left behind
// would look like a feed that works and serve nobody.
func (s *Server) Listen() error {
	// A socket left by a killed process makes bind fail with EADDRINUSE. Two
	// tun-manager processes cannot usefully coexist - they would both be
	// driving the same tunnels - so the path is ours to take.
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", s.Path, err)
	}

	ln, err := net.Listen("unix", s.Path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.Path, err)
	}

	if err := os.Chmod(s.Path, SocketMode); err != nil {
		return s.abandon(ln, fmt.Errorf("chmod %s: %w", s.Path, err))
	}
	if s.Owner.Demotable {
		if err := os.Chown(s.Path, s.Owner.UID, s.Owner.GID); err != nil {
			return s.abandon(ln, fmt.Errorf("hand %s to %s: %w", s.Path, s.Owner.Username, err))
		}
	}

	s.ln = ln
	return nil
}

// abandon undoes a half-built listener and returns the reason it was given.
func (s *Server) abandon(ln net.Listener, cause error) error {
	ln.Close()
	os.Remove(s.Path)
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
	if rmErr := os.Remove(s.Path); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
		err = rmErr
	}
	return err
}
