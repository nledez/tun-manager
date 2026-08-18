package feed

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/privdrop"
)

// socketPath returns a path for a unix socket, short enough to bind.
//
// t.TempDir() is the usual answer and the wrong one here: it puts the test's
// name in the path, and a unix socket path is capped at around a hundred
// bytes, which a name like TestAClientArrivingBeforeAnyViewGetsOnlyHello
// blows through on its own once $TMPDIR is prepended. The directory is still
// made fresh for each call and removed when the test ends.
func socketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "tm")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "f.sock")
}

func TestListenCreatesTheSocketReadableByNobodyElse(t *testing.T) {
	// The socket carries what tunnels exist and where they connect to. It is
	// for one person: whoever started the program.
	s := &Server{Path: socketPath(t)}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close()

	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != SocketMode {
		t.Errorf("mode = %o, want %o", got, SocketMode)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("mode = %v, want a socket", info.Mode())
	}
}

func TestTheSocketIsHandedToThePreSudoUser(t *testing.T) {
	// Root creates it and the user's session has to open it. Chowning to the
	// current uid is a real chown that an unprivileged test can make.
	s := &Server{
		Path: socketPath(t),
		Owner: privdrop.User{
			Username: "operator", UID: os.Getuid(), GID: os.Getgid(), Demotable: true,
		},
	}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close()
}

func TestHandingTheSocketOverIsFatalWhenItFails(t *testing.T) {
	if os.Geteuid() == 0 {
		// Chowning to root succeeds when you are root, so there is no failure
		// to observe. The whole program runs under sudo, so a suite run that
		// way is not far-fetched.
		t.Skip("this test proves a chown fails, which it cannot do as root")
	}
	// Leaving a root-owned socket behind would look like it worked and then
	// serve nobody.
	s := &Server{
		Path:  socketPath(t),
		Owner: privdrop.User{Username: "root", UID: 0, GID: 0, Demotable: true},
	}

	err := s.Listen()

	if err == nil {
		s.Close()
		t.Fatal("Listen succeeded while chowning to root as an ordinary user")
	}
	if _, statErr := os.Stat(s.Path); !os.IsNotExist(statErr) {
		t.Errorf("the socket outlived a failed Listen")
	}
}

func TestASocketWithNoOneToHandItToStaysWhereItIs(t *testing.T) {
	// A real root login rather than sudo: there is no user session to serve,
	// so the socket stays root-owned rather than failing to start.
	s := &Server{Path: socketPath(t), Owner: privdrop.User{Demotable: false, UID: 0}}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s.Close()
}

func TestAStaleSocketIsReplacedRatherThanRefused(t *testing.T) {
	// A killed process leaves the file behind and bind fails with EADDRINUSE.
	// Two tun-managers cannot usefully coexist anyway, so the path is ours.
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &Server{Path: path}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	s.Close()
}

func TestListenReportsAPathItCannotBind(t *testing.T) {
	s := &Server{Path: filepath.Join(t.TempDir(), "no-such-dir", "f.sock")}

	if err := s.Listen(); err == nil {
		s.Close()
		t.Fatal("Listen succeeded on a path with no directory")
	}
}

func TestCloseRemovesTheSocket(t *testing.T) {
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Errorf("stat after Close = %v, want the socket gone", err)
	}
}

func TestClosingAServerThatNeverListenedIsHarmless(t *testing.T) {
	// The composition root closes whatever it built, including on the path
	// where Listen failed.
	if err := (&Server{Path: "/nonexistent"}).Close(); err != nil {
		t.Errorf("Close = %v, want nothing to do", err)
	}
}

func TestCloseFallsBackToRemovingByPathWhenListenNeverRecordedWhichOneItBound(t *testing.T) {
	// A stat failure right after bind is not fatal to Listen, but it leaves
	// Close with nothing to compare against. Falling back to removing
	// unconditionally is what stops a working socket from being left behind
	// forever for want of one Stat call.
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s.mu.Lock()
	s.socket = nil
	s.mu.Unlock()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Error("socket still present, want Close to remove it when it has nothing to compare against")
	}
}

func TestCloseIsHarmlessWhenTheSocketIsAlreadyGone(t *testing.T) {
	// Something else can remove the path between Listen and Close - the
	// identity check must not turn that into a reported failure.
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := os.Remove(s.Path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Errorf("Close = %v, want the missing socket treated as already gone", err)
	}
}

func TestCloseRemovesOnlyTheSocketItBound(t *testing.T) {
	// A second tun-manager can unlink a stale socket and bind its own at the
	// same path - Listen deliberately allows exactly that. Close must not take
	// the replacement with it: that would leave the second process listening
	// on an unlinked inode nobody can reach, while doctor reports Pass.
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	// Stand in for a second process: unlink what Listen bound and put a plain
	// file where it was, playing the part of that process's own socket.
	if err := os.Remove(s.Path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.WriteFile(s.Path, []byte("somebody else's socket"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatalf("the replacement did not survive Close: %v", err)
	}
	if string(got) != "somebody else's socket" {
		t.Errorf("path holds %q, want the replacement left untouched", got)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	_ = s.Close()
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nothing to do", err)
	}
}

func TestIntervalReturnsTheConfiguredInterval(t *testing.T) {
	// interval() returns the configured Interval if set, otherwise sampleInterval.
	s := &Server{}
	if got := s.interval(); got != sampleInterval {
		t.Errorf("interval = %v, want %v", got, sampleInterval)
	}

	s.Interval = 100 * time.Millisecond
	if got := s.interval(); got != 100*time.Millisecond {
		t.Errorf("interval = %v, want 100ms", got)
	}
}

func TestClockReturnsTheConfiguredClock(t *testing.T) {
	// clock() returns the configured Now function if set, otherwise time.Now.
	s := &Server{}
	now := s.clock()
	if now.IsZero() {
		t.Errorf("clock returned zero time, want a real time")
	}

	fixedTime := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return fixedTime }
	if got := s.clock(); got != fixedTime {
		t.Errorf("clock = %v, want %v", got, fixedTime)
	}
}
