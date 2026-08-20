package feed

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

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

func TestListenRefusesADirectoryAnybodyCanWrite(t *testing.T) {
	// World-writable, which is the one that matters: a directory root owns and
	// only its group can write is somewhere a plain user already cannot touch.
	path := socketPath(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	err := (&Server{Path: path}).Listen()

	if err == nil {
		t.Fatal("Listen bound under a directory anybody can write")
	}
	if !strings.Contains(err.Error(), "sudo chmod o-w") {
		t.Errorf("error %q does not say how to fix it", err)
	}
}

func TestListenBindsUnderADirectoryOnlyItsGroupCanWrite(t *testing.T) {
	// /var/run is 0775 root:daemon on darwin, and `touch /var/run/anything` is
	// refused to a plain user. Refusing it would refuse the documented socket
	// path, and the advice that came with the refusal — chmod a system
	// directory — was worse than the thing it guarded against.
	path := socketPath(t)
	if err := os.Chmod(filepath.Dir(path), 0o775); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	s := &Server{Path: path}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen refused the mode /var/run has: %v", err)
	}
	s.Close() //nolint:errcheck
}

func TestListenReportsADirectoryItCannotLookAt(t *testing.T) {
	ownedByRoot(t)
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

func TestListenReportsASocketItCannotUnlink(t *testing.T) {
	// A stale socket in a directory that has gone read-only. Binding on top of
	// it is not possible, and pretending otherwise leaves a feed that looks
	// like it works and serves nobody.
	path := socketPath(t)
	staleSocket(t, path)
	boom := errors.New("permission denied")
	previous := fsx.Remove
	fsx.Remove = func(string) error { return boom }
	t.Cleanup(func() { fsx.Remove = previous })

	if err := (&Server{Path: path}).Listen(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure to unlink", err)
	}
}

// MARK: the mode the socket has from the moment it exists

func TestTheSocketIsNeverReadableEvenBeforeTheChmod(t *testing.T) {
	// The chmod that follows the bind cannot close this on its own: the
	// permissions of a unix socket are consulted at connect(2) and never again,
	// so anybody who connected during the window keeps reading the feed for as
	// long as tun-manager runs. With the chmod doing nothing at all, the mode
	// has to be 0600 already.
	previous := fsx.Chmod
	fsx.Chmod = func(string, os.FileMode) error { return nil }
	t.Cleanup(func() { fsx.Chmod = previous })
	s := &Server{Path: socketPath(t)}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close() //nolint:errcheck

	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != SocketMode {
		t.Errorf("the socket was born %04o, want %04o: the umask did not take", got, SocketMode)
	}
}

func TestTheUmaskIsPutBackAfterTheBind(t *testing.T) {
	// It is process-wide. Leaving it at 0177 would tighten every file the
	// program writes afterwards, which is the harmless direction — and still
	// not something to do behind the back of the rest of the program.
	var asked []int
	previous := fsx.Umask
	fsx.Umask = func(mask int) int {
		asked = append(asked, mask)
		return previous(mask)
	}
	t.Cleanup(func() { fsx.Umask = previous })
	s := &Server{Path: socketPath(t)}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close() //nolint:errcheck

	if len(asked) != 2 {
		t.Fatalf("the umask was set %d time(s), want it set and put back", len(asked))
	}
	if asked[0] != 0o177 {
		t.Errorf("bound under umask %04o, want 0177", asked[0])
	}
	if asked[1] == 0o177 {
		t.Error("the umask was left at 0177 for the rest of the program")
	}
}

func TestTheUmaskIsPutBackEvenWhenTheBindFails(t *testing.T) {
	var asked []int
	previous := fsx.Umask
	fsx.Umask = func(mask int) int {
		asked = append(asked, mask)
		return previous(mask)
	}
	t.Cleanup(func() { fsx.Umask = previous })
	ownedByRoot(t)
	s := &Server{Path: filepath.Join(t.TempDir(), "f.sock")}
	if err := os.Chmod(filepath.Dir(s.Path), 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if err := s.Listen(); err == nil {
		s.Close() //nolint:errcheck
		t.Fatal("Listen bound in a directory it cannot write")
	}

	if len(asked) != 2 || asked[1] == 0o177 {
		t.Errorf("the umask was left behind after a failed bind: %v", asked)
	}
}

// MARK: who the feed will answer

func TestAConnectionFromSomebodyElseIsClosedHavingLearntNothing(t *testing.T) {
	// The socket is 0600 in a directory root owns, so this should not happen —
	// and it is the last line rather than the first: a mode is consulted at
	// connect(2) and never again, so a connection that got in during any window
	// at all would otherwise keep reading the feed for as long as tun-manager
	// runs.
	asking(t, func(net.Conn) (int, error) { return os.Getuid() + 2, nil })
	s := serving(t, nil)

	c := dial(t, s)

	if c.lines.Scan() {
		t.Errorf("the feed answered a stranger with %q", c.lines.Bytes())
	}
	if s.clientCount() != 0 {
		t.Errorf("the stranger was remembered as a client")
	}
}

func TestAConnectionWhoseCredentialsCannotBeReadIsRefused(t *testing.T) {
	// A feed that answered anyway would be one that answers whenever the check
	// breaks, which is the state somebody trying to reach it would aim for.
	asking(t, func(net.Conn) (int, error) { return 0, errors.New("no credentials on this socket") })
	s := serving(t, nil)

	c := dial(t, s)

	if c.lines.Scan() {
		t.Errorf("the feed answered a connection it could not identify: %q", c.lines.Bytes())
	}
}

func TestTheFeedAnswersTheUserItsSocketWasHandedTo(t *testing.T) {
	// The whole point of handing the socket over: the menu bar runs as that
	// user and this is the connection it makes.
	//
	// Neither root nor the identity running the suite, or the two other rules
	// would let it through and this would be proving nothing.
	operator := os.Getuid() + 1
	asking(t, func(net.Conn) (int, error) { return operator, nil })
	// Listen hands the socket to that user on the way, which a suite running as
	// somebody else cannot do for real.
	previousChown := fsx.Chown
	fsx.Chown = func(string, int, int) error { return nil }
	t.Cleanup(func() { fsx.Chown = previousChown })
	s := serving(t, nil, func(s *Server) {
		s.Owner = privdrop.User{Username: "operator", UID: operator, GID: 20, Demotable: true}
	})

	c := dial(t, s)

	if got := c.next(t)["type"]; got != "hello" {
		t.Errorf("first line = %v, want the hello the feed opens with", got)
	}
}

func TestTheFeedAnswersRoot(t *testing.T) {
	// Root can read that socket whatever its mode says. Refusing would be
	// theatre.
	asking(t, func(net.Conn) (int, error) { return 0, nil })
	s := serving(t, nil)

	if got := dial(t, s).next(t)["type"]; got != "hello" {
		t.Errorf("first line = %v, want the hello the feed opens with", got)
	}
}

func TestTheRealCredentialsAreWhatIsAskedFor(t *testing.T) {
	// Every other test here stands in for the kernel. This one does not: the
	// connection is a real one, from the identity running the suite, and the
	// feed has to recognise it.
	s := serving(t, nil)

	if got := dial(t, s).next(t)["type"]; got != "hello" {
		t.Errorf("first line = %v, want the hello the feed opens with", got)
	}
}

func TestCredentialsCannotBeReadFromSomethingThatIsNotASocket(t *testing.T) {
	// The assertion in realPeerUID: a net.Conn that carries no descriptor to
	// ask about. Answering "uid 0" for one would be answering for anybody.
	if _, err := realPeerUID(nothingUnderneath{}); err == nil {
		t.Error("credentials were read off a connection that has none")
	}
}

// asking makes the feed read the uid a test chooses rather than the one the
// kernel reports: every socket a suite opens comes from the identity running
// it, so the case worth checking is the one it cannot arrange.
func asking(t *testing.T, who func(net.Conn) (int, error)) {
	t.Helper()

	previous := peerUID
	peerUID = who
	t.Cleanup(func() { peerUID = previous })
}

// nothingUnderneath is a connection with no descriptor behind it. It says it is
// a unix socket, because that is the question realPeerUID asks first.
type nothingUnderneath struct{ net.Conn }

func (nothingUnderneath) LocalAddr() net.Addr { return unixAddr{} }

func TestCredentialsCannotBeReadFromAConnectionThatIsGone(t *testing.T) {
	// Control cannot reach a descriptor that has been closed. Answering for it
	// would be answering for whoever comes next on that number.
	conn := aUnixConnection(t)
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := realPeerUID(conn); err == nil {
		t.Error("credentials were read off a closed connection")
	}
}

func TestCredentialsCannotBeReadFromASocketThatIsNotLocal(t *testing.T) {
	// LOCAL_PEERCRED is a unix-socket question, and darwin does not refuse it
	// when it is asked of a TCP socket: it answers with a zeroed xucred, which
	// says uid 0, which is root. So the question is not asked of anything but a
	// unix socket, and this is the test that says so.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	if uid, err := realPeerUID(conn); err == nil {
		t.Errorf("realPeerUID = %d on a TCP connection, want a refusal", uid)
	}
}

func TestCredentialsCannotBeReadFromAConnectionThatWillNotBeReached(t *testing.T) {
	// A net.Conn that says it has a descriptor and then cannot produce one.
	if _, err := realPeerUID(unreachable{}); err == nil {
		t.Error("credentials were read off a connection that cannot be reached")
	}
}

// unreachable implements syscall.Conn and fails to hand over the descriptor,
// which is the shape of a connection closed between the accept and the
// question.
type unreachable struct{ net.Conn }

func (unreachable) LocalAddr() net.Addr { return unixAddr{} }

// aUnixConnection is one end of a real unix socket pair, for the tests that ask
// about credentials without a feed behind them.
func aUnixConnection(t *testing.T) net.Conn {
	t.Helper()

	path := socketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// unixAddr is what a unix socket answers when asked what it is.
type unixAddr struct{}

func (unixAddr) Network() string { return "unix" }
func (unixAddr) String() string  { return "/var/run/tun-manager.sock" }

func (unreachable) SyscallConn() (syscall.RawConn, error) {
	return nil, errors.New("the connection is gone")
}

func TestAPeerTheKernelWillNotVouchForIsRefused(t *testing.T) {
	// The getsockopt itself failing. It does not on a working machine, and what
	// it does when it fails is decide who gets to read the feed.
	//
	// No Server here on purpose: standing in for a package variable while an
	// accept loop is reading it is a data race, and -race says so.
	conn := aUnixConnection(t)
	previous := peerCredentials
	peerCredentials = func(int, int, int) (*unix.Xucred, error) {
		return nil, errors.New("protocol not available")
	}
	t.Cleanup(func() { peerCredentials = previous })

	if uid, err := realPeerUID(conn); err == nil {
		t.Errorf("realPeerUID = %d, want the refusal the kernel gave", uid)
	}
}

// MARK: what one client is allowed to cost

func TestThePublisherHoldsOnlySoManyClients(t *testing.T) {
	// A client that opens connections and never closes them costs a goroutine
	// and a descriptor each, and root running out of descriptors takes the
	// interface down with it.
	s := serving(t, nil)
	for range maxClients {
		dial(t, s).next(t) // read the hello, so the client is well and truly in
	}

	over := dial(t, s)

	if got := over.next(t)["type"]; got != "refused" {
		t.Errorf("first line = %v, want the publisher saying it is full", got)
	}
	if s.clientCount() != maxClients {
		t.Errorf("clients = %d, want %d", s.clientCount(), maxClients)
	}
}

func TestARefusedClientIsToldWhy(t *testing.T) {
	// Closed in silence, a client reconnects forever against a publisher that
	// will not have it, and whoever wrote it has no way of finding out.
	s := serving(t, nil)
	for range maxClients {
		dial(t, s).next(t)
	}

	line := dial(t, s).next(t)

	if reason, _ := line["reason"].(string); reason == "" {
		t.Errorf("line = %v, want a reason in it", line)
	}
}

func TestAClientFollowingMoreTunnelsThanExistIsCutOff(t *testing.T) {
	// A name can be watched before any view has arrived to say whether it
	// exists, which is the window where a map with no bound could be grown from
	// the wire.
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t) // hello

	for i := range maxWatch + 10 {
		c.send(t, fmt.Sprintf(`{"type":"watch","tunnel":"tunnel-%d"}`, i))
	}

	var watched int
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		watched = 0
		for client := range s.clients {
			watched = len(client.watch)
		}
		s.mu.Unlock()
		if watched >= maxWatch || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if watched != maxWatch {
		t.Errorf("watching %d tunnels, want it stopped at %d", watched, maxWatch)
	}
}

func TestALineNobodyCouldMeanDropsThatClientAndNobodyElse(t *testing.T) {
	// A peer writing without ever sending a newline would otherwise grow a
	// buffer until the process died. It is dropped; the publisher and every
	// other client carry on.
	s := serving(t, nil)
	quiet := dial(t, s)
	quiet.next(t)
	shouting := dial(t, s)
	shouting.next(t)

	// The write may well fail half way: the publisher drops the client the
	// moment the line is too long, which closes the socket underneath it. That
	// is the outcome, not a problem with the test.
	_, _ = shouting.Write([]byte(strings.Repeat("x", maxLine+1024) + "\n"))

	// The shouting one goes; the quiet one still gets what is published.
	s.Publish(aView("alpha"))
	if got := quiet.next(t)["type"]; got != "state" {
		t.Errorf("the other client got %v, want the view", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.clientCount() > 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.clientCount() != 1 {
		t.Errorf("clients = %d, want the shouting one dropped", s.clientCount())
	}
}

func TestTheHelloCarriesThePublicKey(t *testing.T) {
	// So the application can show its fingerprint beside the one
	// `sudo tun-manager feed-key` prints, and somebody can compare the two
	// without either of them being the key itself.
	s := serving(t, nil, func(s *Server) { s.FeedKey = knownSeed })

	hello := dial(t, s).next(t)

	pub, err := PublicKeyOfSeed(knownSeed)
	if err != nil {
		t.Fatalf("PublicKeyOfSeed: %v", err)
	}
	if got := hello["pubkey"]; got != base64.StdEncoding.EncodeToString(pub) {
		t.Errorf("pubkey = %v, want the public half of the configured key", got)
	}
	if line := fmt.Sprint(hello); strings.Contains(line, knownSeed) {
		t.Errorf("the hello carries the seed itself: %s", line)
	}
}

func TestTheHelloSaysNothingAboutAKeyThereIsNot(t *testing.T) {
	// A field carrying "" would have the application show an empty fingerprint
	// rather than say there is none.
	s := serving(t, nil)

	hello := dial(t, s).next(t)

	if _, carried := hello["pubkey"]; carried {
		t.Errorf("hello = %v, want no pubkey when there is no key", hello)
	}
}

func TestAKeyThatIsNotOneKeepsTheFeedQuietAboutIt(t *testing.T) {
	// Truncated by a copy and paste. The publisher still publishes - the feed
	// is what tells somebody their tunnels are down - and doctor is where the
	// key is diagnosed.
	s := serving(t, nil, func(s *Server) { s.FeedKey = "not a key" })

	hello := dial(t, s).next(t)

	if _, carried := hello["pubkey"]; carried {
		t.Errorf("hello = %v, want no pubkey when it cannot be read", hello)
	}
}

// knownSeed is a key of a known shape, so a test can name what it expects.
const knownSeed = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
