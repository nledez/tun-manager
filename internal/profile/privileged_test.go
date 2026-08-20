package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ledez.net/tun-manager/internal/fsx"
)

// Every value below is invented. Addresses come from the ranges reserved for
// documentation (RFC 5737).
const samplePrivilegedYAML = `
wg_quick: /usr/bin/wg-quick
run_dir: /var/run/wireguard
feed: true
feed_socket: /var/run/tun-manager.sock
feed_key: c2VjcmV0LXNlZWQtdGhpcnR5LXR3by1ieXRlcy0=
wg_quick_root_owned: true
wg_quick_no_symlink: true
`

// ownedBy makes every path look owned by one uid for the length of a test.
//
// A fixture is owned by whoever runs the suite. Making it root-owned would mean
// running the suite as root, and a suite that only proves itself under sudo
// proves nothing on anybody else's machine.
func ownedBy(t *testing.T, uid int) {
	t.Helper()
	ownedPerPath(t, nil, uid)
}

// ownedPerPath is the same, for a test that needs one path to differ. A path
// matches when it ends with the key, so a test names "tun-manager.yaml" rather
// than the temporary directory it happens to be under.
func ownedPerPath(t *testing.T, bySuffix map[string]int, fallback int) {
	t.Helper()

	previous := fsx.Owner
	fsx.Owner = func(path string, _ os.FileInfo) (int, int) {
		for suffix, uid := range bySuffix {
			if strings.HasSuffix(path, suffix) {
				return uid, uid
			}
		}
		return fallback, fallback
	}
	t.Cleanup(func() { fsx.Owner = previous })
}

// writePrivileged lays out a privileged file the way a correct installation
// has it: 0600 in a 0700 directory. Ownership is the caller's to arrange with
// ownedBy, since a test cannot create a root-owned file.
func writePrivileged(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	path := filepath.Join(dir, "tun-manager.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write privileged config: %v", err)
	}
	return path
}

// refusal loads a file that is expected to be refused and returns the reason,
// so that every test below can assert on what the user is told.
func refusal(t *testing.T, path string) string {
	t.Helper()

	cfg, err := LoadPrivileged(path)
	if err == nil {
		t.Fatalf("LoadPrivileged(%s) = %v, want a refusal", path, cfg)
	}
	return err.Error()
}

// mustContain fails naming both halves: a message that no longer says what to
// type is the failure worth catching, and the diff has to show it.
func mustContain(t *testing.T, message, want string) {
	t.Helper()

	if !strings.Contains(message, want) {
		t.Errorf("message %q does not contain %q", message, want)
	}
}

func TestPrivilegedPathIsFixed(t *testing.T) {
	if ConfigDir != "/private/wireguard/config" {
		t.Errorf("ConfigDir = %q, want /private/wireguard/config", ConfigDir)
	}
	if want := ConfigDir + "/tun-manager.yaml"; PrivilegedPath != want {
		t.Errorf("PrivilegedPath = %q, want %q", PrivilegedPath, want)
	}
}

func TestLoadPrivilegedReadsSettings(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, samplePrivilegedYAML)

	cfg, err := LoadPrivileged(path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}

	if cfg.WgQuick != "/usr/bin/wg-quick" {
		t.Errorf("WgQuick = %q", cfg.WgQuick)
	}
	if cfg.RunDir != "/var/run/wireguard" {
		t.Errorf("RunDir = %q", cfg.RunDir)
	}
	if !cfg.Feed {
		t.Error("Feed = false, want true")
	}
	if cfg.FeedSocket != "/var/run/tun-manager.sock" {
		t.Errorf("FeedSocket = %q", cfg.FeedSocket)
	}
	if cfg.FeedKey.Reveal() != "c2VjcmV0LXNlZWQtdGhpcnR5LXR3by1ieXRlcy0=" {
		t.Errorf("FeedKey = %q", cfg.FeedKey.Reveal())
	}
	if !cfg.WgQuickRootOwned || !cfg.WgQuickNoSymlink {
		t.Errorf("the strict rules were not read: %v", cfg)
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, want %q", cfg.Path, path)
	}
}

func TestLoadPrivilegedFillsDefaults(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, "wg_quick_no_symlink: false\n")

	cfg, err := LoadPrivileged(path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}

	d := DefaultPrivileged()
	if cfg.WgQuick != d.WgQuick {
		t.Errorf("WgQuick = %q, want the default %q", cfg.WgQuick, d.WgQuick)
	}
	if cfg.RunDir != d.RunDir {
		t.Errorf("RunDir = %q, want the default %q", cfg.RunDir, d.RunDir)
	}
	if cfg.FeedSocket != d.FeedSocket {
		t.Errorf("FeedSocket = %q, want the default %q", cfg.FeedSocket, d.FeedSocket)
	}
	// The one default that cannot be spelled as a zero value: an absent `feed:`
	// used to mean false, which is the opposite of what it documents.
	if !cfg.Feed {
		t.Error("Feed = false, want the documented default true")
	}
}

