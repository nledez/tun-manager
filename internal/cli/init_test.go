package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// initTarget is where a test asks for the layout to be built: the same shape as
// /private/wireguard/config/tun-manager.yaml, under a directory the test owns.
func initTarget(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "wireguard", "config", "tun-manager.yaml")
}

// initialised runs the command and hands back what it wrote, failing the test
// if it refused.
func initialised(t *testing.T, path string, force bool) (report, body string) {
	t.Helper()

	var out strings.Builder
	if err := InitPrivileged(&out, path, force); err != nil {
		t.Fatalf("InitPrivileged: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read what was written: %v", err)
	}
	return out.String(), string(written)
}

// seedOf digs the generated key out of what was written.
func seedOf(t *testing.T, body string) string {
	t.Helper()

	var written struct {
		FeedKey string `yaml:"feed_key"`
	}
	if err := yaml.Unmarshal([]byte(body), &written); err != nil {
		t.Fatalf("what was written is not yaml: %v", err)
	}
	return written.FeedKey
}

func TestInitPrivilegedBuildsTheLayoutNobodyElseCanRead(t *testing.T) {
	path := initTarget(t)

	_, body := initialised(t, path, false)

	// The file holds the feed's signing key, and sits beside the .conf files
	// that hold the tunnels' private keys.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != TunnelFileMode {
		t.Errorf("file is %04o, want %04o", got, TunnelFileMode)
	}
	for _, dir := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if got := info.Mode().Perm(); got != WireGuardDirMode {
			t.Errorf("%s is %04o, want %04o", dir, got, WireGuardDirMode)
		}
	}
	if !strings.Contains(body, "wg_quick:") {
		t.Errorf("what was written has no settings in it:\n%s", body)
	}
}

func TestInitPrivilegedGeneratesAFeedKey(t *testing.T) {
	path := initTarget(t)

	report, body := initialised(t, path, false)

	seed := seedOf(t, body)
	raw, err := base64.StdEncoding.DecodeString(seed)
	if err != nil {
		t.Fatalf("the written feed_key is not base64: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("the written feed_key is %d bytes, want 32", len(raw))
	}
	// The fingerprint is what the person compares against the application. It
	// is the reason to print anything at all.
	if !strings.Contains(report, ":") || !strings.Contains(strings.ToLower(report), "feed key") {
		t.Errorf("the report shows no fingerprint:\n%s", report)
	}
	if strings.Contains(report, seed) {
		t.Errorf("the report prints the private seed:\n%s", report)
	}
}

func TestInitPrivilegedGeneratesADifferentKeyEveryTime(t *testing.T) {
	first := initTarget(t)
	second := initTarget(t)

	_, oneBody := initialised(t, first, false)
	_, otherBody := initialised(t, second, false)

	if seedOf(t, oneBody) == seedOf(t, otherBody) {
		t.Error("two installations were given the same feed key")
	}
}

func TestInitPrivilegedRefusesToOverwriteWhatIsThere(t *testing.T) {
	path := initTarget(t)
	initialised(t, path, false)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	err = InitPrivileged(&strings.Builder{}, path, false)

	if err == nil {
		t.Fatal("InitPrivileged overwrote an existing configuration")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error %q does not say how to replace it", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(before) {
		t.Error("the refusal still changed the file")
	}
}

func TestInitPrivilegedForcedKeepsTheOldOneBeside(t *testing.T) {
	// Replacing the file replaces the feed key, which is what the menu bar
	// pinned. Somebody who did that by accident needs the old one back.
	path := initTarget(t)
	_, before := initialised(t, path, false)

	report, after := initialised(t, path, true)

	saved := path + ".before-init"
	kept, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("the previous configuration was not kept: %v", err)
	}
	if string(kept) != before {
		t.Error("what was kept is not what was there")
	}
	if seedOf(t, after) == seedOf(t, before) {
		t.Error("the forced run reused the old key, so --force did nothing")
	}
	if !strings.Contains(report, saved) {
		t.Errorf("the report does not say where the old one went:\n%s", report)
	}
	info, err := os.Stat(saved)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != TunnelFileMode {
		t.Errorf("the kept copy is %04o, want %04o: it still holds a key", got, TunnelFileMode)
	}
}

