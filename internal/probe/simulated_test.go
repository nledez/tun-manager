package probe

import (
	"context"
	"testing"
	"time"
)

// documentationHosts are addresses from the ranges reserved for documentation
// (RFC 5737), which is what the demo configuration uses.
var documentationHosts = []string{
	"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5",
	"198.51.100.1", "198.51.100.2", "203.0.113.1", "203.0.113.2", "203.0.113.3",
}

func TestASimulatedProbeSendsNothingAndAnswersAnyway(t *testing.T) {
	// The point of the type: the demo's addresses reach nothing, so a real
	// probe would take the timeout and then fail.
	before := time.Now()

	rtt, err := Simulated{}.Ping(context.Background(), "192.0.2.1")

	if elapsed := time.Since(before); elapsed > 100*time.Millisecond {
		t.Errorf("took %v: something was actually sent", elapsed)
	}
	if err != nil && rtt != 0 {
		t.Errorf("rtt = %v alongside err = %v, want one or the other", rtt, err)
	}
}

func TestTheSameAddressAlwaysGivesTheSameAnswer(t *testing.T) {
	// A screenshot has to be retakeable. A random generator here would mean the
	// second capture of the same demo disagrees with the first.
	first, firstErr := Simulated{}.Ping(context.Background(), "192.0.2.1")

	for range 5 {
		got, err := Simulated{}.Ping(context.Background(), "192.0.2.1")
		if got != first || (err == nil) != (firstErr == nil) {
			t.Fatalf("got %v/%v then %v/%v, want the same answer every time",
				first, firstErr, got, err)
		}
	}
}

func TestDifferentAddressesGiveDifferentLatencies(t *testing.T) {
	// A column where every row reads 18ms looks like a broken column.
	seen := map[time.Duration]bool{}
	for _, host := range documentationHosts {
		rtt, err := Simulated{}.Ping(context.Background(), host)
		if err == nil {
			seen[rtt] = true
		}
	}

	if len(seen) < 3 {
		t.Errorf("%d distinct latencies across %d addresses, want a spread",
			len(seen), len(documentationHosts))
	}
}

func TestAnAnsweredProbeStaysInsideTheBoundsOfSomethingPlausible(t *testing.T) {
	// Below the floor it reads as a loopback rather than a tunnel; above the
	// ceiling it reads as a fault, which is what the failures are for.
	for _, host := range documentationHosts {
		rtt, err := Simulated{}.Ping(context.Background(), host)
		if err != nil {
			continue
		}
		if rtt < simulatedFloor || rtt >= simulatedCeiling {
			t.Errorf("%s = %v, want between %v and %v",
				host, rtt, simulatedFloor, simulatedCeiling)
		}
	}
}

func TestSomeAddressesAnswerNothing(t *testing.T) {
	// A demo where everything works never shows what a failure looks like,
	// which is half of what the column is for.
	failures := 0
	for _, host := range documentationHosts {
		_, err := Simulated{}.Ping(context.Background(), host)
		if err != nil {
			failures++
		}
	}

	if failures == 0 {
		t.Errorf("every one of %d addresses answered, want at least one failure",
			len(documentationHosts))
	}
	if failures == len(documentationHosts) {
		t.Error("none of them answered, want a mixture")
	}
}

func TestAFailedProbeReportsNoTime(t *testing.T) {
	// Zero is a measurement; the caller tells them apart by the error, and a
	// duration beside one would be read as both.
	for _, host := range documentationHosts {
		rtt, err := Simulated{}.Ping(context.Background(), host)
		if err != nil && rtt != 0 {
			t.Errorf("%s = %v with err %v, want no duration", host, rtt, err)
		}
	}
}

func TestASimulatedProbeSatisfiesPinger(t *testing.T) {
	// It is reached through the interface, never by its own name: the flag
	// swaps it in where *ICMP would go.
	var p Pinger = Simulated{}

	// Either outcome is fine here. What is being asserted is that it fits.
	if _, err := p.Ping(context.Background(), "192.0.2.2"); err != nil {
		t.Logf("this address is one of the ones that answers nothing: %v", err)
	}
}
