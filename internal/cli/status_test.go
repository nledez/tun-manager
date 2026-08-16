package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

var taken = time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)

func sampleView() app.View {
	return app.View{
		Context: netctx.Context{Name: "office", Interface: "en0", Address: "198.51.100.42"},
		Taken:   taken,
		Rows: []app.Row{
			{
				Tunnel: wgconf.Tunnel{Name: "alpha", Endpoint: "192.0.2.10:51820", CheckIP: "10.20.30.1"},
				Group:  profile.GroupNeeded,
				Health: wg.Up,
				Peer: wg.Peer{
					Device:        "utun7",
					Endpoint:      "192.0.2.10:51820",
					LastHandshake: taken.Add(-42 * time.Second),
					RxBytes:       1258291,
					TxBytes:       4096,
				},
			},
			{
				Tunnel: wgconf.Tunnel{Name: "charlie", Endpoint: "gateway.example:51824"},
				Group:  profile.GroupExtra,
				Health: wg.Down,
			},
		},
	}
}

func TestStatusTableListsEveryTunnel(t *testing.T) {
	var out strings.Builder

	if err := WriteStatus(&out, sampleView(), false); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	got := out.String()
	for _, want := range []string{"alpha", "charlie", "up", "down", "utun7"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestStatusTableShowsTheNetworkContext(t *testing.T) {
	var out strings.Builder

	if err := WriteStatus(&out, sampleView(), false); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	if !strings.Contains(out.String(), "office (en0 198.51.100.42)") {
		t.Errorf("output missing the context header:\n%s", out.String())
	}
}

func TestStatusJSONIsMachineReadable(t *testing.T) {
	var out strings.Builder

	if err := WriteStatus(&out, sampleView(), true); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	var got struct {
		Context struct {
			Name string `json:"name"`
		} `json:"context"`
		Tunnels []struct {
			Name     string `json:"name"`
			Group    string `json:"group"`
			Health   string `json:"health"`
			Device   string `json:"device"`
			RxBytes  int64  `json:"rx_bytes"`
			Endpoint string `json:"endpoint"`
		} `json:"tunnels"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}

	if got.Context.Name != "office" {
		t.Errorf("context.name = %q, want %q", got.Context.Name, "office")
	}
	if len(got.Tunnels) != 2 {
		t.Fatalf("len(tunnels) = %d, want 2", len(got.Tunnels))
	}
	if got.Tunnels[0].Name != "alpha" || got.Tunnels[0].Health != "up" {
		t.Errorf("tunnels[0] = %+v, want alpha/up", got.Tunnels[0])
	}
	if got.Tunnels[0].Device != "utun7" || got.Tunnels[0].RxBytes != 1258291 {
		t.Errorf("tunnels[0] live fields = %+v", got.Tunnels[0])
	}
}

func TestStatusJSONUsesTheConfiguredEndpointWhenDown(t *testing.T) {
	var out strings.Builder

	if err := WriteStatus(&out, sampleView(), true); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	if !strings.Contains(out.String(), "gateway.example:51824") {
		t.Errorf("output missing the configured endpoint of a down tunnel:\n%s", out.String())
	}
}

// failingWriter refuses every write, like a closed pipe.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestStatusTableReportsAWriteFailure(t *testing.T) {
	// `tun-manager status | head` closes the pipe early; the exit code must not
	// claim success.
	boom := errors.New("broken pipe")

	err := WriteStatus(failingWriter{err: boom}, sampleView(), false)

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestStatusJSONReportsAWriteFailure(t *testing.T) {
	boom := errors.New("broken pipe")

	if err := WriteStatus(failingWriter{err: boom}, sampleView(), true); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}
