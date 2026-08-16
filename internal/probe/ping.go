// Package probe measures reachability of the remote end of a tunnel.
//
// It answers two questions: the pre-check before bringing a tunnel up ("is this
// address already reachable?"), and the round-trip time shown in the TUI.
package probe

import (
	"context"
	"fmt"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

// DefaultTimeout bounds a single probe. Tunnel endpoints that need longer than
// this are not usable anyway.
const DefaultTimeout = 2 * time.Second

// Result is the outcome of one probe.
type Result struct {
	RTT time.Duration
	Err error
}

// ICMP pings hosts over ICMP.
//
// It uses unprivileged datagram sockets, which macOS grants to every user. That
// keeps the package testable outside of sudo even though the program itself
// runs as root.
type ICMP struct {
	Count   int
	Timeout time.Duration
}

// New returns a pinger with the default settings.
func New() *ICMP { return &ICMP{Count: 1, Timeout: DefaultTimeout} }

func (p *ICMP) count() int {
	if p.Count <= 0 {
		return 1
	}
	return p.Count
}

func (p *ICMP) timeout() time.Duration {
	if p.Timeout <= 0 {
		return DefaultTimeout
	}
	return p.Timeout
}

// Ping probes a host and returns the average round-trip time. An unanswered
// probe is an error, not a zero duration.
func (p *ICMP) Ping(ctx context.Context, host string) (time.Duration, error) {
	pinger, err := probing.NewPinger(host)
	if err != nil {
		return 0, fmt.Errorf("ping %s: %w", host, err)
	}
	pinger.Count = p.count()
	pinger.Timeout = p.timeout()
	// Datagram sockets rather than raw sockets: no privilege needed on darwin.
	pinger.SetPrivileged(false)

	if err := pinger.RunWithContext(ctx); err != nil {
		return 0, fmt.Errorf("ping %s: %w", host, err)
	}

	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return 0, fmt.Errorf("ping %s: no reply", host)
	}
	return stats.AvgRtt, nil
}

// Pinger is the behaviour Controller and the TUI depend on.
type Pinger interface {
	Ping(ctx context.Context, host string) (time.Duration, error)
}

// All probes every host concurrently and returns one result per host. Empty
// host names are skipped: a tunnel with no known check address has nothing to
// probe.
func All(ctx context.Context, p Pinger, hosts []string) map[string]Result {
	results := make(map[string]Result, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, host := range hosts {
		if host == "" {
			continue
		}
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			rtt, err := p.Ping(ctx, host)
			mu.Lock()
			results[host] = Result{RTT: rtt, Err: err}
			mu.Unlock()
		}(host)
	}
	wg.Wait()
	return results
}
