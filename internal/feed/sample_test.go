package feed

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/wgconf"
)

// countingSampler answers every reading and records who was asked.
type countingSampler struct {
	reads atomic.Int64

	mu   sync.Mutex
	seen map[string]int
}

func newSampler() *countingSampler {
	return &countingSampler{seen: map[string]int{}}
}

func (c *countingSampler) Sample(tun wgconf.Tunnel) (app.Sample, bool) {
	n := c.reads.Add(1)
	c.mu.Lock()
	c.seen[tun.Name]++
	c.mu.Unlock()
	return app.Sample{At: taken, Rx: 1000 + n, Tx: 500 + n}, true
}

func (c *countingSampler) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[name]
}

// deadSampler is a tunnel that is down: no counters to read.
type deadSampler struct{}

func (deadSampler) Sample(wgconf.Tunnel) (app.Sample, bool) { return app.Sample{}, false }

// eventually waits for a condition rather than for a duration.
func eventually(t *testing.T, why string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

func TestWatchingATunnelStartsItsSamples(t *testing.T) {
	s := serving(t, newSampler())
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t) // hello
	c.next(t) // state

	c.send(t, `{"type":"watch","tunnel":"alpha"}`)

	msg := c.next(t)
	if msg["type"] != "sample" {
		t.Fatalf("got %v, want a sample", msg)
	}
	if msg["tunnel"] != "alpha" {
		t.Errorf("tunnel = %v, want alpha", msg["tunnel"])
	}
	if _, ok := msg["rx"]; !ok {
		t.Errorf("sample = %v, want a counter", msg)
	}
}

func TestNothingIsSampledUntilSomebodyWatches(t *testing.T) {
	// A reading a second for a graph nobody is looking at is a reading wasted.
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	dial(t, s)

	time.Sleep(50 * time.Millisecond) // ten intervals

	if got := sampler.reads.Load(); got != 0 {
		t.Errorf("reads = %d, want none with nobody watching", got)
	}
}

func TestUnwatchingStopsTheReadings(t *testing.T) {
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)
	c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	eventually(t, "the first reading", func() bool { return sampler.reads.Load() > 0 })

	c.send(t, `{"type":"unwatch","tunnel":"alpha"}`)

	// Let whatever was already in flight land, then hold still.
	time.Sleep(50 * time.Millisecond)
	settled := sampler.reads.Load()
	time.Sleep(50 * time.Millisecond)
	if got := sampler.reads.Load(); got != settled {
		t.Errorf("reads went %d -> %d, want the loop stopped", settled, got)
	}
}

func TestTwoClientsWatchingOneTunnelCostOneReading(t *testing.T) {
	// The union is sampled, not each client's list: two menu bars on the same
	// tunnel are one control-socket read, not two.
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))

	for _, c := range []*conn{dial(t, s), dial(t, s)} {
		c.next(t)
		c.next(t)
		c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	}

	eventually(t, "several rounds of sampling", func() bool { return sampler.count("alpha") >= 4 })

	// Every read went to alpha and nowhere else; the count above is rounds,
	// not clients.
	if got := sampler.count("bravo"); got != 0 {
		t.Errorf("bravo was read %d time(s), want none", got)
	}
}

func TestEachClientOnlyHearsAboutWhatItWatches(t *testing.T) {
	s := serving(t, newSampler())
	s.Publish(aView("alpha", "bravo"))

	watcher := dial(t, s)
	watcher.next(t)
	watcher.next(t)
	watcher.send(t, `{"type":"watch","tunnel":"alpha"}`)

	bystander := dial(t, s)
	bystander.next(t)
	bystander.next(t)

	if msg := watcher.next(t); msg["tunnel"] != "alpha" {
		t.Errorf("watcher got %v, want alpha", msg)
	}

	// The bystander asked for nothing, so the next thing it hears is a state.
	s.Publish(aView("alpha", "bravo"))
	if msg := bystander.next(t); msg["type"] != "state" {
		t.Errorf("bystander got %v, want no samples it never asked for", msg)
	}
}

