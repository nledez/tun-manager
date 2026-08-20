package profile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"gopkg.in/yaml.v3"

	"ledez.net/tun-manager/internal/wg"
)

// Where the privileged settings live, and why it is a constant rather than a
// setting.
//
// Everything in this file decides what root executes, writes or removes:
// which binary is run, which socket is bound and then unlinked, which
// directory is read as the list of tunnels. A path that can be written by a
// plain user is a path that a plain user can point at anything, so these
// settings are read from a file only root can write - and the location of that
// file is not itself one of them. A path that cannot be chosen is a path that
// does not have to be defended.
const (
	// ConfigDir holds the .conf files and the privileged settings beside them.
	ConfigDir = "/private/wireguard/config"

	// PrivilegedName is the file, so `import` and the *.conf glob can be told
	// apart from it by name alone.
	PrivilegedName = "tun-manager.yaml"

	// PrivilegedPath is where LoadPrivileged reads from in production.
	PrivilegedPath = ConfigDir + "/" + PrivilegedName

	// PrivilegedFileMode is the loosest mode the file may have. It carries the
	// feed signing key: nobody but root needs it.
	PrivilegedFileMode os.FileMode = 0o600
)

// Secret is a value that must never reach a log line or an error message.
//
// It is a type rather than a discipline because discipline is what fails: a
// `%v` on the surrounding struct, a wrapped error carrying the configuration
// along, a debug print left behind. String and GoString make every formatting
// verb render it as nothing, and Reveal is the one way to the bytes - which
// makes the call sites that need them greppable.
type Secret string

// String renders the secret as its absence. It covers %v, %s and %q.
func (Secret) String() string { return "(redacted)" }

// GoString covers %#v, which would otherwise print the underlying string.
func (Secret) GoString() string { return `"(redacted)"` }

// Reveal returns the value itself. Every call is a place where a secret leaves
// the type that protects it, and is worth reading twice.
func (s Secret) Reveal() string { return string(s) }

// Privileged is the half of the configuration that only root may write.
//
// The split is not about who is allowed to change a setting, but about what a
// setting can do: every field here becomes a path root executes, binds,
// unlinks or trusts. The other half - refresh interval, notifications, groups,
// network contexts - lives under the user's home and can say nothing about any
// of that.
type Privileged struct {
	// WgQuick is the binary run, as root, to bring a tunnel up and down.
	WgQuick string `yaml:"wg_quick"`
	// RunDir is where wg-quick records the interface name of each live tunnel.
	RunDir string `yaml:"run_dir"`
	// Feed publishes state on a unix socket for a menu bar application to
	// read. Nothing on that socket can start or stop a tunnel.
	Feed bool `yaml:"feed"`
	// FeedSocket is where that socket is bound - and, before it is bound, the
	// path the publisher unlinks.
	FeedSocket string `yaml:"feed_socket"`
	// FeedKey is the seed of the Ed25519 key the feed signs with, so that the
	// menu bar application can tell this publisher from any other.
	FeedKey Secret `yaml:"feed_key"`
	// AllowHooks permits .conf files carrying PreUp/PostUp/PreDown/PostDown,
	// which wg-quick executes as root. Off unless somebody has read them.
	AllowHooks bool `yaml:"allow_hooks"`

	// Path is the file this was read from, for `doctor`.
	Path string `yaml:"-"`
}

// String renders the settings with the key left out. It is what makes `%v` on
// a Privileged safe, wherever somebody eventually writes one.
func (p Privileged) String() string {
	return fmt.Sprintf(
		"Privileged{Path:%s WgQuick:%s RunDir:%s Feed:%t FeedSocket:%s FeedKey:%s AllowHooks:%t}",
		p.Path, p.WgQuick, p.RunDir, p.Feed, p.FeedSocket, p.FeedKey, p.AllowHooks,
	)
}

// GoString covers %#v for the same reason Secret.GoString does.
func (p Privileged) GoString() string { return p.String() }

// DefaultPrivileged returns the built-in settings: the paths a machine set up
// the documented way already has.
func DefaultPrivileged() *Privileged {
	return &Privileged{
		WgQuick:    DefaultWgQuick,
		RunDir:     wg.DefaultRunDir,
		Feed:       true,
		FeedSocket: DefaultFeedSocket,
	}
}

