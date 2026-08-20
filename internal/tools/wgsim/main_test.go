package main

import (
	"bufio"
	"encoding/base64"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The simulator is a program people run while looking at something else — the
// terminal interface, the menu bar — so its own failures have to be legible on
// their own. These tests are about the two things it must never get wrong: the
// fixtures it writes, which are committed and diffed, and the sockets it binds,
// which are indistinguishable from a live tunnel's.

func TestEveryTunnelHasAKeyOfItsOwn(t *testing.T) {
	// The key is derived from the name so that the .conf on disk and the key on
	// the socket cannot drift apart. Two tunnels sharing one would make the
	// device matching in tun-manager pick whichever it saw first.
	seen := map[string]string{}
	for _, tun := range tunnels {
		key := tun.publicKey()
		if raw, err := base64.StdEncoding.DecodeString(key); err != nil || len(raw) != 32 {
			t.Errorf("%s has a key that is not a WireGuard key: %q", tun.name, key)
		}
		if other, ok := seen[key]; ok {
			t.Errorf("%s and %s share a key", tun.name, other)
		}
		seen[key] = tun.name
	}
}

func TestATunnelWithNoDeviceIsDown(t *testing.T) {
	// That absence is the whole way a tunnel is down here: no socket, no name
	// file, and tun-manager reads the silence.
	if (tunnel{device: "utun7"}).up() != true {
		t.Error("a tunnel with an interface is not up")
	}
	if (tunnel{}).up() != false {
		t.Error("a tunnel with no interface is up")
	}
}

func TestRunNeedsSomewhereToWrite(t *testing.T) {
	err := run("", "", "", false)

	if err == nil {
		t.Fatal("run wrote its fixtures nowhere in particular")
	}
	if !strings.Contains(err.Error(), "--config-dir") {
		t.Errorf("error %q does not say which flag is missing", err)
	}
}

func TestRunNeedsSomewhereToBind(t *testing.T) {
	err := run("", t.TempDir(), "", false)

	if err == nil {
		t.Fatal("run bound its sockets nowhere in particular")
	}
	if !strings.Contains(err.Error(), "--wg-socket") {
		t.Errorf("error %q does not say which flag is missing", err)
	}
}

func TestRunRefusesToBindWhereTheRealTunnelsLive(t *testing.T) {
	// The one thing this program must never do. Its sockets look exactly like a
	// live tunnel's, and the real wg would find them.
	err := run("", t.TempDir(), "/var/run/wireguard/", false)

	if err == nil {
		t.Fatal("the simulator agreed to bind in /var/run/wireguard")
	}
	if !strings.Contains(err.Error(), "real tunnels") {
		t.Errorf("error %q does not say why it refused", err)
	}
}

func TestWriteOnlyWritesTheFixturesAndStops(t *testing.T) {
	dir := t.TempDir()

	if err := run("", dir, "", true); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, tun := range tunnels {
		body, err := os.ReadFile(filepath.Join(dir, tun.name+".conf"))
		if err != nil {
			t.Fatalf("read %s: %v", tun.name, err)
		}
		if !strings.Contains(string(body), "# TO_CHECK="+tun.checkIP) {
			t.Errorf("%s carries no check address:\n%s", tun.name, body)
		}
		if strings.Contains(string(body), "\nPrivateKey") {
			t.Errorf("%s carries a private key, which no fixture may:\n%s", tun.name, body)
		}
		info, err := os.Stat(filepath.Join(dir, tun.name+".conf"))
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != confMode {
			t.Errorf("%s is %04o, want %04o", tun.name, info.Mode().Perm(), confMode)
		}
	}
}

func TestTheFixturesAreTheSameOnEveryRun(t *testing.T) {
	// `make demo-configs-check` tells a stale fixture from a fresh one with git
	// diff, which only works while the output is byte-for-byte stable.
	first, second := t.TempDir(), t.TempDir()

	if err := writeConfigs(first); err != nil {
		t.Fatalf("writeConfigs: %v", err)
	}
	if err := writeConfigs(second); err != nil {
		t.Fatalf("writeConfigs: %v", err)
	}

	for _, tun := range tunnels {
		one, err := os.ReadFile(filepath.Join(first, tun.name+".conf"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		other, err := os.ReadFile(filepath.Join(second, tun.name+".conf"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(one) != string(other) {
			t.Errorf("%s differs between two runs", tun.name)
		}
	}
}

func TestWriteConfigsReportsADirectoryItCannotMake(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := writeConfigs(filepath.Join(blocked, "config")); err == nil {
		t.Error("writeConfigs wrote into a path that is a file")
	}
}

func TestWriteConfigsReportsAFileItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	// A directory where the first .conf should go, so the write fails on it.
	if err := os.Mkdir(filepath.Join(dir, tunnels[0].name+".conf"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := writeConfigs(dir); err == nil {
		t.Error("writeConfigs reported success over a directory")
	}
}

func TestCheckNamesAcceptsAConfigurationItCanServe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("groups:\n  all: [alpha, bravo]\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := checkNames(path); err != nil {
		t.Errorf("checkNames: %v", err)
	}
}

func TestCheckNamesRefusesATunnelItDoesNotInvent(t *testing.T) {
	// Without it the demo shows a table with a missing row, and the reason is
	// two files apart.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("groups:\n  all: [alpha, zulu]\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := checkNames(path)

	if err == nil {
		t.Fatal("checkNames accepted a name it cannot serve")
	}
	if !strings.Contains(err.Error(), "zulu") {
		t.Errorf("error %q does not name the tunnel", err)
	}
}

func TestCheckNamesWithNoConfigurationChecksNothing(t *testing.T) {
	if err := checkNames(""); err != nil {
		t.Errorf("checkNames with no configuration: %v", err)
	}
}

func TestCheckNamesReportsAConfigurationItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("groups: [not, a, map]\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := checkNames(path); err == nil {
		t.Error("checkNames accepted a configuration that does not parse")
	}
}

func TestServingAnswersTheWayWireGuardDoes(t *testing.T) {
	// One socket per interface, the name file beside it, and a get answered
	// with the counters and a handshake counted back from now.
	dir := shortDir(t)
	stop := make(chan struct{})
	served := make(chan error, 1)
	go func() { served <- serveUntil(dir, stop) }()
	t.Cleanup(func() {
		close(stop)
		if err := <-served; err != nil {
			t.Errorf("serveUntil: %v", err)
		}
	})

	live := tunnels[0]
	path := filepath.Join(dir, live.device+".sock")
	waitFor(t, path)

	name, err := os.ReadFile(filepath.Join(dir, live.name+".name"))
	if err != nil {
		t.Fatalf("read the name file: %v", err)
	}
	if strings.TrimSpace(string(name)) != live.device {
		t.Errorf("name file says %q, want %q", name, live.device)
	}

	answer := ask(t, path)
	for _, want := range []string{
		"public_key=" + live.publicKey(),
		"endpoint=" + live.liveEndpoint,
		"errno=0",
	} {
		if !strings.Contains(answer, want) {
			t.Errorf("the answer does not contain %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "private_key") {
		t.Errorf("the simulator sent a private key:\n%s", answer)
	}
}

func TestATunnelThatIsDownGetsNoSocket(t *testing.T) {
	dir := shortDir(t)
	stop := make(chan struct{})
	served := make(chan error, 1)
	go func() { served <- serveUntil(dir, stop) }()
	t.Cleanup(func() {
		close(stop)
		<-served
	})

	waitFor(t, filepath.Join(dir, tunnels[0].device+".sock"))

	for _, tun := range tunnels {
		if tun.up() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, tun.name+".name")); !os.IsNotExist(err) {
			t.Errorf("%s is down and has a name file", tun.name)
		}
	}
}

func TestServingAgainTakesTheSocketBack(t *testing.T) {
	// A simulator killed with ^C leaves its sockets behind, and the next run
	// has to be able to start.
	dir := shortDir(t)
	for range 2 {
		stop := make(chan struct{})
		served := make(chan error, 1)
		go func() { served <- serveUntil(dir, stop) }()
		waitFor(t, filepath.Join(dir, tunnels[0].device+".sock"))
		close(stop)
		if err := <-served; err != nil {
			t.Fatalf("serveUntil: %v", err)
		}
	}
}

func TestServeReportsADirectoryItCannotMake(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := serveUntil(filepath.Join(blocked, "wireguard"), make(chan struct{})); err == nil {
		t.Error("serveUntil bound its sockets under a file")
	}
}

func TestServeReportsASocketItCannotBind(t *testing.T) {
	// A directory where a socket should go, with something in it: an empty one
	// would simply be removed by the unlink that clears a stale socket.
	dir := shortDir(t)
	inTheWay := filepath.Join(dir, tunnels[0].device+".sock")
	if err := os.Mkdir(inTheWay, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inTheWay, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := serveUntil(dir, make(chan struct{}))

	if err == nil {
		t.Fatal("serveUntil reported success without binding")
	}
	if !strings.Contains(err.Error(), "bind") {
		t.Errorf("error %q does not say what failed", err)
	}
}

func TestServeReportsANameFileItCannotWrite(t *testing.T) {
	dir := shortDir(t)
	if err := os.Mkdir(filepath.Join(dir, tunnels[0].name+".name"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := serveUntil(dir, make(chan struct{})); err == nil {
		t.Error("serveUntil reported success without writing the name files")
	}
}

func TestTheCountersClimb(t *testing.T) {
	// The rate graph has nothing to draw otherwise.
	c := &counters{rx: map[string]int64{}, tx: map[string]int64{}}
	live := tunnels[0]
	c.rx[live.name], c.tx[live.name] = live.rx, live.tx

	c.advance()

	if c.rx[live.name] != live.rx+live.rxRate {
		t.Errorf("rx = %d, want %d", c.rx[live.name], live.rx+live.rxRate)
	}
	if c.tx[live.name] != live.tx+live.txRate {
		t.Errorf("tx = %d, want %d", c.tx[live.name], live.tx+live.txRate)
	}
}

func TestTickKeepsTheCountersMovingUntilItIsStopped(t *testing.T) {
	c := &counters{rx: map[string]int64{}, tx: map[string]int64{}}
	stop := make(chan struct{})
	go c.tick(stop, time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		moved := c.rx[tunnels[0].name] > 0
		c.mu.Unlock()
		if moved {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the counters never moved")
		}
		time.Sleep(time.Millisecond)
	}
	close(stop)
}

func TestAConnectionThatSaysNothingIsDropped(t *testing.T) {
	// The read has a deadline: a client that opens a socket and goes away must
	// not hold a goroutine until the simulator is killed.
	dir := shortDir(t)
	stop := make(chan struct{})
	served := make(chan error, 1)
	go func() { served <- serveUntil(dir, stop) }()
	t.Cleanup(func() {
		close(stop)
		<-served
	})

	path := filepath.Join(dir, tunnels[0].device+".sock")
	waitFor(t, path)
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Closed without asking anything. What matters is that the simulator is
	// still answering afterwards.
	_ = conn.Close()

	if answer := ask(t, path); !strings.Contains(answer, "errno=0") {
		t.Errorf("the simulator stopped answering:\n%s", answer)
	}
}

func TestMainReportsWhatStoppedIt(t *testing.T) {
	var said error
	previousFatal, previousArgs, previousFlags := fatal, os.Args, flag.CommandLine
	fatal = func(v ...any) {
		if err, ok := v[0].(error); ok {
			said = err
		}
	}
	os.Args = []string{"wgsim"}
	flag.CommandLine = flag.NewFlagSet("wgsim", flag.ContinueOnError)
	t.Cleanup(func() { fatal, os.Args, flag.CommandLine = previousFatal, previousArgs, previousFlags })

	main()

	if said == nil || !strings.Contains(said.Error(), "--config-dir") {
		t.Errorf("main reported %v, want the missing flag", said)
	}
}

// TestMain silences the simulator's own log: it says which socket it bound on
// every run, and a suite that prints four lines per test is a suite nobody
// reads the failures of.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// shortDir is a directory whose name leaves room for a socket path.
// sockaddr_un.sun_path is 104 bytes on darwin, and t.TempDir() spends most of
// them on the name of the test.
func shortDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "wgsim")
	if err != nil {
		t.Fatalf("temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// waitFor blocks until a socket is there to talk to, so a test does not race
// the goroutine binding it.
func waitFor(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(time.Millisecond)
	}
}

// ask sends a get the way tun-manager does and returns the answer.
func ask(t *testing.T, path string) string {
	t.Helper()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	defer conn.Close() //nolint:errcheck

	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := conn.Write([]byte("get=1\n\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var answer strings.Builder
	lines := bufio.NewScanner(conn)
	for lines.Scan() {
		if lines.Text() == "" {
			break
		}
		answer.WriteString(lines.Text() + "\n")
	}
	return answer.String()
}

func TestRunPassesOnAConfigurationItCannotServe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("groups:\n  all: [zulu]\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := run(path, t.TempDir(), "", true); err == nil {
		t.Error("run started against a configuration it cannot serve")
	}
}

func TestRunPassesOnFixturesItCannotWrite(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := run("", filepath.Join(blocked, "config"), "", true); err == nil {
		t.Error("run reported success having written nothing")
	}
}

func TestRunServesUntilItIsInterrupted(t *testing.T) {
	// The whole program, from the flags to the socket to ^C. The signal is sent
	// to this process, so a handler of the test's own goes on first: without
	// it, an interrupt arriving before the simulator has installed its own
	// would kill the suite.
	ours := make(chan os.Signal, 1)
	signal.Notify(ours, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(ours) })

	dir := shortDir(t)
	done := make(chan error, 1)
	go func() { done <- run("", t.TempDir(), dir, false) }()

	waitFor(t, filepath.Join(dir, tunnels[0].device+".sock"))
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the simulator did not stop on an interrupt")
	}
}
