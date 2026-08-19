// Command wgsim stands in for a machine with WireGuard tunnels on it.
//
// It writes the .conf files that tun-manager reads, and serves the UAPI
// sockets that tun-manager's --wg-socket points at. Everything it reports is
// invented: the names are the repository's placeholders and the addresses come
// from the ranges reserved for documentation (RFC 5737 and RFC 3849), so no
// fixture can name a real host.
//
// It exists so the program can be seen working - and photographed for the
// README - without anybody's real network in the picture.
//
// Never point it at /var/run/wireguard. It binds sockets that look exactly like
// a live tunnel's, and the real wg would find them.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"ledez.net/tun-manager/internal/profile"
)

// tunnel is one invented tunnel, and everything the simulator says about it.
//
// The table is fixed rather than random: two runs of the same demo produce the
// same picture, so a screenshot can be retaken and still match the text beside
// it.
type tunnel struct {
	name string
	// device is empty for a tunnel that is down: down means no interface, so
	// there is neither a socket nor a name file.
	device string
	// handshakeAge is how long ago it last handshook. Over wg.StaleAfter is
	// what makes a tunnel read as stale rather than up.
	handshakeAge time.Duration
	// endpoint is what the .conf file says, which may be a DNS name.
	endpoint string
	// liveEndpoint is what the socket reports: always an address, because
	// wireguard-go has resolved it by then. Keeping the two apart is what makes
	// the demo show the real behaviour - the table prefers the resolved one,
	// which is more use than a name when something is wrong.
	liveEndpoint string
	checkIP      string
	address      string
	allowedIPs   string
	rx, tx       int64
	// rxRate and txRate are how fast the counters climb, in bytes a second. A
	// flat counter draws a flat graph, and different rates keep the two lines
	// of the chart apart.
	rxRate, txRate int64
}

// tunnels is the demo, written out. The names are the ones in
// configs/config.example.yaml.
//
// One of each thing worth showing: two ordinary tunnels carrying traffic, one
// that has not handshaken in long enough to be stale, one that is down, and one
// reached over IPv6.
var tunnels = []tunnel{
	{
		name: "alpha", device: "utun4", handshakeAge: 12 * time.Second,
		endpoint: "192.0.2.10:51820", liveEndpoint: "192.0.2.10:51820",
		checkIP: "192.0.2.11", address: "192.0.2.2/32",
		allowedIPs: "192.0.2.0/24",
		rx:         4_404_019, tx: 38_205_849, rxRate: 24_000, txRate: 3_800,
	},
	{
		name: "bravo", device: "utun5", handshakeAge: 47 * time.Second,
		endpoint: "198.51.100.20:51820", liveEndpoint: "198.51.100.20:51820",
		checkIP: "198.51.100.21", address: "198.51.100.2/32",
		allowedIPs: "198.51.100.0/24",
		rx:         16_252_928, tx: 1_992_294, rxRate: 141_000, txRate: 9_600,
	},
	{
		name: "charlie", device: "utun6", handshakeAge: 4 * time.Minute,
		endpoint: "charlie.example:51820", liveEndpoint: "203.0.113.10:51820",
		checkIP: "203.0.113.11", address: "203.0.113.2/32",
		allowedIPs: "203.0.113.0/24",
		rx:         5_120, tx: 10_240, rxRate: 0, txRate: 0,
	},
	{
		name: "delta", device: "utun7", handshakeAge: 89 * time.Second,
		endpoint: "delta.example:51820", liveEndpoint: "[2001:db8::a]:51820",
		checkIP: "2001:db8::b", address: "2001:db8:1::2/128",
		allowedIPs: "2001:db8::/64",
		rx:         2_048, tx: 5_120, rxRate: 320, txRate: 180,
	},
	{
		name: "echo", device: "", handshakeAge: 0,
		endpoint: "echo.example:51820", checkIP: "192.0.2.31", address: "192.0.2.3/32",
		allowedIPs: "192.0.2.0/24",
	},
}

// up reports whether this tunnel has an interface at all.
func (t tunnel) up() bool { return t.device != "" }

// publicKey is derived from the name, so the .conf file the simulator writes
// and the key it serves on the socket cannot drift apart.
func (t tunnel) publicKey() string {
	sum := sha256.Sum256([]byte("tun-manager demo peer " + t.name))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("wgsim: ")

	configPath := flag.String("config", "", "configuration to take the tunnel names from")
	configDir := flag.String("config-dir", "", "where to write the .conf files")
	wgSocket := flag.String("wg-socket", "", "directory to bind the UAPI sockets in")
	writeOnly := flag.Bool("write-only", false, "write the .conf files and exit")
	flag.Parse()

	if err := run(*configPath, *configDir, *wgSocket, *writeOnly); err != nil {
		log.Fatal(err)
	}
}

func run(configPath, configDir, wgSocket string, writeOnly bool) error {
	if configDir == "" {
		return errors.New("--config-dir is required: it is where the .conf files go")
	}
	if err := checkNames(configPath); err != nil {
		return err
	}
	if err := writeConfigs(configDir); err != nil {
		return err
	}
	if writeOnly {
		return nil
	}

	if wgSocket == "" {
		return errors.New("--wg-socket is required: it is where the UAPI sockets go")
	}
	// The one thing this program must never do. Its sockets are
	// indistinguishable from a live tunnel's, and the real wg would find them.
	if filepath.Clean(wgSocket) == "/var/run/wireguard" {
		return errors.New("refusing to bind in /var/run/wireguard: that is where the real tunnels live")
	}
	return serve(wgSocket)
}

