package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"ledez.net/tun-manager/internal/wgconf"
)

// Keys and addresses here are invented; the addresses come from the ranges
// reserved for documentation (RFC 5737).
const (
	reviewKey    = "JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g="
	reviewPeer   = "lKGuu8jV4u/8CRYjMD1KV2RxfouYpbK/zNnm8wANGic="
	plainConfig  = "[Interface]\nPrivateKey = " + reviewKey + "\nAddress = 10.20.30.2/32\n\n[Peer]\nPublicKey = " + reviewPeer + "\nEndpoint = 192.0.2.10:51820\n# TO_CHECK=10.20.30.1\n"
	hookedConfig = "[Interface]\nPrivateKey = " + reviewKey + "\nPostUp = /usr/local/bin/announce %i\nPreDown = logger stopping\n\n[Peer]\nPublicKey = " + reviewPeer + "\n# TO_CHECK=10.20.30.1\n"
)

// reviewed renders a configuration the way import shows it, and hands back what
// somebody would read.
func reviewed(t *testing.T, body string) string {
	t.Helper()

	tun, err := wgconf.Parse([]byte(body), "/Users/someone/Downloads/work.conf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var out strings.Builder
	if err := Review(&out, "/Users/someone/Downloads/work.conf", []byte(body), tun); err != nil {
		t.Fatalf("Review: %v", err)
	}
	return out.String()
}

func TestReviewShowsTheWholeFile(t *testing.T) {
	// The whole file, not the fields this program happens to parse: what is
	// being handed to root is the file, and a summary of it is not what anybody
	// needs to read before agreeing to that.
	got := reviewed(t, plainConfig)

	for _, want := range []string{
		"[Interface]", "Address = 10.20.30.2/32",
		"[Peer]", "Endpoint = 192.0.2.10:51820", "# TO_CHECK=10.20.30.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the review does not show %q:\n%s", want, got)
		}
	}
}

func TestReviewNumbersTheLines(t *testing.T) {
	// So that the hook warning below can point at one, and so that somebody can
	// open the file and find what they just read.
	got := reviewed(t, plainConfig)

	if !strings.Contains(got, "1 │ [Interface]") {
		t.Errorf("the review does not number the lines:\n%s", got)
	}
}

func TestReviewHidesEveryPrivateKey(t *testing.T) {
	// It is printed on a terminal, scrolled back through, and pasted into
	// issues. The one thing that must never be in it is the key.
	got := reviewed(t, plainConfig)

	if strings.Contains(got, reviewKey) {
		t.Errorf("the review printed the private key:\n%s", got)
	}
	if !strings.Contains(got, "PrivateKey") {
		t.Errorf("the review hid that there is a private key at all:\n%s", got)
	}
	if !strings.Contains(got, reviewPeer) {
		t.Errorf("the review hid the public key, which is an identity rather than a secret:\n%s", got)
	}
}

func TestReviewSaysWhichAddressWillBePinged(t *testing.T) {
	// It is the one address this program will send packets to on its own, and
	// it comes from the file being imported.
	got := reviewed(t, plainConfig)

	if !strings.Contains(got, "10.20.30.1") {
		t.Errorf("the review does not name the address it will ping:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "ping") {
		t.Errorf("the review does not say what that address is for:\n%s", got)
	}
}

func TestReviewSaysNothingAboutHooksWhenThereAreNone(t *testing.T) {
	// A warning that appears on every import is a warning nobody reads.
	got := reviewed(t, plainConfig)

	if strings.Contains(strings.ToLower(got), "as root") {
		t.Errorf("the review warns about hooks in a file that has none:\n%s", got)
	}
}

func TestReviewShowsEveryHookAndSaysWhatWillRunThem(t *testing.T) {
	got := reviewed(t, hookedConfig)

	for _, want := range []string{
		"PostUp = /usr/local/bin/announce %i",
		"PreDown = logger stopping",
		"line 3", "line 4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the review does not show %q:\n%s", want, got)
		}
	}
	lowered := strings.ToLower(got)
	for _, want := range []string{"root", "wg-quick", "be sure"} {
		if !strings.Contains(lowered, want) {
			t.Errorf("the warning does not say %q:\n%s", want, got)
		}
	}
}

func TestTheHookWarningIsRedOnATerminal(t *testing.T) {
	// Red, because it is the one thing on the screen that can hand somebody
	// else root on this machine.
	forceColour(t)

	got := reviewed(t, hookedConfig)

	if !strings.Contains(got, "\x1b[") {
		t.Errorf("nothing in the warning is coloured:\n%q", got)
	}
}

func TestNothingIsColouredWhenTheOutputIsNotATerminal(t *testing.T) {
	// Piped into a file, or into `less` without -R, escape codes are noise in
	// the middle of a configuration somebody is trying to read.
	got := reviewed(t, hookedConfig)

	if strings.Contains(got, "\x1b[") {
		t.Errorf("escape codes reached a plain writer:\n%q", got)
	}
}

// forceColour makes the renderer behave as if it were writing to a terminal,
// which a test's strings.Builder is not.
func forceColour(t *testing.T) {
	t.Helper()

	previous := newRenderer
	newRenderer = func(w io.Writer, opts ...termenv.OutputOption) *lipgloss.Renderer {
		r := lipgloss.NewRenderer(w, opts...)
		r.SetColorProfile(termenv.ANSI)
		return r
	}
	t.Cleanup(func() { newRenderer = previous })
}

func TestOneHookIsCalledACommand(t *testing.T) {
	// "asks for 1 commands to be run as root" is the sort of thing that makes
	// somebody stop reading the sentence that matters most on the screen.
	got := reviewed(t, "[Interface]\nPrivateKey = "+reviewKey+"\nPostUp = touch /tmp/x\n\n[Peer]\nPublicKey = "+reviewPeer+"\n# TO_CHECK=10.20.30.1\n")

	if !strings.Contains(got, "a command to be run as root") {
		t.Errorf("a single hook is not called a command:\n%s", got)
	}
}
