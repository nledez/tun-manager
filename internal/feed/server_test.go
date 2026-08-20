package feed

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/fsx"
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
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// Listen refuses a directory somebody other than root could write, and a
	// directory made by a test belongs to whoever is running it. The mode is
	// real; the owner is the one thing a suite cannot arrange without sudo.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	ownedByRoot(t)
	return filepath.Join(dir, "f.sock")
}

// ownedByRoot makes every path look root-owned for the length of a test. A
// fixture belongs to whoever runs the suite, and a suite that only proves
// itself under sudo proves nothing on anybody else's machine.
func ownedByRoot(t *testing.T) {
	t.Helper()

	previous := fsx.Owner
	fsx.Owner = func(string, os.FileInfo) (int, int) { return fsx.Root, fsx.Root }
	t.Cleanup(func() { fsx.Owner = previous })
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
	// What matters is which identity the socket goes to. A real chown cannot
	// say: as an ordinary user it only succeeds for the identity the test
	// already has, so it would pass with the wrong ids hard-coded, and under
	// sudo - which is how this program runs - it succeeds for any of them.
	var got struct {
		path     string
		uid, gid int
		calls    int
	}
	s := &Server{
		Path:  socketPath(t),
		Owner: privdrop.User{Username: "operator", UID: 501, GID: 20, Demotable: true},
	}
	previous := fsx.Chown
	fsx.Chown = func(path string, uid, gid int) error {
		got.path, got.uid, got.gid, got.calls = path, uid, gid, got.calls+1
		return nil
	}
	t.Cleanup(func() { fsx.Chown = previous })

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close()

	if got.calls != 1 {
		t.Fatalf("chown called %d time(s), want once", got.calls)
	}
	if got.path != s.Path || got.uid != 501 || got.gid != 20 {
		t.Errorf("chown(%q, %d, %d), want the socket handed to operator", got.path, got.uid, got.gid)
	}
}

func TestHandingTheSocketOverIsFatalWhenItFails(t *testing.T) {
	// Leaving a root-owned socket behind would look like it worked and then
	// serve nobody.
	boom := errors.New("operation not permitted")
	s := &Server{
		Path:  socketPath(t),
		Owner: privdrop.User{Username: "operator", UID: 501, GID: 20, Demotable: true},
	}
	previous := fsx.Chown
	fsx.Chown = func(string, int, int) error { return boom }
	t.Cleanup(func() { fsx.Chown = previous })

	err := s.Listen()

	if !errors.Is(err, boom) {
		s.Close()
		t.Fatalf("Listen = %v, want the handover failure", err)
	}
	if _, statErr := os.Stat(s.Path); !os.IsNotExist(statErr) {
		t.Errorf("the socket outlived a failed Listen")
	}
}

func TestTheRealChownIsWhatRunsWhenNoTestStandsInForIt(t *testing.T) {
	// All this proves is that fsx.Chown resolves to the system call rather than
	// to nothing: the identity is the one the test process already has, so it
	// says nothing about which identity would be chosen. That part is pinned by
	// TestTheSocketIsHandedToThePreSudoUser, which is why this test asserts so
	// little.
	s := &Server{
		Path: socketPath(t),
		Owner: privdrop.User{
			Username: "operator", UID: os.Getuid(), GID: os.Getgid(), Demotable: true,
		},
	}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s.Close()
}

func TestASocketWithNoOneToHandItToStaysWhereItIs(t *testing.T) {
	// A real root login rather than sudo: there is no user session to serve,
	// so the socket stays root-owned rather than failing to start. The
	// recorder proves the handover was skipped, not merely survived.
	handed := 0
	s := &Server{
		Path:  socketPath(t),
		Owner: privdrop.User{Demotable: false, UID: 0},
	}
	previous := fsx.Chown
	fsx.Chown = func(string, int, int) error { handed++; return nil }
	t.Cleanup(func() { fsx.Chown = previous })
	defer func() {
		if handed != 0 {
			t.Errorf("chown called %d time(s), want none", handed)
		}
	}()

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s.Close()
}

func TestAStaleSocketIsReplacedRatherThanRefused(t *testing.T) {
	// A killed process leaves the file behind and bind fails with EADDRINUSE.
	// Two tun-managers cannot usefully coexist anyway, so the path is ours.
	path := socketPath(t)
	staleSocket(t, path)

	s := &Server{Path: path}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	s.Close()
}