// LoadPrivileged reads the privileged settings, refusing a file anybody but
// root could have written.
//
// The checks are made on the open file descriptor rather than on the path, and
// the file is opened with O_NOFOLLOW. Checking a path and then opening it is
// two lookups of a name that something else may change in between, which is
// the whole shape of the attack this file exists to stop.
//
// A missing file is a refusal, not a set of defaults. Defaults that appear
// when the file cannot be read are defaults an attacker can arrange to get.
func LoadPrivileged(path string) (*Privileged, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, openRefusal(path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		// NOT TESTED: fstat on a descriptor this process has just opened.
		// Reaching it needs the descriptor to become invalid between the open
		// and the call, which is not a window a test can open.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := checkPrivilegedFile(path, info); err != nil {
		return nil, err
	}
	if err := checkPrivilegedParent(filepath.Dir(path)); err != nil {
		return nil, err
	}

	// Decoded into the defaults rather than into a zero value, for the reason
	// spelled out in Load: yaml only sets the fields a document mentions, so an
	// absent `feed:` has to keep the documented default rather than become
	// false.
	cfg := DefaultPrivileged()
	decoder := yaml.NewDecoder(f)
	// A key this program does not know is refused rather than ignored, so that
	// a misspelling in the file that grants root cannot pass for a setting.
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		// io.EOF is an empty file: init-privileged made it and nothing has been
		// filled in.
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Path = path
	cfg.applyDefaults()
	return cfg, nil
}

// openRefusal turns the reasons an open can fail into the sentence that says
// what to do about it.
func openRefusal(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf(
			"%s is missing: run `sudo tun-manager init-privileged` to create it. "+
				"tun-manager reads what it executes as root from that file, and from nowhere else",
			path)
	case errors.Is(err, syscall.ELOOP):
		return fmt.Errorf(
			"%s is a symbolic link: tun-manager will not follow one to read what it runs as root. "+
				"Replace it with a regular file owned by root", path)
	default:
		return fmt.Errorf("open %s: %w", path, err)
	}
}

// checkPrivilegedFile refuses a file somebody other than root owns, or one
// somebody other than root can read.
func checkPrivilegedFile(path string, info os.FileInfo) error {
	if uid, _ := ownerOf(path, info); uid != rootUID {
		return fmt.Errorf(
			"%s is owned by uid %d rather than root, so it does not say what root will do: `sudo chown 0:0 %s`",
			path, uid, path)
	}
	// Only the bits that grant somebody else access. A mode stricter than
	// wanted - 0400 where 0600 was expected - is somebody being careful.
	if extra := info.Mode().Perm() &^ PrivilegedFileMode.Perm(); extra != 0 {
		return fmt.Errorf(
			"%s is %04o and holds the feed signing key, want %04o or stricter: `sudo chmod %04o %s`",
			path, info.Mode().Perm(), PrivilegedFileMode.Perm(), PrivilegedFileMode.Perm(), path)
	}
	return nil
}

// checkPrivilegedParent refuses a directory in which somebody else could
// replace the file.
//
// The mode of the file is not the whole story: anybody who can write the
// directory can rename the file away and put their own in its place, and the
// new one would pass every check above because they would own it and could
// make it 0600.
func checkPrivilegedParent(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		// NOT TESTED: the open above walked through this directory, so it was
		// there a moment ago.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if uid, _ := ownerOf(dir, info); uid != rootUID {
		return fmt.Errorf(
			"%s is owned by uid %d rather than root, so %s could be replaced: `sudo chown 0:0 %s`",
			dir, uid, PrivilegedName, dir)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf(
			"%s is %04o and writable by others, so %s could be replaced: `sudo chmod go-w %s`",
			dir, info.Mode().Perm(), PrivilegedName, dir)
	}
	return nil
}

// applyDefaults fills anything the document set to an empty value. Load starts
// from DefaultPrivileged, so this only catches keys written out as blank rather
// than left out.
func (p *Privileged) applyDefaults() {
	d := DefaultPrivileged()
	if p.WgQuick == "" {
		p.WgQuick = d.WgQuick
	}
	if p.RunDir == "" {
		p.RunDir = d.RunDir
	}
	if p.FeedSocket == "" {
		p.FeedSocket = d.FeedSocket
	}
}

// rootUID is the owner the privileged side is meant to have.
const rootUID = 0

// ownerOf reads the uid and gid behind a FileInfo.
//
// A variable for the reason the identical one in internal/cli has: a fixture is
// owned by whoever runs the suite, and making it root-owned would mean running
// the suite as root - a suite that only proves itself under sudo proves nothing
// on anybody else's machine. Swapped by ownedBy in the tests, and never
// assigned anywhere else.
var ownerOf func(path string, info os.FileInfo) (uid, gid int) = realOwner

// realOwner reads the uid and gid out of the stat behind a FileInfo. darwin
// only, like the rest of this program.
func realOwner(_ string, info os.FileInfo) (uid, gid int) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// NOT TESTED: os.Stat on darwin always yields a *syscall.Stat_t. This
		// guards a platform where it does not, on which the whole program does
		// not build.
		// See docs/coverage-gaps.md, "cli.ownerOf".
		return -1, -1
	}
	return int(stat.Uid), int(stat.Gid)
}
