package wg

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// UserspaceClient reads WireGuard state from the UAPI sockets in one directory.
//
// wgctrl does the same thing against a directory list compiled into it, and
// that list is not reachable from outside the module. This exists so the
// directory can be chosen: it is what `--wg-socket` points at, and what lets
// internal/tools/wgsim stand in for a machine with tunnels on it.
//
// The protocol is wireguard-go's: connect, write "get=1\n\n", read key=value
// lines until a blank one. There is no verb for "list the devices" — a socket
// carries exactly one, and its name is the file's. That is why this takes a
// directory rather than a socket.
type UserspaceClient struct {
	// Dir holds one socket per live interface. Empty means DefaultRunDir.
	Dir string

	// dial opens one socket. Unexported and injectable only from the tests:
	// the two things that can go wrong after a successful dial - a deadline
	// refused, a write to a peer that has just gone - are real conditions with
	// no way to provoke them through a real socket, and a seam is cheaper than
	// two paragraphs arguing they cannot happen.
	dial func(path string) (net.Conn, error)
}

// dialTimeout bounds both halves of the exchange. These sockets are local and
// answer in microseconds; a peer that does not is one that has stopped, and
// waiting on it would hang a refresh.
const dialTimeout = 2 * time.Second

func (c UserspaceClient) dir() string {
	if c.Dir == "" {
		return DefaultRunDir
	}
	return c.Dir
}

// Devices returns every interface with a socket in the directory.
//
// A directory that is not there is not an error: it is what a machine with no
// tunnel up looks like, and reporting it as a failure would turn "nothing is
// running" into a red line.
func (c UserspaceClient) Devices() ([]*wgtypes.Device, error) {
	entries, err := os.ReadDir(c.dir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", c.dir(), err)
	}

	var devices []*wgtypes.Device
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || info.Mode()&fs.ModeSocket == 0 {
			// The same directory holds the "<name>.name" files wg-quick
			// writes, so anything that is not a socket belongs to somebody
			// else.
			continue
		}
		device, devErr := c.read(filepath.Join(c.dir(), entry.Name()))
		if devErr != nil {
			return nil, devErr
		}
		devices = append(devices, device)
	}
	return devices, nil
}

// Close releases nothing: a connection lives for one exchange and is closed
// there. The method exists because Client requires it.
func (UserspaceClient) Close() error { return nil }

// read runs one get exchange against a socket.
func (c UserspaceClient) read(path string) (*wgtypes.Device, error) {
	open := c.dial
	if open == nil {
		open = func(p string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: dialTimeout}
			return dialer.DialContext(context.Background(), "unix", p)
		}
	}

	conn, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", path, err)
	}
	defer conn.Close() //nolint:errcheck // read-only exchange, nothing to flush

	if err := conn.SetDeadline(time.Now().Add(dialTimeout)); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := conn.Write([]byte("get=1\n\n")); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	device := &wgtypes.Device{
		// The socket file name is the interface name: the protocol carries no
		// other identity for it.
		Name: strings.TrimSuffix(filepath.Base(path), ".sock"),
		Type: wgtypes.Userspace,
	}
	return device, parse(conn, path, device)
}

// parse turns the response into a device. Anything this program does not read
// is skipped rather than rejected, which is what lets it face a newer
// wireguard-go.
func parse(r net.Conn, path string, device *wgtypes.Device) error {
	var peer *wgtypes.Peer
	// The handshake arrives as two keys, so it is assembled rather than set.
	var sec, nsec int64

	lines := bufio.NewScanner(r)
	for lines.Scan() {
		line := lines.Text()
		if line == "" {
			break
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s: %q is not a key=value line", path, line)
		}

		switch key {
		case "errno":
			code, convErr := strconv.Atoi(value)
			if convErr != nil {
				return fmt.Errorf("%s: errno %q is not a number", path, value)
			}
			if code != 0 {
				return fmt.Errorf("%s: wireguard returned errno %d", path, code)
			}
		case "private_key":
			// Skipped on purpose, and named here so nobody adds it back by
			// filling in a gap. This program never reads a private key; a field
			// on the struct is all it would take for one to reach a log line.
			continue
		case "public_key":
			// A device's peers arrive one after another, each opened by its own
			// public key.
			device.Peers = append(device.Peers, wgtypes.Peer{})
			peer = &device.Peers[len(device.Peers)-1]
			sec, nsec = 0, 0
			decoded, keyErr := base64.StdEncoding.DecodeString(value)
			if keyErr != nil || len(decoded) != wgtypes.KeyLen {
				return fmt.Errorf("%s: %q is not a WireGuard key", path, value)
			}
			peer.PublicKey = wgtypes.Key(decoded)
		case "endpoint", "last_handshake_time_sec", "last_handshake_time_nsec",
			"rx_bytes", "tx_bytes":
			if peer == nil {
				return fmt.Errorf("%s: %s arrived before any public_key", path, key)
			}
			if err := peerField(peer, key, value, &sec, &nsec); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// peerField fills one field of the peer being read.
func peerField(peer *wgtypes.Peer, key, value string, sec, nsec *int64) error {
	switch key {
	case "endpoint":
		addr, err := net.ResolveUDPAddr("udp", value)
		if err != nil {
			return fmt.Errorf("endpoint %q: %w", value, err)
		}
		peer.Endpoint = addr
		return nil
	case "last_handshake_time_sec", "last_handshake_time_nsec":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%s %q is not a number", key, value)
		}
		if key == "last_handshake_time_sec" {
			*sec = n
		} else {
			*nsec = n
		}
		// Zero means it has never handshaken, and time.Unix(0, 0) is 1970
		// rather than nothing — which reads as a handshake fifty years ago.
		if *sec == 0 && *nsec == 0 {
			peer.LastHandshakeTime = time.Time{}
		} else {
			peer.LastHandshakeTime = time.Unix(*sec, *nsec)
		}
		return nil
	default:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%s %q is not a number", key, value)
		}
		if key == "rx_bytes" {
			peer.ReceiveBytes = n
		} else {
			peer.TransmitBytes = n
		}
		return nil
	}
}