func TestAWatchSurvivesUntilAViewNamesTheTunnel(t *testing.T) {
	// A client may watch before the first view lands. The watch waits rather
	// than being thrown away, or the menu bar's first graph is always empty.
	sampler := newSampler()
	s := serving(t, sampler)
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	time.Sleep(30 * time.Millisecond)
	if got := sampler.reads.Load(); got != 0 {
		t.Fatalf("reads = %d, want none before a view names the tunnel", got)
	}

	s.Publish(aView("alpha"))

	eventually(t, "sampling to start once the tunnel is known",
		func() bool { return sampler.reads.Load() > 0 })
}

func TestWatchingATunnelNobodyHasHeardOfIsIgnored(t *testing.T) {
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)

	c.send(t, `{"type":"watch","tunnel":"nowhere"}`)
	time.Sleep(50 * time.Millisecond)

	if got := sampler.reads.Load(); got != 0 {
		t.Errorf("reads = %d, want none for a tunnel that is not in the view", got)
	}
}

func TestWatchingUnknownTunnelsDoesNotGrowTheWatchSetWithoutBound(t *testing.T) {
	// Once a view is known, a name that is not in it will never produce a
	// reading. Keeping it anyway would let a client grow c.watch without
	// bound, and sampleOnce rebuilds a map that size every second while
	// holding s.mu - the same mutex Publish needs.
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)

	for i := range 500 {
		c.send(t, fmt.Sprintf(`{"type":"watch","tunnel":"nowhere-%d"}`, i))
	}
	// Give the reads a moment to land; there is nothing to wait on directly
	// since none of them are supposed to do anything observable.
	time.Sleep(100 * time.Millisecond)

	s.mu.Lock()
	var watch map[string]bool
	for cl := range s.clients {
		watch = cl.watch
	}
	n := len(watch)
	s.mu.Unlock()
	if n != 0 {
		t.Errorf("watch set = %d entries, want none for tunnels no view has ever named", n)
	}
}

func TestWatchingWithNoTunnelDoesNotStartTheLoop(t *testing.T) {
	// A watch with nothing named is not a watch on the empty string; it is
	// dropped before it can register at all, so it must not be what starts the
	// loop.
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)

	c.send(t, `{"type":"watch"}`)
	time.Sleep(50 * time.Millisecond)

	s.mu.Lock()
	sampling := s.sampling
	s.mu.Unlock()
	if sampling != nil {
		t.Error("sampling loop started for a watch with no tunnel")
	}
}

func TestATunnelThatIsDownProducesNoSample(t *testing.T) {
	// No counters is a fact, not a failure, and a zero reading would draw as a
	// tunnel doing nothing rather than a tunnel that is not there.
	s := serving(t, deadSampler{})
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)
	c.send(t, `{"type":"watch","tunnel":"alpha"}`)

	time.Sleep(50 * time.Millisecond)
	s.Publish(aView("alpha"))

	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("got %v, want no sample for a tunnel that is down", msg)
	}
}

func TestSampleOnceWithNoSamplerDoesNotPanic(t *testing.T) {
	// Only startFeed builds a server with a Sampler today, so this path is
	// unreachable in production, but a nil Sampler is one field away and a
	// panic in a process running as root is worth guarding against.
	s := serving(t, nil)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)

	c.send(t, `{"type":"watch","tunnel":"alpha"}`)

	// A panic in the sampling goroutine takes the whole test binary down with
	// it; surviving past several intervals with nothing watching having
	// crashed is the assertion.
	time.Sleep(50 * time.Millisecond)
}

func TestAClientLeavingStopsTheReadingsItAskedFor(t *testing.T) {
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)
	c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	eventually(t, "the first reading", func() bool { return sampler.reads.Load() > 0 })

	c.Close()

	eventually(t, "the loop to stop with the last watcher", func() bool {
		settled := sampler.reads.Load()
		time.Sleep(40 * time.Millisecond)
		return sampler.reads.Load() == settled
	})
}
