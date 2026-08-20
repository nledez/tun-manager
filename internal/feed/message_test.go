package feed

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
	"ledez.net/tun-manager/internal/wire"
)

var taken = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

func TestHelloAnnouncesTheSchemaAndTheVersion(t *testing.T) {
	// A client that does not know the schema has to be able to say so before
	// it misreads a field that changed meaning.
	out, err := json.Marshal(helloMsg{Type: "hello", Schema: Schema, Version: "v0.2.0"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if got := string(out); got != `{"type":"hello","schema":2,"version":"v0.2.0"}` {
		t.Errorf("hello = %s", got)
	}
}

func TestStateCarriesTheViewFlatBesideItsType(t *testing.T) {
	// The view's fields sit at the top level rather than under a "view" key:
	// one object per line, and the type is just another field of it.
	out, err := json.Marshal(stateMsg{Type: "state", View: wire.Of(app.View{
		Taken: taken,
		Rows: []app.Row{{
			Tunnel: wgconf.Tunnel{Name: "alpha"},
			Health: wg.Up,
			Peer:   wg.Peer{Device: "utun7", RxBytes: 2048},
		}},
	})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := string(out)
	for _, want := range []string{`"type":"state"`, `"tunnels":[`, `"name":"alpha"`, `"rx_bytes":2048`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing from %s", want, got)
		}
	}
	if strings.Contains(got, `"View"`) {
		t.Errorf("the view is nested rather than flat: %s", got)
	}
}

func TestASampleCarriesCumulativeCountersNotARate(t *testing.T) {
	// Rates are the consumer's job: it has to compute them anyway to survive a
	// sample that never arrived.
	out, err := json.Marshal(sampleMsg{
		Type: "sample", Tunnel: "alpha", At: taken, Rx: 1259000, Tx: 4200,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"type":"sample","tunnel":"alpha","at":"2026-08-17T10:00:00Z","rx":1259000,"tx":4200}`
	if got := string(out); got != want {
		t.Errorf("sample  = %s\nwant    = %s", got, want)
	}
}

func TestByeIsJustItsType(t *testing.T) {
	out, err := json.Marshal(byeMsg{Type: "bye"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if got := string(out); got != `{"type":"bye"}` {
		t.Errorf("bye = %s", got)
	}
}

func TestAClientMessageCarriesAtMostATunnel(t *testing.T) {
	var m clientMsg
	if err := json.Unmarshal([]byte(`{"type":"watch","tunnel":"alpha"}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Type != "watch" || m.Tunnel != "alpha" {
		t.Errorf("message = %+v, want a watch on alpha", m)
	}

	var refresh clientMsg
	if err := json.Unmarshal([]byte(`{"type":"refresh"}`), &refresh); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if refresh.Type != "refresh" || refresh.Tunnel != "" {
		t.Errorf("message = %+v, want a bare refresh", refresh)
	}
}
