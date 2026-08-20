package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wire"
)

var errRefresh = errors.New("read the control socket: permission denied")

// recorder is a Feed that keeps what it was given.
type recorder struct {
	mu    sync.Mutex
	views []app.View
	pings [][]wire.Ping
}

func (r *recorder) Publish(v app.View) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.views = append(r.views, v)
}

func (r *recorder) PublishPings(p []wire.Ping) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pings = append(r.pings, p)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.views)
}

// run executes a command and everything it batches, so that a side effect
// hidden in a tea.Cmd can be observed.
func run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if msg, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range msg {
			run(c)
		}
	}
}

func TestEveryViewIsPublished(t *testing.T) {
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Up))})
	run(cmd)

	if got := rec.count(); got != 1 {
		t.Errorf("published %d view(s), want one per refresh", got)
	}
}

func TestPublishingHappensInACommandRatherThanInUpdate(t *testing.T) {
	// Update performs no I/O. A view published from inside it would be a side
	// effect in the one function this program keeps pure.
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Up))})

	if got := rec.count(); got != 0 {
		t.Fatalf("published %d view(s) during Update, want none until the command runs", got)
	}
	run(cmd)
	if got := rec.count(); got != 1 {
		t.Errorf("published %d view(s) after the command, want one", got)
	}
}

func TestAFailedRefreshPublishesNothing(t *testing.T) {
	// There is no view to publish, and the last good one is better than an
	// empty one.
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(viewMsg{err: errRefresh})
	run(cmd)

	if got := rec.count(); got != 0 {
		t.Errorf("published %d view(s) from a failed refresh, want none", got)
	}
}

func TestAnInterfaceWithoutAFeedIsUnaffected(t *testing.T) {
	m := loadedModel(threeRows...)

	next, cmd := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Up))})
	run(cmd)

	if len(next.(Model).view.Rows) != 1 {
		t.Error("the view was not taken")
	}
}

func TestARequestedRefreshIsTakenLikeAnyOther(t *testing.T) {
	reqs := make(chan feed.Request, 1)
	m := loadedModel(threeRows...)
	m.requests = reqs

	next, cmd := m.Update(requestMsg{req: feed.Request{Kind: feed.RequestRefresh}, from: reqs})

	if !next.(Model).refreshing {
		t.Error("refreshing = false, want the refresh started")
	}
	if cmd == nil {
		t.Error("cmd = nil, want the refresh and the next request listened for")
	}
}

func TestARequestArrivingDuringARefreshIsDropped(t *testing.T) {
	// A second read of the whole system while the first is out gains nothing.
	reqs := make(chan feed.Request, 1)
	m := loadedModel(threeRows...)
	m.requests = reqs
	m.refreshing = true

	_, cmd := m.Update(requestMsg{req: feed.Request{Kind: feed.RequestRefresh}, from: reqs})

	if cmd == nil {
		t.Error("cmd = nil, want the next request still listened for")
	}
}

func TestListeningForRequestsEndsWithTheChannel(t *testing.T) {
	reqs := make(chan feed.Request)
	close(reqs)

	if msg := nextRequest(reqs)(); msg != nil {
		t.Errorf("msg = %#v, want nothing once the feed is gone", msg)
	}
}

func TestAnInterfaceWithNoFeedListensForNothing(t *testing.T) {
	if cmd := nextRequest(nil); cmd != nil {
		t.Error("cmd is not nil, want no listener without a feed")
	}
}

func TestNextRequestReturnsWhatArrived(t *testing.T) {
	reqs := make(chan feed.Request, 1)
	reqs <- feed.Request{Kind: feed.RequestRefresh}

	msg, ok := nextRequest(reqs)().(requestMsg)
	if !ok {
		t.Fatalf("msg = %#v, want a requestMsg", msg)
	}
	if msg.req.Kind != feed.RequestRefresh {
		t.Errorf("req = %+v, want a refresh", msg.req)
	}
}

func TestAProbeRoundIsPublished(t *testing.T) {
	// The measurement exists in the interface either way; publishing it is what
	// lets a second program show the same number rather than take its own.
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(pingMsg{results: map[string]probe.Result{
		"10.20.30.a": {RTT: 18 * time.Millisecond},
	}})
	run(cmd)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.pings) != 1 {
		t.Fatalf("published %d round(s), want one", len(rec.pings))
	}
	if got := rec.pings[0]; len(got) != 1 || got[0].Tunnel != "alpha" || got[0].RTT != 18 {
		t.Errorf("pings = %+v, want alpha at 18ms", got)
	}
}

func TestAProbeRoundThatMeasuredNothingIsNotPublished(t *testing.T) {
	// An empty round is what a probe of a tunnel that is down produces. Sending
	// it would clear whatever a client is showing, which is not what was
	// learned.
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(pingMsg{results: map[string]probe.Result{}})
	run(cmd)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.pings) != 0 {
		t.Errorf("published %+v, want nothing", rec.pings)
	}
}

func TestARequestedPingProbesRatherThanRefreshes(t *testing.T) {
	// A ping and a refresh cost different things and answer different
	// questions: asking for one must not run the other.
	reqs := make(chan feed.Request, 1)
	m := loadedModel(threeRows...)
	m.requests = reqs

	next, cmd := m.Update(requestMsg{req: feed.Request{Kind: feed.RequestPing}, from: reqs})

	if next.(Model).refreshing {
		t.Error("refreshing = true, want a ping rather than a refresh")
	}
	if !next.(Model).pinging {
		t.Error("pinging = false, want the probe started")
	}
	if cmd == nil {
		t.Error("cmd = nil, want the probe and the next request listened for")
	}
}

func TestARequestedPingProbesOnlyTheTunnelItNamed(t *testing.T) {
	// A client asking about one tunnel must not make the publisher send packets
	// to every address it knows.
	view := viewOf(threeRows...)

	if got := pingTargets(view, "alpha"); len(got) != 1 || got[0] != "10.20.30.a" {
		t.Errorf("targets = %v, want alpha's check address alone", got)
	}
}

func TestAPingThatNamesNoTunnelProbesThemAll(t *testing.T) {
	view := viewOf(row("alpha", profile.GroupNeeded, wg.Up), row("bravo", profile.GroupNeeded, wg.Up))

	if got := pingTargets(view, ""); len(got) != 2 {
		t.Errorf("targets = %v, want every tunnel that is up", got)
	}
}

func TestAPingForATunnelThatIsDownProbesNothing(t *testing.T) {
	// Its address is unreachable by construction, and the timeout would be read
	// as a fault rather than as the tunnel being off.
	view := viewOf(row("bravo", profile.GroupNeeded, wg.Down))

	if got := pingTargets(view, "bravo"); len(got) != 0 {
		t.Errorf("targets = %v, want nothing to probe", got)
	}
}

func TestASecondPingRequestWhileOneIsRunningIsIgnored(t *testing.T) {
	// The menu bar asks for a round every time somebody opens it. Honouring a
	// second one would double the packets and interleave two sets of answers
	// into one table. The publisher's floor bounds how often a client can ask,
	// not how many rounds overlap here.
	reqs := make(chan feed.Request, 1)
	m := loadedModel(threeRows...)
	m.requests = reqs
	m.pinging = true

	next, cmd := m.Update(requestMsg{req: feed.Request{Kind: feed.RequestPing}, from: reqs})

	if !next.(Model).pinging {
		t.Error("pinging = false, want the round already in flight remembered")
	}
	if cmd == nil {
		t.Error("cmd = nil, want the next request still listened for")
	}
}
