package wg

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// socketDir returns a directory short enough to hold unix socket paths.
//
// t.TempDir() is not usable here: on darwin it sits under a per-test path long
// enough to overrun the 104 bytes of sun_path, and the bind fails with
// "invalid argument" rather than anything that names the real cause.
func socketDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "tm")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// serveUAPI binds a socket named after the interface and answers every get with
// the same canned response.
func serveUAPI(t *testing.T, dir, iface, response string) {
	t.Helper()

	ln, err := net.Listen("unix", filepath.Join(dir, iface+".sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close() //nolint:errcheck
		wg.Wait()
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close() //nolint:errcheck
				// The request is read and discarded: this stands in for one
				// device, so "get=1" is the only thing it can be.
				buf := make([]byte, 64)
				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, _ = conn.Read(buf)
				_, _ = conn.Write([]byte(response))
			}()
		}
	}()
}

// aLiveDevice is what wireguard-go answers for one interface carrying one peer.
const aLiveDevice = `private_key=8Ay0000000000000000000000000000000000000000=
listen_port=51820
public_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=
endpoint=192.0.2.10:51820
last_handshake_time_sec=1786968191
last_handshake_time_nsec=250000000
rx_bytes=184320
tx_bytes=92160
errno=0

`

func TestTheDirectoryDecidesWhichDevicesAreSeen(t *testing.T) {
	// The whole reason this type exists: wgctrl's directory list is compiled
	// into it and cannot be reached from outside the module.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", aLiveDevice)

	devices, err := UserspaceClient{Dir: dir}.Devices()

	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "utun7" {
		t.Fatalf("devices = %+v, want utun7 alone", devices)
	}
}

func TestAPeerCarriesEverythingTheTableShows(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", aLiveDevice)

	devices, err := UserspaceClient{Dir: dir}.Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	peers := devices[0].Peers
	if len(peers) != 1 {
		t.Fatalf("peers = %+v, want one", peers)
	}
	peer := peers[0]
	if got := peer.Endpoint.String(); got != "192.0.2.10:51820" {
		t.Errorf("endpoint = %q, want the one on the wire", got)
	}
	if got := peer.LastHandshakeTime.Unix(); got != 1786968191 {
		t.Errorf("handshake = %v, want the seconds it was given", got)
	}
	if peer.ReceiveBytes != 184320 || peer.TransmitBytes != 92160 {
		t.Errorf("counters = %d/%d, want 184320/92160", peer.ReceiveBytes, peer.TransmitBytes)
	}
}

func TestThePrivateKeyIsNeverKept(t *testing.T) {
	// The response carries one, because a real socket does. This program has no
	// use for it, and a field holding it is all it would take for one to reach
	// a log line.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", aLiveDevice)

	devices, err := UserspaceClient{Dir: dir}.Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	var zero [32]byte
	if devices[0].PrivateKey != zero {
		t.Error("the private key was read off the wire and stored")
	}
}

