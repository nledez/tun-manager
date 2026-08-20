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
	"bytes"
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
	// Hooks are the commands wg-quick would run, as root, around bringing this
	// tunnel up and down. They are collected so they can be shown to somebody
	// before the file is trusted; nothing here ever runs one.
	Hooks []Hook
}

// Hook is one PreUp, PostUp, PreDown or PostDown line.
//
// wg-quick executes these as root, every time the tunnel goes up or down. A
// configuration downloaded from a provider can carry one, and it will run with
// every privilege tun-manager has — so the point of reading them here is to be
// able to put them in front of a person.
type Hook struct {
	// Key is the directive as it was written. wg-quick's parser is
	// case-insensitive, and showing "postup" back to somebody who wrote
	// "PostUp" makes them look for a line that is not there.
	Key string
	// Value is the command line, as written.
	Value string
	// Line is where it is in the file, counting from one.
	Line int
}

// hookKeys are the directives wg-quick executes. Table is not among them: it
// chooses how routes are installed and runs nothing, and crying wolf over it
// would teach people to skim the warning.
var hookKeys = map[string]bool{
	"preup":    true,
	"postup":   true,
	"predown":  true,
	"postdown": true,
}

// secretKeys are the values that must not be shown when a file is displayed.
// PublicKey is not among them: it is an identity rather than a secret, and it
// is what matches a configuration to a live interface.
var secretKeys = map[string]bool{
	"privatekey":   true,
	"presharedkey": true,
}

// Redact returns the file with every secret value replaced, and everything else
// left exactly as it was — same lines, same order, same spelling.
//
// It exists so a configuration can be shown back to whoever is importing it.
// What they are being asked to read is what wg-quick will run as root; what
// they must not have to think about is whether it is safe to have on screen.
func Redact(body []byte) []byte {
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		// Cut rather than splitKeyValue: the part before the "=" is kept as it
		// was written, indentation and all, so the file reads back the way it
		// is on disk.
		key, _, ok := strings.Cut(line, "=")
		if !ok || !secretKeys[strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		lines[i] = strings.TrimRight(key, " \t") + " = " + hidden
	}
	return []byte(strings.Join(lines, "\n"))
}

// hidden is what a secret is shown as. Not a row of asterisks the width of the
// value: the length of a key is not something worth publishing either.
const hidden = "(hidden)"

const toCheckMarker = "TO_CHECK"

// ParseFile reads a single WireGuard configuration file.
//
// It reads the whole file and parses what it read, rather than parsing as it
// reads: whoever imports a configuration is shown it first, and showing one
// file while importing another is the kind of difference nobody would notice.
func ParseFile(path string) (Tunnel, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Tunnel{}, err
	}
	return Parse(body, path)
}

// Parse reads a configuration out of bytes already in hand. path names where
// they came from: the tunnel takes its name from the file name, and wg-quick is
// handed the path rather than the contents.
func Parse(body []byte, path string) (Tunnel, error) {
	tun := Tunnel{
		Name: strings.TrimSuffix(filepath.Base(path), ".conf"),
		Path: path,
	}

	var section string
	var seenPeer bool
	var number int

	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		number++
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
			if hookKeys[strings.ToLower(key)] {
				tun.Hooks = append(tun.Hooks, Hook{Key: key, Value: value, Line: number})
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
