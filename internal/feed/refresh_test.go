package feed

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a clock a test can move. It is guarded because the server reads
// it from its own goroutines while the test writes it.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// stopped returns a server tweak installing a clock that does not move, so the
// refresh floor is entirely under the test's control.
func stopped(c *fakeClock) func(*Server) {
	c.at = taken
	return func(s *Server) { s.Now = c.now }
}

func TestAClientCanAskForAFreshView(t *testing.T) {
	// A menu bar opened between two ticks would otherwise show state up to
	// five minutes old.
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"refresh"}`)

	select {
	case req := <-s.Requests():
		if req.Kind != RequestRefresh {
			t.Errorf("request = %+v, want a refresh", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request arrived")
	}
}

func TestRefreshesInQuickSuccessionAreCollapsed(t *testing.T) {
	// Reading the whole system is not free, and a client is not allowed to
	// turn the menu bar into a way of hammering it.
	clock := &fakeClock{}
	s := serving(t, nil, stopped(clock))
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"refresh"}`)
	<-s.Requests()
	c.send(t, `{"type":"refresh"}`)

	select {
	case req := <-s.Requests():
		t.Errorf("a second request got through at once: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestARefreshIsAllowedAgainOnceTheFloorHasPassed(t *testing.T) {
	clock := &fakeClock{}
	s := serving(t, nil, stopped(clock))
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"refresh"}`)
	<-s.Requests()

	clock.advance(refreshFloor + time.Second)
	c.send(t, `{"type":"refresh"}`)

	select {
	case req := <-s.Requests():
		if req.Kind != RequestRefresh {
			t.Errorf("request = %+v, want a refresh", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request arrived after the floor had passed")
	}
}

func TestRequestsNobodyReadsDoNotWedgeTheFeed(t *testing.T) {
	// Nothing guarantees the interface is listening. A refresh that cannot be
	// delivered is dropped, because the next one carries the same meaning.
	clock := &fakeClock{}
	s := serving(t, nil, stopped(clock))
	c := dial(t, s)
	c.next(t)

	for range 5 {
		clock.advance(refreshFloor + time.Second)
		c.send(t, `{"type":"refresh"}`)
	}

	// The feed is still serving.
	s.Publish(aView("alpha"))
	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("got %v, want the feed still running", msg)
	}
}

func TestRequestsIsUsableBeforeServeStarts(t *testing.T) {
	// The composition root wires the channel into the interface before it
	// starts accepting anything.
	if (&Server{Path: "/nonexistent"}).Requests() == nil {
		t.Error("Requests returned nil")
	}
}