func TestInitPrivilegedTightensADirectoryAnybodyCanRead(t *testing.T) {
	// An installation that predates this command, or a mkdir done by hand with
	// the wrong umask. Leaving it as found would mean the tunnel list is
	// readable by anybody, which is what the mode is there to stop.
	path := initTarget(t)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chmod(t, filepath.Dir(dir), 0o755)
	chmod(t, dir, 0o755)

	initialised(t, path, false)

	for _, d := range []string{dir, filepath.Dir(dir)} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		if got := info.Mode().Perm(); got != WireGuardDirMode {
			t.Errorf("%s is still %04o, want %04o", d, got, WireGuardDirMode)
		}
	}
}

func TestInitPrivilegedRefusesASymbolicLinkInThePath(t *testing.T) {
	// Somebody who can write the parent can point the config directory
	// somewhere else, and root would then write a file holding a key there.
	root := t.TempDir()
	elsewhere := t.TempDir()
	wireguard := filepath.Join(root, "wireguard")
	if err := os.Symlink(elsewhere, wireguard); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	path := filepath.Join(wireguard, "config", "tun-manager.yaml")

	err := InitPrivileged(&strings.Builder{}, path, false)

	if err == nil {
		t.Fatal("InitPrivileged followed a symbolic link")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("error %q does not say what is wrong", err)
	}
}

func TestTheWrittenConfigurationIsTheShippedExample(t *testing.T) {
	// Two copies of the same document drift, and the one people read would stop
	// being the one the program writes. The template is the original; the
	// example in configs/ is a copy kept for reading on the forge.
	shipped, err := os.ReadFile("../../configs/tun-manager.example.yaml")
	if err != nil {
		t.Fatalf("read the shipped example: %v", err)
	}

	if string(shipped) != privilegedTemplate {
		t.Error("configs/tun-manager.example.yaml and internal/cli/privileged.yaml have drifted: " +
			"copy internal/cli/privileged.yaml over it")
	}
}

func TestTheTemplateHasExactlyOnePlaceToPutTheKey(t *testing.T) {
	// The substitution below is a text one. If the template ever grows a second
	// empty feed_key, or loses the one it has, the written file would carry no
	// key and nothing else would notice.
	if got := strings.Count(privilegedTemplate, emptyFeedKey); got != 1 {
		t.Errorf("the template has %d %q lines, want exactly 1", got, emptyFeedKey)
	}
}

func TestInitPrivilegedRefusesAFileWhereADirectoryBelongs(t *testing.T) {
	// /private/wireguard exists and is not a directory. Rare, and the message
	// has to say so rather than fail on the mkdir with something obscure.
	root := t.TempDir()
	wireguard := filepath.Join(root, "wireguard")
	if err := os.WriteFile(wireguard, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := InitPrivileged(&strings.Builder{}, filepath.Join(wireguard, "config", "tun-manager.yaml"), false)

	if err == nil {
		t.Fatal("InitPrivileged accepted a file where a directory belongs")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error %q does not say what is wrong", err)
	}
}

func TestInitPrivilegedReportsADirectoryItCannotCreate(t *testing.T) {
	// A read-only /private, a filesystem mounted read-only, a full disk. The
	// path that failed has to be in the message: "permission denied" on its own
	// says nothing about which of the two directories it was.
	root := t.TempDir()
	chmod(t, root, 0o500)
	path := filepath.Join(root, "wireguard", "config", "tun-manager.yaml")

	err := InitPrivileged(&strings.Builder{}, path, false)

	if err == nil {
		t.Fatal("InitPrivileged created a directory under a read-only parent")
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "wireguard")) {
		t.Errorf("error %q does not name the directory it could not create", err)
	}
}