func TestLoadPrivilegedFillsDefaultsOfAnEmptyFile(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, "")

	cfg, err := LoadPrivileged(path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}
	if cfg.WgQuick != DefaultPrivileged().WgQuick {
		t.Errorf("WgQuick = %q, want a default", cfg.WgQuick)
	}
}

func TestLoadPrivilegedRefusesAMissingFile(t *testing.T) {
	ownedBy(t, 0)
	path := filepath.Join(t.TempDir(), "tun-manager.yaml")

	message := refusal(t, path)
	mustContain(t, message, path)
	mustContain(t, message, "sudo tun-manager init-privileged")
}

func TestLoadPrivilegedRefusesASymbolicLink(t *testing.T) {
	ownedBy(t, 0)
	real := writePrivileged(t, samplePrivilegedYAML)
	link := filepath.Join(filepath.Dir(real), "link.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	message := refusal(t, link)
	mustContain(t, message, link)
	mustContain(t, message, "symbolic link")
}

func TestLoadPrivilegedRefusesAModeOthersCanRead(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, samplePrivilegedYAML)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	message := refusal(t, path)
	mustContain(t, message, "0644")
	mustContain(t, message, "sudo chmod 0600 "+path)
}

func TestLoadPrivilegedAcceptsAStricterMode(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, samplePrivilegedYAML)
	// Somebody who runs it at 0400 has been careful, not careless.
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, err := LoadPrivileged(path); err != nil {
		t.Fatalf("LoadPrivileged at 0400: %v", err)
	}
}

func TestLoadPrivilegedRefusesAnOwnerOtherThanRoot(t *testing.T) {
	ownedBy(t, 501)
	path := writePrivileged(t, samplePrivilegedYAML)

	message := refusal(t, path)
	mustContain(t, message, "uid 501")
	mustContain(t, message, "sudo chown 0:0 "+path)
}

func TestLoadPrivilegedRefusesAParentOwnedByAnotherUser(t *testing.T) {
	path := writePrivileged(t, samplePrivilegedYAML)
	// The file itself is root's; the directory it sits in is not, so somebody
	// else can rename it away and put their own file there.
	ownedPerPath(t, map[string]int{"tun-manager.yaml": 0}, 501)

	message := refusal(t, path)
	dir := filepath.Dir(path)
	mustContain(t, message, "uid 501")
	mustContain(t, message, "sudo chown 0:0 "+dir)
}

func TestLoadPrivilegedRefusesAParentOthersCanWrite(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, samplePrivilegedYAML)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	message := refusal(t, path)
	mustContain(t, message, "0777")
	mustContain(t, message, "sudo chmod go-w "+dir)
}

func TestLoadPrivilegedRefusesAnUnknownKey(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, "wg_quik: /usr/bin/wg-quick\n")

	message := refusal(t, path)
	mustContain(t, message, "wg_quik")
}

func TestPrivilegedRedactsTheFeedKey(t *testing.T) {
	ownedBy(t, 0)
	path := writePrivileged(t, samplePrivilegedYAML)

	cfg, err := LoadPrivileged(path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}

	const secret = "c2VjcmV0LXNlZWQtdGhpcnR5LXR3by1ieXRlcy0="
	for _, rendered := range []string{
		fmt.Sprintf("%v", cfg),
		cfg.String(),
		fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%v", cfg.FeedKey),
		cfg.FeedKey.String(),
		fmt.Sprintf("%q", cfg.FeedKey),
		fmt.Sprintf("%#v", cfg.FeedKey),
	} {
		if strings.Contains(rendered, secret) {
			t.Errorf("a rendering leaked the feed key: %s", rendered)
		}
	}
}

func TestLoadPrivilegedFillsKeysWrittenOutAsBlank(t *testing.T) {
	ownedBy(t, 0)
	// Not the same thing as leaving them out: somebody edited the file, emptied
	// a value and left the key behind. A blank wg_quick is not a request to run
	// nothing.
	path := writePrivileged(t, "wg_quick: \"\"\nrun_dir: \"\"\nfeed_socket: \"\"\n")

	cfg, err := LoadPrivileged(path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}

	d := DefaultPrivileged()
	if cfg.WgQuick != d.WgQuick {
		t.Errorf("WgQuick = %q, want the default %q", cfg.WgQuick, d.WgQuick)
	}
	if cfg.RunDir != d.RunDir {
		t.Errorf("RunDir = %q, want the default %q", cfg.RunDir, d.RunDir)
	}
	if cfg.FeedSocket != d.FeedSocket {
		t.Errorf("FeedSocket = %q, want the default %q", cfg.FeedSocket, d.FeedSocket)
	}
}

