package wg

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultRunDir is where wg-quick records, for each tunnel it brought up, the
// name of the interface it created.
const DefaultRunDir = "/var/run/wireguard"

// Locator maps a tunnel name to the interface carrying it.
//
// It exists because peer public keys are not unique: two configs reaching the
// same server through different endpoints share one, and only the interface
// tells them apart.
type Locator interface {
	// Device returns the interface carrying the tunnel. The second result
	// reports whether the locator could answer at all; when it is false the
	// caller must fall back to matching by public key. When it is true, an
	// empty device name means the tunnel is down.
	Device(tunnel string) (device string, authoritative bool)
}

// RunDirLocator reads the "<tunnel>.name" files wg-quick writes on darwin.
type RunDirLocator struct {
	Dir string
}

// Device reads the interface name recorded for a tunnel.
func (l RunDirLocator) Device(tunnel string) (string, bool) {
	dir := l.Dir
	if dir == "" {
		dir = DefaultRunDir
	}
	if _, err := os.Stat(dir); err != nil {
		// No run directory: this machine does not use wg-quick's convention, or
		// we cannot read it. Let the caller fall back.
		return "", false
	}

	data, err := os.ReadFile(filepath.Join(dir, tunnel+".name"))
	if err != nil {
		// The directory exists, so the absence of a name file is an answer: the
		// tunnel is not up.
		return "", true
	}
	return strings.TrimSpace(string(data)), true
}
