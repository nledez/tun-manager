package tui

import (
	"errors"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

var errRefresh = errors.New("read the control socket: permission denied")

// recorder is a Feed that keeps what it was given.
type recorder struct {
	mu    sync.Mutex
	views []app.View
}

func (r *recorder) Publish(v app.View) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.views = append(r.views, v)
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