// checkNames reports a configuration whose tunnels are not the ones this
// simulator invents. Without it the demo shows an empty table and the reason is
// two files apart.
func checkNames(path string) error {
	if path == "" {
		return nil
	}
	cfg, err := profile.Load(path)
	if err != nil {
		return err
	}

	known := map[string]bool{}
	for _, t := range tunnels {
		known[t.name] = true
	}
	for _, name := range cfg.Groups[profile.GroupAll] {
		if !known[name] {
			return fmt.Errorf(
				"%s lists %q, which this simulator does not invent: add it to the table in %s",
				path, name, "internal/tools/wgsim/main.go")
		}
	}
	return nil
}

// confMode is what a generated .conf is written as. They hold no secret - there
// is no PrivateKey in them - but tun-manager's own imports are 0600 and a demo
// that models something looser would be teaching the wrong habit.
const confMode = 0o600

// writeConfigs writes one .conf per tunnel.
//
// Byte-for-byte the same on every run, so `make demo-configs-check` can tell a
// stale file from a fresh one with git diff.
func writeConfigs(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, t := range tunnels {
		path := filepath.Join(dir, t.name+".conf")
		if err := os.WriteFile(path, []byte(conf(t)), confMode); err != nil {
			return err
		}
	}
	return nil
}

func conf(t tunnel) string {
	return fmt.Sprintf(`# tun-manager demo fixture, written by internal/tools/wgsim. Do not edit:
# `+"`make demo-configs`"+` rewrites it.
#
# There is deliberately no PrivateKey. tun-manager never reads one - the parser
# knows only PublicKey - so leaving it out keeps every key out of the repository,
# and means wg-quick refuses the file, which is what stops a demo fixture from
# ever bringing anything up.
#
# Addresses come from the ranges reserved for documentation: RFC 5737 for IPv4,
# RFC 3849 for IPv6.

# TO_CHECK=%s

[Interface]
Address = %s

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = %s
`, t.checkIP, t.address, t.publicKey(), t.endpoint, t.allowedIPs)
}

// counters are the byte counts, which climb while the simulator runs. Guarded
// because the ticker writes them while the sockets read them.
type counters struct {
	mu sync.Mutex
	rx map[string]int64
	tx map[string]int64
}

func serve(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	c := &counters{rx: map[string]int64{}, tx: map[string]int64{}}
	for _, t := range tunnels {
		c.rx[t.name], c.tx[t.name] = t.rx, t.tx
	}

	var listeners []net.Listener
	for _, t := range tunnels {
		if !t.up() {
			// A tunnel that is down has no interface: no socket, and no name
			// file either. That absence is exactly what tun-manager reads as
			// down.
			continue
		}
		name := filepath.Join(dir, t.name+".name")
		if err := os.WriteFile(name, []byte(t.device+"\n"), 0o644); err != nil {
			return err
		}

		path := filepath.Join(dir, t.device+".sock")
		_ = os.Remove(path)
		var config net.ListenConfig
		ln, err := config.Listen(context.Background(), "unix", path)
		if err != nil {
			return fmt.Errorf("bind %s: %w", path, err)
		}
		listeners = append(listeners, ln)
		go accept(ln, t, c)
		log.Printf("%s on %s (%s)", t.name, t.device, path)
	}

	go c.tick()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	log.Printf("serving %d tunnels in %s; ^C to stop", len(listeners), dir)
	<-stop

	for _, ln := range listeners {
		_ = ln.Close()
	}
	log.Print("stopped")
	return nil
}

// tick advances the counters, so the rate charts have something to draw.
func (c *counters) tick() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		c.mu.Lock()
		for _, tun := range tunnels {
			c.rx[tun.name] += tun.rxRate
			c.tx[tun.name] += tun.txRate
		}
		c.mu.Unlock()
	}
}

func accept(ln net.Listener, t tunnel, c *counters) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go answer(conn, t, c)
	}
}

// answer serves one get. The request is read and discarded: this socket carries
// one device, so "get=1" is the only thing it can be.
func answer(conn net.Conn, t tunnel, c *counters) {
	defer conn.Close() //nolint:errcheck

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		return
	}

	c.mu.Lock()
	rx, tx := c.rx[t.name], c.tx[t.name]
	c.mu.Unlock()

	// Counted back from now rather than from when the simulator started: what
	// is fixed about a tunnel here is how long ago it handshook, not when. An
	// instant fixed at startup means every tunnel drifts into stale after three
	// minutes of the demo being left open, which is what the table then shows.
	handshake := time.Now().Add(-t.handshakeAge)

	// No private_key. A real wireguard-go sends one; there is nothing here to
	// send, and tun-manager would drop it anyway.
	_, _ = fmt.Fprintf(conn, "listen_port=51820\n"+
		"public_key=%s\n"+
		"endpoint=%s\n"+
		"last_handshake_time_sec=%d\n"+
		"last_handshake_time_nsec=0\n"+
		"rx_bytes=%d\n"+
		"tx_bytes=%d\n"+
		"errno=0\n\n",
		t.publicKey(), t.liveEndpoint, handshake.Unix(), rx, tx)
}