func TestLoadPrivilegedReportsAnOpenFailureItCannotExplain(t *testing.T) {
	ownedBy(t, 0)
	// A path whose parent is a regular file. It stands for every open failure
	// that is neither a missing file nor a symbolic link: the reason has to
	// reach the user rather than be flattened into one of the two sentences
	// written for those.
	notADir := writePrivileged(t, samplePrivilegedYAML)
	path := filepath.Join(notADir, "tun-manager.yaml")

	message := refusal(t, path)
	mustContain(t, message, path)
	mustContain(t, message, "open")
}

// privilegedExamplePath is the file shipped for /private/wireguard/config,
// read as part of the repository rather than as something the machine happens
// to have.
const privilegedExamplePath = "../../configs/tun-manager.example.yaml"

func TestTheShippedPrivilegedExampleLoads(t *testing.T) {
	ownedBy(t, 0)
	body, err := os.ReadFile(privilegedExamplePath)
	if err != nil {
		t.Fatalf("read the example: %v", err)
	}
	// Copied into a directory the test owns: the file in the repository is
	// owned by whoever cloned it and is not 0600, which is exactly what
	// LoadPrivileged refuses. What is being checked here is its content.
	path := writePrivileged(t, string(body))

	cfg, err := LoadPrivileged(path)
	if err != nil {
		t.Fatalf("the example does not load: %v", err)
	}

	if cfg.WgQuick == "" || cfg.RunDir == "" || cfg.FeedSocket == "" {
		t.Errorf("the example leaves a path empty: %v", cfg)
	}
	if !cfg.Feed {
		t.Error("the example turns the feed off, which is not the documented default")
	}
	// The one thing the shipped file must never do: carry a key somebody could
	// end up sharing with everyone who reads the repository.
	if cfg.FeedKey.Reveal() != "" {
		t.Error("the example ships a feed key")
	}
}

func TestTheFeedIsOnByDefault(t *testing.T) {
	// The socket is 0600 and owned by one person, so there is nothing to
	// protect by making it opt-in, and an application that needs configuration
	// before it works once is an application nobody runs twice.
	cfg := DefaultPrivileged()

	if !cfg.Feed {
		t.Error("Feed = false, want the feed available without configuration")
	}
	if cfg.FeedSocket != DefaultFeedSocket {
		t.Errorf("FeedSocket = %q, want %q", cfg.FeedSocket, DefaultFeedSocket)
	}
}

func TestTheFeedCanBeSwitchedOff(t *testing.T) {
	// A default that cannot be overridden is not a default.
	ownedBy(t, 0)
	path := writePrivileged(t, "feed: false\nfeed_socket: /tmp/other.sock\n")

	cfg, err := LoadPrivileged(path)
	if err != nil {
		t.Fatalf("LoadPrivileged: %v", err)
	}

	if cfg.Feed {
		t.Error("Feed = true, want it off")
	}
	if cfg.FeedSocket != "/tmp/other.sock" {
		t.Errorf("FeedSocket = %q, want the configured path", cfg.FeedSocket)
	}
}

// MARK: the failures a working filesystem does not produce

func TestLoadPrivilegedReportsADescriptorItCannotStat(t *testing.T) {
	// The check is made on the open descriptor rather than on the path, which
	// is what stops a name changing between the look and the read. When that
	// fstat fails there is nothing left to judge the file by.
	ownedBy(t, 0)
	path := writePrivileged(t, samplePrivilegedYAML)
	boom := errors.New("bad file descriptor")
	previous := fsx.StatFile
	fsx.StatFile = func(*os.File) (os.FileInfo, error) { return nil, boom }
	t.Cleanup(func() { fsx.StatFile = previous })

	_, err := LoadPrivileged(path)

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the fstat failure", err)
	}
}

func TestLoadPrivilegedReportsAParentItCannotStat(t *testing.T) {
	// The open walked through that directory a moment earlier. Something
	// removed it in between, and a file whose parent cannot be judged is a file
	// that cannot be trusted.
	ownedBy(t, 0)
	path := writePrivileged(t, samplePrivilegedYAML)
	boom := errors.New("no such file or directory")
	previous := fsx.Stat
	fsx.Stat = func(string) (os.FileInfo, error) { return nil, boom }
	t.Cleanup(func() { fsx.Stat = previous })

	_, err := LoadPrivileged(path)

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the stat failure", err)
	}
}
