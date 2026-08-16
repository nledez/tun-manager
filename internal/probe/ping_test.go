package probe

import (
	"context"
	"testing"
	"time"
)

func TestPingLoopbackSucceeds(t *testing.T) {
	p := &ICMP{Count: 1, Timeout: 2 * time.Second}

	rtt, err := p.Ping(context.Background(), "127.0.0.1")
	if err != nil {
		t.Skipf("ICMP unavailable in this environment: %v", err)
	}

	if rtt <= 0 {
		t.Errorf("rtt = %v, want a positive duration", rtt)
	}
}

func TestPingUnreachableAddressFails(t *testing.T) {
	// 192.0.2.0/24 is TEST-NET-1: reserved for documentation, never routable.
	p := &ICMP{Count: 1, Timeout: 300 * time.Millisecond}

	if _, err := p.Ping(context.Background(), "192.0.2.1"); err == nil {
		t.Fatal("Ping succeeded against an unroutable address, want error")
	}
}

func TestPingHonoursContextCancellation(t *testing.T) {
	p := &ICMP{Count: 1, Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := p.Ping(ctx, "192.0.2.1"); err == nil {
		t.Fatal("Ping succeeded, want a cancellation error")
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Ping took %v, want it to stop with the context", elapsed)
	}
}

func TestPingRejectsUnresolvableHost(t *testing.T) {
	p := &ICMP{Count: 1, Timeout: time.Second}

	if _, err := p.Ping(context.Background(), "no-such-host.invalid"); err == nil {
		t.Fatal("Ping succeeded against an unresolvable host, want error")
	}
}

func TestAllProbesEveryHostAndKeepsPerHostErrors(t *testing.T) {
	p := &ICMP{Count: 1, Timeout: 300 * time.Millisecond}

	got := All(context.Background(), p, []string{"127.0.0.1", "192.0.2.1"})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got["192.0.2.1"].Err == nil {
		t.Error("the unroutable host reported no error, want one")
	}
}

func TestAllSkipsEmptyHosts(t *testing.T) {
	p := &ICMP{Count: 1, Timeout: 300 * time.Millisecond}

	got := All(context.Background(), p, []string{"", "127.0.0.1"})

	if _, ok := got[""]; ok {
		t.Error("an empty host produced a result, want it skipped")
	}
}

func TestDefaultsFillInMissingSettings(t *testing.T) {
	p := &ICMP{}

	if p.count() != 1 {
		t.Errorf("count = %d, want 1", p.count())
	}
	if p.timeout() != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", p.timeout(), DefaultTimeout)
	}
}

func TestNewCarriesTheDefaults(t *testing.T) {
	p := New()

	if p.Count != 1 {
		t.Errorf("Count = %d, want 1", p.Count)
	}
	if p.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", p.Timeout, DefaultTimeout)
	}
}