func TestSeveralPeersOnOneDeviceAreKeptApart(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", `public_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=
rx_bytes=100
public_key=MDEyMzQ1Njc4OUFCQ0RFRkdISUpLTE1OT1BRUlNUVVY=
rx_bytes=200
errno=0

`)

	devices, err := UserspaceClient{Dir: dir}.Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	peers := devices[0].Peers
	if len(peers) != 2 {
		t.Fatalf("peers = %d, want two", len(peers))
	}
	if peers[0].ReceiveBytes != 100 || peers[1].ReceiveBytes != 200 {
		t.Errorf("counters = %d/%d, want each peer to keep its own",
			peers[0].ReceiveBytes, peers[1].ReceiveBytes)
	}
}

func TestATunnelThatHasNeverHandshakenHasNoHandshakeTime(t *testing.T) {
	// Zero on the wire means never. time.Unix(0, 0) is 1970, which the table
	// would render as a handshake fifty years old rather than as none.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", `public_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=
last_handshake_time_sec=0
last_handshake_time_nsec=0
errno=0

`)

	devices, err := UserspaceClient{Dir: dir}.Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	if !devices[0].Peers[0].LastHandshakeTime.IsZero() {
		t.Errorf("handshake = %v, want no time at all",
			devices[0].Peers[0].LastHandshakeTime)
	}
}

func TestAKeyThisProgramDoesNotReadIsIgnoredRatherThanRefused(t *testing.T) {
	// It is how this survives a newer wireguard-go that added a field.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", `public_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=
persistent_keepalive_interval=25
protocol_version=1
something_invented_later=yes
rx_bytes=42
errno=0

`)

	devices, err := UserspaceClient{Dir: dir}.Devices()

	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if devices[0].Peers[0].ReceiveBytes != 42 {
		t.Error("the keys after the unknown one were lost")
	}
}

func TestAnErrnoIsReported(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", "errno=1\n\n")

	_, err := UserspaceClient{Dir: dir}.Devices()

	if err == nil {
		t.Fatal("Devices accepted a failed get")
	}
	if !strings.Contains(err.Error(), "errno") {
		t.Errorf("err = %v, want it to name the errno", err)
	}
}

func TestALineThatIsNotKeyValueIsReported(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", "this is not a uapi response\n\n")

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted something that is not the protocol")
	}
}

func TestAKeyThatIsNotAWireGuardKeyIsReported(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", "public_key=not-base64!\nerrno=0\n\n")

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted a public key that is not one")
	}
}

func TestACounterBeforeAnyPeerIsReported(t *testing.T) {
	// It would otherwise be written into whatever peer came last, or panic on
	// none at all.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", "rx_bytes=42\nerrno=0\n\n")

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted a counter belonging to no peer")
	}
}

func TestACounterThatIsNotANumberIsReported(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7",
		"public_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=\nrx_bytes=lots\nerrno=0\n\n")

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted a counter that is not a number")
	}
}

func TestAHandshakeThatIsNotANumberIsReported(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7",
		"public_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=\n"+
			"last_handshake_time_sec=recently\nerrno=0\n\n")

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted a handshake time that is not a number")
	}
}

func TestAnEndpointThatIsNotAnAddressIsReported(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7",
		"public_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU=\nendpoint=nowhere\nerrno=0\n\n")

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted an endpoint that is not an address")
	}
}

func TestAnErrnoThatIsNotANumberIsReported(t *testing.T) {
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", "errno=broken\n\n")

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted an errno that is not a number")
	}
}

func TestAnythingThatIsNotASocketIsSkipped(t *testing.T) {
	// The same directory holds the "<name>.name" files wg-quick writes.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", aLiveDevice)
	if err := os.WriteFile(filepath.Join(dir, "alpha.name"), []byte("utun7\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	devices, err := UserspaceClient{Dir: dir}.Devices()

	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("devices = %+v, want the name file skipped", devices)
	}
}

func TestADirectoryThatIsNotThereMeansNothingIsRunning(t *testing.T) {
	// A machine with no tunnel up. Reporting it as a failure would turn
	// "nothing is running" into a red line across the interface.
	devices, err := UserspaceClient{Dir: "/tmp/tm-there-is-no-such-directory"}.Devices()

	if err != nil {
		t.Errorf("Devices: %v, want an absent directory to be no news", err)
	}
	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none", devices)
	}
}

func TestAnUnreadableDirectoryIsReported(t *testing.T) {
	// Distinct from an absent one: this is something wrong rather than nothing
	// running.
	dir := socketDir(t)
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := (UserspaceClient{Dir: file}).Devices(); err == nil {
		t.Error("Devices accepted a regular file as a socket directory")
	}
}

func TestASocketNobodyIsListeningOnIsReported(t *testing.T) {
	// What a crashed wireguard-go leaves behind.
	dir := socketDir(t)
	ln, err := net.Listen("unix", filepath.Join(dir, "utun7.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Closed without unlinking, which is the state that matters.
	if unix, ok := ln.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	ln.Close() //nolint:errcheck

	if _, err := (UserspaceClient{Dir: dir}).Devices(); err == nil {
		t.Error("Devices accepted a socket with nobody behind it")
	}
}

func TestTheDefaultDirectoryIsTheOneWgQuickUses(t *testing.T) {
	// An empty Dir must not mean the working directory.
	if got := (UserspaceClient{}).dir(); got != DefaultRunDir {
		t.Errorf("dir = %q, want %q", got, DefaultRunDir)
	}
}

func TestClosingReleasesNothingAndSaysSo(t *testing.T) {
	// A connection lives for one exchange and is closed there. The method
	// exists because Client requires it.
	if err := (UserspaceClient{}).Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestAReaderCanBePointedAtADirectory(t *testing.T) {
	// The seam --wg-socket reaches: same parsing, same state, chosen directory.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", aLiveDevice)

	state, err := NewReaderIn(dir).Read()

	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(state) != 1 || state[0].Device != "utun7" {
		t.Fatalf("state = %+v, want the one device in that directory", state)
	}
	if state[0].RxBytes != 184320 {
		t.Errorf("rx = %d, want the counter off the wire", state[0].RxBytes)
	}
}

// brokenConn fails at whichever step it was told to.
//
// The two branches it reaches are real - a peer that goes between the dial and
// the write is a wireguard-go that just crashed - but neither can be provoked
// through a socket the test controls, because both happen in the window between
// two adjacent statements.
type brokenConn struct {
	net.Conn
	failDeadline bool
	failWrite    bool
}

func (c brokenConn) SetDeadline(t time.Time) error {
	if c.failDeadline {
		return errors.New("use of closed network connection")
	}
	return c.Conn.SetDeadline(t)
}

func (c brokenConn) Write(b []byte) (int, error) {
	if c.failWrite {
		return 0, errors.New("broken pipe")
	}
	return c.Conn.Write(b)
}

// dialing returns a client whose connections break as asked. Nothing is bound:
// the connection is a pipe, and the path is never opened.
//
// The far end stays open for as long as the test runs. Closing it would make
// net.Pipe refuse the deadline as well, and a test that fails at the first of
// two steps proves nothing about the second.
func dialing(t *testing.T, broken brokenConn) UserspaceClient {
	t.Helper()

	return UserspaceClient{
		Dir: "/tmp",
		dial: func(string) (net.Conn, error) {
			ours, theirs := net.Pipe()
			t.Cleanup(func() {
				ours.Close()   //nolint:errcheck
				theirs.Close() //nolint:errcheck
			})
			broken.Conn = ours
			return broken, nil
		},
	}
}

func TestADeadlineTheConnectionRefusesIsReported(t *testing.T) {
	_, err := dialing(t, brokenConn{failDeadline: true}).read("/tmp/utun7.sock")

	if err == nil {
		t.Error("read carried on with no deadline, which is how a refresh hangs")
	}
}

func TestAPeerThatWentBetweenTheDialAndTheWriteIsReported(t *testing.T) {
	// A wireguard-go that crashed in that window.
	_, err := dialing(t, brokenConn{failWrite: true}).read("/tmp/utun7.sock")

	if err == nil {
		t.Error("read reported a device it never managed to ask for")
	}
}

func TestALineTooLongToReadIsReportedRatherThanTruncated(t *testing.T) {
	// bufio.Scanner refuses a token over 64KB. Returning the device parsed so
	// far would report a tunnel with whatever fraction of its peers fitted.
	dir := socketDir(t)
	serveUAPI(t, dir, "utun7", "public_key="+strings.Repeat("A", 128*1024))

	_, err := (UserspaceClient{Dir: dir}).Devices()

	if err == nil {
		t.Error("Devices accepted a response it could not read to the end of")
	}
}
