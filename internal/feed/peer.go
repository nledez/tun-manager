package feed

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// peerCredentials is the one call that asks the kernel. A variable for the
// reason the rest of this program's syscalls are: it cannot be made to fail on
// a working machine, and what it does when it fails is refuse a client, which
// is worth having run at least once.
var peerCredentials = unix.GetsockoptXucred

// peerUID reports the uid of whoever opened a connection.
//
// A variable because a test cannot connect as somebody else: every socket a
// suite opens comes from the identity running it, so the one case worth
// checking - a connection from a uid this feed does not serve - is the one that
// cannot be arranged.
var peerUID = realPeerUID

// realPeerUID asks the kernel who is on the other end.
//
// LOCAL_PEERCRED answers with the credentials the peer had when it connected,
// recorded by the kernel rather than claimed by anybody. That is what makes it
// worth having on top of the socket's mode: a mode is consulted at connect(2)
// and never again, so a connection opened during any window at all keeps
// whatever it got. This is asked on the connection itself, and stays true.
//
// The descriptor is reached through SyscallConn().Control rather than through
// File(), which would duplicate it and leave a second one to close.
func realPeerUID(conn net.Conn) (int, error) {
	// Asked of a TCP socket on darwin, LOCAL_PEERCRED does not fail: it answers
	// with a zeroed xucred, and a zeroed xucred says uid 0, which is root. This
	// feed only ever accepts on a unix socket, so the check costs nothing — and
	// a question whose wrong answer is "root" is not one to ask of something it
	// was not meant for.
	if network := conn.LocalAddr().Network(); network != "unix" {
		return 0, fmt.Errorf("a %s connection carries no local credentials", network)
	}

	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, errors.New("this connection has no credentials to read")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("reach the connection: %w", err)
	}

	var uid int
	var asked error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr := peerCredentials(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if credErr != nil {
			asked = credErr
			return
		}
		uid = int(cred.Uid)
	}); err != nil {
		return 0, fmt.Errorf("reach the connection: %w", err)
	}
	if asked != nil {
		return 0, fmt.Errorf("read the credentials of the peer: %w", asked)
	}
	return uid, nil
}

// serves reports whether a connection is one this feed will answer.
//
// Three identities, and no more. Root, because root can read the socket
// whatever its mode says and refusing would be theatre. The user the socket was
// handed to, which is the whole point of handing it over. And the identity this
// process runs as, which is the same as root in production and is what makes a
// demo work: a simulated run publishes as an ordinary user and is talked to by
// that same user.
//
// Credentials that cannot be read are a refusal. A feed that answered anyway
// would be one that answers whenever the check breaks, which is the state
// somebody trying to reach it would aim for.
func (s *Server) serves(conn net.Conn) bool {
	uid, err := peerUID(conn)
	if err != nil {
		return false
	}
	if uid == 0 || uid == os.Getuid() {
		return true
	}
	return s.Owner.Demotable && uid == s.Owner.UID
}
