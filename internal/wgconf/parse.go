// Package wgconf reads WireGuard .conf files without ever retaining secrets.
//
// Only the fields tun-manager needs are kept: the tunnel name (derived from the
// file name, which is what wg-quick uses as the interface name), the peer public
// key (the stable identity used to match a config against a live device), the
// endpoint, the allowed IPs and the address to probe.
//
// PrivateKey and PresharedKey are deliberately ignored.
package wgconf

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Tunnel is the static description of a tunnel, as read from its .conf file.
type Tunnel struct {
	// Name is the file name without its .conf suffix, e.g. "alpha".
	Name string
	// Path is the full path of the .conf file, to hand over to wg-quick.
	Path string
	// PeerPublicKey identifies the tunnel among the live WireGuard devices.
	PeerPublicKey string
	// Endpoint is the peer endpoint, "host:port". It may be a DNS name.
	Endpoint string
	// AllowedIPs are the peer's allowed IPs, in declaration order.
	AllowedIPs []string
	// Address is the local address of the interface.
	Address string
	// CheckIP is the remote address to ping to tell whether the tunnel works.
	CheckIP string
	// CheckIPInferred reports that CheckIP was guessed rather than read from a
	// "# TO_CHECK=" comment.
	CheckIPInferred bool
}

const toCheckMarker = "TO_CHECK"

// ParseFile reads a single WireGuard configuration file.
func ParseFile(path string) (Tunnel, error) {
	f, err := os.Open(path)
	if err != nil {
		return Tunnel{}, err
	}
	// Read-only: a failure to close has nothing to report.
	defer func() { _ = f.Close() }()

	tun := Tunnel{
		Name: strings.TrimSuffix(filepath.Base(path), ".conf"),
		Path: path,
	}

	var section string
	var seenPeer bool

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			if ip, ok := parseCheckComment(line); ok {
				tun.CheckIP = ip
			}
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "peer" {
				seenPeer = true
			}
			continue
		}

		key, value, ok := splitKeyValue(line)
		if !ok {
			continue
		}

		switch section {
		case "interface":
			if strings.EqualFold(key, "Address") {
				tun.Address = value
			}
		case "peer":
			// Only the first peer matters: every config here is a client with a
			// single server peer.
			if tun.PeerPublicKey != "" && strings.EqualFold(key, "PublicKey") {
				continue
			}
			switch {
			case strings.EqualFold(key, "PublicKey"):
				tun.PeerPublicKey = value
			case strings.EqualFold(key, "Endpoint"):
				tun.Endpoint = value
			case strings.EqualFold(key, "AllowedIPs"):
				tun.AllowedIPs = append(tun.AllowedIPs, splitList(value)...)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Tunnel{}, fmt.Errorf("read %s: %w", path, err)
	}

	if !seenPeer || tun.PeerPublicKey == "" {
		return Tunnel{}, fmt.Errorf("%s: no [Peer] section with a PublicKey", path)
	}

	if tun.CheckIP == "" {
		tun.CheckIP = inferCheckIP(tun)
		tun.CheckIPInferred = tun.CheckIP != ""
	}

	return tun, nil
}

// LoadDir parses every .conf file of a directory, sorted by tunnel name.
func LoadDir(dir string) ([]Tunnel, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.conf"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no *.conf file in %s", dir)
	}

	tuns := make([]Tunnel, 0, len(paths))
	for _, p := range paths {
		tun, err := ParseFile(p)
		if err != nil {
			return nil, err
		}
		tuns = append(tuns, tun)
	}
	sort.Slice(tuns, func(i, j int) bool { return tuns[i].Name < tuns[j].Name })
	return tuns, nil
}

// parseCheckComment recognises the "# TO_CHECK=<ip>" convention: the address to
// probe to tell whether the tunnel works. Trailing semicolons are tolerated.
func parseCheckComment(line string) (string, bool) {
	body := strings.TrimLeft(line, "#; \t")
	key, value, ok := splitKeyValue(body)
	if !ok || !strings.EqualFold(key, toCheckMarker) {
		return "", false
	}
	// Trailing semicolons are a comment habit, and they may be spaced away from
	// the value; neither must end up in the address handed to the pinger.
	value = strings.TrimSpace(strings.TrimRight(value, "; \t"))
	if value == "" {
		return "", false
	}
	return value, true
}

func splitKeyValue(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(key), strings.TrimSpace(value), true
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// inferCheckIP guesses a probe target when the config carries no TO_CHECK
// comment: a single-host AllowedIPs entry first, then the endpoint host.
func inferCheckIP(tun Tunnel) string {
	for _, cidr := range tun.AllowedIPs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			continue
		}
		if prefix.Bits() == prefix.Addr().BitLen() {
			return prefix.Addr().String()
		}
	}
	if tun.Endpoint != "" {
		if host, _, err := net.SplitHostPort(tun.Endpoint); err == nil {
			return host
		}
		return tun.Endpoint
	}
	return ""
}
