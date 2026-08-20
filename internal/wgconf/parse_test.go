package wgconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keys, addresses and endpoints in testdata are invented. Addresses come from
// the ranges reserved for documentation (RFC 5737 and RFC 3849) so that no test
// can ever reach a real host.
const (
	alphaKey = "JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g="
	deltaKey = "lKGuu8jV4u/8CRYjMD1KV2RxfouYpbK/zNnm8wANGic="
)

func TestParseFileReadsInterfaceAndPeer(t *testing.T) {
	tun, err := ParseFile(filepath.Join("testdata", "alpha.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.Name != "alpha" {
		t.Errorf("Name = %q, want %q", tun.Name, "alpha")
	}
	if tun.PeerPublicKey != alphaKey {
		t.Errorf("PeerPublicKey = %q", tun.PeerPublicKey)
	}
	if tun.Endpoint != "192.0.2.10:51820" {
		t.Errorf("Endpoint = %q", tun.Endpoint)
	}
	if tun.Address != "10.20.30.2/32" {
		t.Errorf("Address = %q", tun.Address)
	}
	if got, want := len(tun.AllowedIPs), 1; got != want {
		t.Fatalf("len(AllowedIPs) = %d, want %d", got, want)
	}
	if tun.AllowedIPs[0] != "10.20.30.0/24" {
		t.Errorf("AllowedIPs[0] = %q", tun.AllowedIPs[0])
	}
}

func TestParseFileNeverExposesSecrets(t *testing.T) {
	tun, err := ParseFile(filepath.Join("testdata", "alpha.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	// alpha.conf carries both PrivateKey and PresharedKey; neither must survive
	// parsing.
	for _, field := range []string{tun.Name, tun.Address, tun.Endpoint, tun.PeerPublicKey, tun.CheckIP} {
		if field == "REDACTED" {
			t.Errorf("a secret placeholder leaked into a parsed field: %q", field)
		}
	}
}

func TestParseFileSplitsMultipleAllowedIPs(t *testing.T) {
	tun, err := ParseFile(filepath.Join("testdata", "delta.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	want := []string{"10.20.33.0/27", "198.51.100.0/24", "203.0.113.7/32", "192.0.2.99/32"}
	if len(tun.AllowedIPs) != len(want) {
		t.Fatalf("AllowedIPs = %v, want %v", tun.AllowedIPs, want)
	}
	for i := range want {
		if tun.AllowedIPs[i] != want[i] {
			t.Errorf("AllowedIPs[%d] = %q, want %q", i, tun.AllowedIPs[i], want[i])
		}
	}
}

func TestCheckIPFromComment(t *testing.T) {
	tun, err := ParseFile(filepath.Join("testdata", "alpha.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.CheckIP != "10.20.30.1" {
		t.Errorf("CheckIP = %q, want %q", tun.CheckIP, "10.20.30.1")
	}
	if tun.CheckIPInferred {
		t.Error("CheckIPInferred = true, want false for an explicit TO_CHECK comment")
	}
}

func TestCheckIPFallsBackToSingleHostAllowedIP(t *testing.T) {
	// bravo.conf has no TO_CHECK comment but a /32 AllowedIPs entry.
	tun, err := ParseFile(filepath.Join("testdata", "bravo.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.CheckIP != "10.20.31.1" {
		t.Errorf("CheckIP = %q, want %q", tun.CheckIP, "10.20.31.1")
	}
	if !tun.CheckIPInferred {
		t.Error("CheckIPInferred = false, want true for an inferred address")
	}
}

func TestCheckIPFallsBackToEndpointHost(t *testing.T) {
	// charlie.conf has no TO_CHECK and only a /24 AllowedIPs entry.
	tun, err := ParseFile(filepath.Join("testdata", "charlie.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.CheckIP != "203.0.113.30" {
		t.Errorf("CheckIP = %q, want %q", tun.CheckIP, "203.0.113.30")
	}
	if !tun.CheckIPInferred {
		t.Error("CheckIPInferred = false, want true for an inferred address")
	}
}

func TestParseFileReadsABracketedIPv6Endpoint(t *testing.T) {
	tun, err := ParseFile(filepath.Join("testdata", "delta6.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.Endpoint != "[2001:db8::40]:51823" {
		t.Errorf("Endpoint = %q", tun.Endpoint)
	}
}

func TestTwoConfigsMayShareAPeerPublicKey(t *testing.T) {
	// delta and delta6 reach the same server over IPv4 and IPv6. Nothing in the
	// parser may assume the key identifies a single tunnel.
	v4, err := ParseFile(filepath.Join("testdata", "delta.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	v6, err := ParseFile(filepath.Join("testdata", "delta6.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if v4.PeerPublicKey != deltaKey || v6.PeerPublicKey != deltaKey {
		t.Fatalf("keys = %q / %q, want both %q", v4.PeerPublicKey, v6.PeerPublicKey, deltaKey)
	}
	if v4.Name == v6.Name {
		t.Error("both configs parsed to the same name")
	}
}

func TestLoadDirReturnsTunnelsSortedByName(t *testing.T) {
	tuns, err := LoadDir("testdata")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	want := []string{"alpha", "bravo", "charlie", "delta", "delta6", "echo"}
	if len(tuns) != len(want) {
		t.Fatalf("got %d tunnels, want %d", len(tuns), len(want))
	}
	for i := range want {
		if tuns[i].Name != want[i] {
			t.Errorf("tuns[%d].Name = %q, want %q", i, tuns[i].Name, want[i])
		}
	}
}

func TestLoadDirSetsPathForWgQuick(t *testing.T) {
	tuns, err := LoadDir("testdata")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	want := filepath.Join("testdata", "alpha.conf")
	if tuns[0].Path != want {
		t.Errorf("Path = %q, want %q", tuns[0].Path, want)
	}
}

func TestParseFileRejectsConfigWithoutPeer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.conf")
	writeFile(t, path, "[Interface]\nAddress = 10.20.35.1/32\n")

	if _, err := ParseFile(path); err == nil {
		t.Fatal("ParseFile succeeded on a config with no [Peer] section, want error")
	}
}

func TestLoadDirRejectsADirectoryWithNoConfig(t *testing.T) {
	if _, err := LoadDir(t.TempDir()); err == nil {
		t.Fatal("LoadDir succeeded on an empty directory, want an error")
	}
}

func TestLoadDirFailsOnAnUnparsableConfig(t *testing.T) {
	// One broken file must not be reported as a partial success.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.conf"), "[Interface]\n")

	if _, err := LoadDir(dir); err == nil {
		t.Fatal("LoadDir succeeded with a broken config, want an error")
	}
}

func TestParseFileFailsOnAMissingFile(t *testing.T) {
	if _, err := ParseFile(filepath.Join(t.TempDir(), "absent.conf")); err == nil {
		t.Fatal("ParseFile succeeded on a missing file, want an error")
	}
}

func TestCommentsWithoutTheMarkerAreIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noisy.conf")
	writeFile(t, path, `# just a comment
; TO_CHECK=
# TO_CHECK = 10.20.30.9 ;
[Peer]
PublicKey = `+alphaKey+`
AllowedIPs = 10.20.30.0/24
`)

	tun, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.CheckIP != "10.20.30.9" {
		t.Errorf("CheckIP = %q, want the marked comment to win", tun.CheckIP)
	}
}

func TestLinesWithoutAnEqualsSignAreIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "odd.conf")
	writeFile(t, path, "[Peer]\ngarbage\nPublicKey = "+alphaKey+"\n")

	if _, err := ParseFile(path); err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
}

func TestOnlyTheFirstPeerKeyIsKept(t *testing.T) {
	// A config with two peers is not something wg-quick would produce here, but
	// silently switching identity halfway through would be worse than ignoring.
	dir := t.TempDir()
	path := filepath.Join(dir, "two.conf")
	writeFile(t, path, "[Peer]\nPublicKey = "+alphaKey+"\n[Peer]\nPublicKey = "+deltaKey+"\n")

	tun, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.PeerPublicKey != alphaKey {
		t.Errorf("PeerPublicKey = %q, want the first one", tun.PeerPublicKey)
	}
}

func TestCheckIPIsEmptyWithoutAnythingToInferFrom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bare.conf")
	writeFile(t, path, "[Peer]\nPublicKey = "+alphaKey+"\n")

	tun, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.CheckIP != "" {
		t.Errorf("CheckIP = %q, want empty: no AllowedIPs and no endpoint", tun.CheckIP)
	}
	if tun.CheckIPInferred {
		t.Error("CheckIPInferred = true, want false when nothing was inferred")
	}
}

func TestCheckIPFallsBackToAnEndpointWithoutAPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hostonly.conf")
	writeFile(t, path, "[Peer]\nPublicKey = "+alphaKey+"\nEndpoint = gateway.example\nAllowedIPs = 10.20.30.0/24\n")

	tun, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.CheckIP != "gateway.example" {
		t.Errorf("CheckIP = %q, want the endpoint used as-is", tun.CheckIP)
	}
}

func TestMalformedAllowedIPsAreSkippedWhenInferring(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.conf")
	writeFile(t, path, "[Peer]\nPublicKey = "+alphaKey+"\nEndpoint = 192.0.2.10:51820\nAllowedIPs = nonsense, 10.20.30.7/32\n")

	tun, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if tun.CheckIP != "10.20.30.7" {
		t.Errorf("CheckIP = %q, want the first parsable single host", tun.CheckIP)
	}
}

func TestLoadDirRejectsADirectoryNameThatBreaksThePattern(t *testing.T) {
	// config_dir comes from the user's YAML, so a bracket in it reaches
	// filepath.Glob as a malformed pattern. This is user input, not dead code.
	if _, err := LoadDir(filepath.Join(t.TempDir(), "wireguard[")); err == nil {
		t.Fatal("LoadDir accepted a directory name that breaks the glob, want an error")
	}
}

func TestParseFileReportsAReadFailure(t *testing.T) {
	// A directory opens like a file and fails on the first read, which is the
	// only way to reach the scanner's error without a fake file system.
	path := filepath.Join(t.TempDir(), "alpha.conf")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := ParseFile(path)

	if err == nil {
		t.Fatal("ParseFile succeeded on an unreadable file, want an error")
	}
	if !strings.Contains(err.Error(), "alpha.conf") {
		t.Errorf("err = %v, want it to name the file", err)
	}
}

// MARK: what wg-quick would run as root

// parseString parses a configuration written out here rather than kept in
// testdata: these tests are about lines that must never reach a real file.
func parseString(t *testing.T, body string) Tunnel {
	t.Helper()

	path := filepath.Join(t.TempDir(), "alpha.conf")
	writeFile(t, path, body)
	tun, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return tun
}

func TestParseCollectsTheHooksWgQuickWouldRun(t *testing.T) {
	// They are not obeyed here and never will be. What matters is that they can
	// be shown to somebody before the file is imported: wg-quick runs each of
	// them as root, every time the tunnel goes up or down.
	tun := parseString(t, `[Interface]
PrivateKey = `+alphaKey+`
Address = 10.20.30.2/32
PostUp = /usr/local/bin/announce %i
PreDown = logger stopping

[Peer]
PublicKey = `+deltaKey+`
Endpoint = 192.0.2.10:51820
# TO_CHECK=10.20.30.1
`)

	if len(tun.Hooks) != 2 {
		t.Fatalf("hooks = %+v, want two", tun.Hooks)
	}
	if tun.Hooks[0].Key != "PostUp" || tun.Hooks[0].Value != "/usr/local/bin/announce %i" {
		t.Errorf("first hook = %+v", tun.Hooks[0])
	}
	if tun.Hooks[0].Line != 4 {
		t.Errorf("first hook is on line %d, want 4: the line is how somebody finds it", tun.Hooks[0].Line)
	}
	if tun.Hooks[1].Key != "PreDown" || tun.Hooks[1].Line != 5 {
		t.Errorf("second hook = %+v", tun.Hooks[1])
	}
}

func TestParseKeepsTheKeyAsItWasWritten(t *testing.T) {
	// wg-quick's parser is case-insensitive. Showing "postup" back to somebody
	// who wrote "PostUp" makes them look for a line that is not there.
	tun := parseString(t, `[Interface]
PrivateKey = `+alphaKey+`
postup = touch /tmp/x

[Peer]
PublicKey = `+deltaKey+`
# TO_CHECK=10.20.30.1
`)

	if len(tun.Hooks) != 1 || tun.Hooks[0].Key != "postup" {
		t.Errorf("hooks = %+v, want the key as written", tun.Hooks)
	}
}

func TestParseFindsNoHookInAFileWithout(t *testing.T) {
	tun := parseString(t, `[Interface]
PrivateKey = `+alphaKey+`

[Peer]
PublicKey = `+deltaKey+`
# TO_CHECK=10.20.30.1
`)

	if len(tun.Hooks) != 0 {
		t.Errorf("hooks = %+v, want none", tun.Hooks)
	}
}

func TestRedactHidesEveryKeyAndNothingElse(t *testing.T) {
	// The file is shown back before it is imported, so that somebody can read
	// what they are about to hand to root. What they must not have to think
	// about is whether it is safe to have it on screen.
	body := []byte(`[Interface]
PrivateKey = ` + alphaKey + `
Address = 10.20.30.2/32

[Peer]
PublicKey = ` + deltaKey + `
PresharedKey = ` + alphaKey + `
Endpoint = 192.0.2.10:51820
`)

	got := string(Redact(body))

	if strings.Contains(got, alphaKey) {
		t.Errorf("a secret survived redaction:\n%s", got)
	}
	// The public key is an identity rather than a secret, and it is what
	// matches a configuration to a live interface: hiding it would hide the one
	// field somebody may need to compare.
	if !strings.Contains(got, deltaKey) {
		t.Errorf("the public key was hidden too:\n%s", got)
	}
	for _, want := range []string{"PrivateKey", "PresharedKey", "Address = 10.20.30.2/32"} {
		if !strings.Contains(got, want) {
			t.Errorf("redaction lost %q:\n%s", want, got)
		}
	}
}

func TestRedactIgnoresCaseAndSpacing(t *testing.T) {
	body := []byte("privatekey=" + alphaKey + "\n  PreSharedKey   =   " + alphaKey + "\n")

	got := string(Redact(body))

	if strings.Contains(got, alphaKey) {
		t.Errorf("a secret survived redaction:\n%s", got)
	}
}

func TestRedactKeepsTheFileReadable(t *testing.T) {
	// Same number of lines, in the same order: it is shown with line numbers
	// beside it, and those have to match the file on disk.
	body := []byte("[Interface]\nPrivateKey = " + alphaKey + "\n\n[Peer]\n")

	got := string(Redact(body))

	if strings.Count(got, "\n") != strings.Count(string(body), "\n") {
		t.Errorf("the line count changed:\n%s", got)
	}
}

func TestParseAndParseFileAgree(t *testing.T) {
	// The whole point of Parse existing: the caller shows what it read and
	// imports what it showed. If the two disagreed, that guarantee would be
	// worth nothing.
	body, err := os.ReadFile(filepath.Join("testdata", "alpha.conf"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	fromFile, err := ParseFile(filepath.Join("testdata", "alpha.conf"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	fromBytes, err := Parse(body, filepath.Join("testdata", "alpha.conf"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if fmt.Sprintf("%+v", fromFile) != fmt.Sprintf("%+v", fromBytes) {
		t.Errorf("Parse = %+v\nParseFile = %+v", fromBytes, fromFile)
	}
}

func TestParseReportsALineItCannotRead(t *testing.T) {
	// bufio.Scanner refuses a line longer than its buffer, and a .conf whose
	// last line never arrived is not one to half-parse: the peer key could be
	// on the part that was dropped.
	body := []byte("[Interface]\nAddress = " + strings.Repeat("1", 70*1024) + "\n")

	_, err := Parse(body, "/private/wireguard/config/alpha.conf")

	if err == nil {
		t.Fatal("Parse accepted a file it could not read to the end")
	}
	if !strings.Contains(err.Error(), "alpha.conf") {
		t.Errorf("error %q does not name the file", err)
	}
}
