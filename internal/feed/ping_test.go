package feed

import (
	"testing"
	"time"

	"ledez.net/tun-manager/internal/wire"
)

func TestAPingIsHandedToWhoeverOwnsTheProbe(t *testing.T) {
	// The feed has no pinger of its own, on purpose: it publishes, it does not
	// act. The request goes out to the program that already owns the network.
	s := serving(t, nil)
	reqs := s.Requests()
	c := dial(t, s)
	c.next(t) // hello

	c.send(t, `{"type":"ping"}`)

	select {
	case req := <-reqs:
		if req.Kind != RequestPing {
			t.Errorf("kind = %q, want %q", req.Kind, RequestPing)
		}
		if req.Tunnel != "" {
			t.Errorf("tunnel = %q, want every tunnel", req.Tunnel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ping request")
	}
}

func TestAPingCanNameOneTunnel(t *testing.T) {
	s := serving(t, nil)
	s.Publish(aView("alpha", "bravo"))
	reqs := s.Requests()
	c := dial(t, s)
	c.next(t) // hello
	c.next(t) // state

	c.send(t, `{"type":"ping","tunnel":"bravo"}`)

	select {
	case req := <-reqs:
		if req.Tunnel != "bravo" {
			t.Errorf("tunnel = %q, want bravo", req.Tunnel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ping request")
	}
}

func TestAPingForATunnelThatIsNotThereIsDropped(t *testing.T) {
	// The name a client sends decides nothing but which configured address is
	// probed. One that is in no view names no address, so there is nothing to
	// pass on — and passing it on anyway is how a name from the wire reaches
	// code that resolves it.
	s := serving(t, nil)
	s.Publish(aView("alpha"))
	reqs := s.Requests()
	c := dial(t, s)
	c.next(t) // hello
	c.next(t) // state

	c.send(t, `{"type":"ping","tunnel":"nowhere"}`)
	c.send(t, `{"type":"ping"}`)

	select {
	case req := <-reqs:
		if req.Tunnel != "" {
			t.Errorf("tunnel = %q: a name that is in no view was passed on", req.Tunnel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ping request")
	}
}

func TestPingsAreRateLimitedLikeRefreshes(t *testing.T) {
	// A menu that reopens in a loop must not turn into a packet source.
	clock := &fakeClock{}
	s := serving(t, nil, stopped(clock))
	reqs := s.Requests()
	c := dial(t, s)
	c.next(t) // hello

	c.send(t, `{"type":"ping"}`)
	select {
	case <-reqs:
	case <-time.After(2 * time.Second):
		t.Fatal("no ping request")
	}

	// The clock has not moved, so this one is inside the floor. A refresh
	// behind it does travel — its own floor has never been reached — and both
	// lines are read in order off the one connection, so seeing the refresh
	// first proves the ping was dropped rather than merely slow.
	c.send(t, `{"type":"ping"}`)
	c.send(t, `{"type":"refresh"}`)

	select {
	case req := <-reqs:
		if req.Kind != RequestRefresh {
			t.Errorf("kind = %q: a ping inside the floor was passed on", req.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request at all")
	}
}

func TestAPingFloorIsItsOwnRatherThanSharedWithRefresh(t *testing.T) {
	// Two verbs, two costs. Asking for a fresh view must not silence a ping.
	clock := &fakeClock{}
	s := serving(t, nil, stopped(clock))
	reqs := s.Requests()
	c := dial(t, s)
	c.next(t) // hello

	c.send(t, `{"type":"refresh"}`)
	select {
	case <-reqs:
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh request")
	}

	c.send(t, `{"type":"ping"}`)
	select {
	case req := <-reqs:
		if req.Kind != RequestPing {
			t.Errorf("kind = %q, want the ping through", req.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the refresh floor silenced the ping")
	}
}

func TestPublishPingsReachesEveryClient(t *testing.T) {
	s := serving(t, nil)
	first, second := dial(t, s), dial(t, s)
	first.next(t)
	second.next(t)

	s.PublishPings([]wire.Ping{{Tunnel: "alpha", RTT: 18}})

	for name, c := range map[string]*conn{"first": first, "second": second} {
		msg := c.next(t)
		if msg["type"] != "ping" {
			t.Fatalf("%s got type %v, want ping", name, msg["type"])
		}
		results, ok := msg["results"].([]any)
		if !ok || len(results) != 1 {
			t.Fatalf("%s got results %v, want one", name, msg["results"])
		}
		got, _ := results[0].(map[string]any)
		if got["tunnel"] != "alpha" || got["rtt_ms"] != 18.0 {
			t.Errorf("%s got %v, want alpha at 18ms", name, got)
		}
	}
}

func TestAPingIsNotReplayedToWhoeverConnectsNext(t *testing.T) {
	// A view keeps its meaning for minutes; a round-trip time does not. Showing
	// a client a measurement taken before it connected, with no way to tell how
	// old it is, is worse than showing it nothing.
	s := serving(t, nil)
	s.Publish(aView("alpha"))
	s.PublishPings([]wire.Ping{{Tunnel: "alpha", RTT: 18}})

	c := dial(t, s)
	if got := c.next(t)["type"]; got != "hello" {
		t.Fatalf("first line is %v, want hello", got)
	}
	if got := c.next(t)["type"]; got != "state" {
		t.Errorf("second line is %v, want state and no replayed ping", got)
	}
}