func TestListenReportsAPathItCannotBind(t *testing.T) {
	ownedByRoot(t)
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

// staleSocket leaves at path what a killed tun-manager leaves behind: a socket
// file with nothing listening on it.
func staleSocket(t *testing.T, path string) {
	t.Helper()

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("bind a stale socket: %v", err)
	}
	if unix, ok := ln.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// MARK: what the publisher will and will not unlink

func TestListenRefusesToRemoveWhatIsNotASocket(t *testing.T) {
	// feed_socket is read from the root-only configuration and unlinked by
	// root. A typo in it was a way to have root delete somebody's file.
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := (&Server{Path: path}).Listen()

	if err == nil {
		t.Fatal("Listen unlinked a file that is not a socket")
	}
	if !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("error %q does not say what is in the way", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("the file was removed anyway: %v", statErr)
	}
}

func TestListenRefusesToFollowASymbolicLinkAtItsPath(t *testing.T) {
	// Judged as a link rather than as whatever it points at. The target here is
	// itself a stale socket, which is the case that tells the two apart:
	// following the link would find something removable and unlink the link,
	// leaving root to have deleted a name somebody else put there.
	dir := filepath.Dir(socketPath(t))
	target := filepath.Join(dir, "elsewhere.sock")
	staleSocket(t, target)
	path := filepath.Join(dir, "f.sock")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := (&Server{Path: path}).Listen()

	if err == nil {
		t.Fatal("Listen followed a symbolic link")
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Errorf("the link was removed anyway: %v", statErr)
	}
	if _, statErr := os.Lstat(target); statErr != nil {
		t.Errorf("what it pointed at was removed: %v", statErr)
	}
}

func TestListenReportsAPathItCannotLookAt(t *testing.T) {
	ownedByRoot(t)
	path := socketPath(t)
	boom := errors.New("input/output error")
	previous := fsx.Lstat
	fsx.Lstat = func(string) (os.FileInfo, error) { return nil, boom }
	t.Cleanup(func() { fsx.Lstat = previous })

	if err := (&Server{Path: path}).Listen(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure to look at the path", err)
	}
}

func TestListenRefusesADirectoryRootDoesNotOwn(t *testing.T) {
	// Anybody who can write that directory can unlink the socket and bind
	// their own in its place, and the menu bar would then be listening to
	// whatever they chose to say.
	path := socketPath(t)
	previous := fsx.Owner
	fsx.Owner = func(string, os.FileInfo) (int, int) { return 501, 501 }
	t.Cleanup(func() { fsx.Owner = previous })

	err := (&Server{Path: path}).Listen()

	if err == nil {
		t.Fatal("Listen bound under a directory root does not own")
	}
	for _, want := range []string{"uid 501", "sudo chown 0:0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestListenRefusesADirectoryOthersCanWrite(t *testing.T) {
	path := socketPath(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	err := (&Server{Path: path}).Listen()

	if err == nil {
		t.Fatal("Listen bound under a directory anybody can write")
	}
	if !strings.Contains(err.Error(), "sudo chmod go-w") {
		t.Errorf("error %q does not say how to fix it", err)
	}
}

func TestListenReportsADirectoryItCannotLookAt(t *testing.T) {
	s := &Server{Path: filepath.Join(t.TempDir(), "absent", "f.sock")}

	err := s.Listen()

	if err == nil {
		t.Fatal("Listen bound under a directory that is not there")
	}
	if !strings.Contains(err.Error(), "cannot bind in") {
		t.Errorf("error %q does not say what it could not read", err)
	}
}

func TestASimulatedFeedBindsWhereItWasTold(t *testing.T) {
	// A demo binds where the flags said, under a directory belonging to
	// whoever started it. Those flags are refused under sudo, so the strict
	// rule above and this one never meet.
	previous := fsx.Owner
	fsx.Owner = func(string, os.FileInfo) (int, int) { return 501, 501 }
	t.Cleanup(func() { fsx.Owner = previous })
	s := &Server{Path: socketPath(t), Simulated: true}

	if err := s.Listen(); err != nil {
		t.Fatalf("a simulated feed was held to the directory rule: %v", err)
	}
	s.Close() //nolint:errcheck
}

func TestCloseLeavesBehindWhatIsNoLongerItsOwnSocket(t *testing.T) {
	// Something took the name while the program was running. Removing it on
	// the way out would be root deleting a file it never made — which is what
	// the identity check is for; a name is not an identity.
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := os.Remove(s.Path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.WriteFile(s.Path, []byte("somebody else's"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(s.Path); err != nil {
		t.Errorf("Close removed a file that was not its socket: %v", err)
	}
}
