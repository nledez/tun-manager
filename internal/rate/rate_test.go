package rate

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

func TestTheFirstReadingHasNoRate(t *testing.T) {
	// A rate is a difference between two readings, and there is only one.
	s := New(10)

	s.Add(t0, 1000, 500)

	if got := s.Points(); len(got) != 0 {
		t.Errorf("points = %v, want none from a single reading", got)
	}
}

func TestARateIsTheDifferenceOverTheElapsedTime(t *testing.T) {
	s := New(10)

	s.Add(t0, 1000, 500)
	s.Add(t0.Add(2*time.Second), 3000, 900)

	got := s.Points()
	if len(got) != 1 {
		t.Fatalf("points = %v, want one", got)
	}
	if got[0].Down != 1000 {
		t.Errorf("down = %v, want 2000 bytes over 2s", got[0].Down)
	}
	if got[0].Up != 200 {
		t.Errorf("up = %v, want 400 bytes over 2s", got[0].Up)
	}
}

func TestACounterGoingBackwardsReadsAsIdle(t *testing.T) {
	// A tunnel taken down and brought back up starts its counters again. The
	// difference is negative, which as a rate would be a spike pointing the
	// wrong way.
	s := New(10)

	s.Add(t0, 5000, 5000)
	s.Add(t0.Add(time.Second), 10, 10)

	got := s.Points()
	if len(got) != 1 {
		t.Fatalf("points = %v, want one", got)
	}
	if got[0].Down != 0 || got[0].Up != 0 {
		t.Errorf("point = %+v, want zero rather than a negative rate", got[0])
	}
}

func TestTwoReadingsAtTheSameInstantAreIgnored(t *testing.T) {
	// Dividing by no elapsed time is an infinite rate.
	s := New(10)

	s.Add(t0, 1000, 500)
	s.Add(t0, 9999, 9999)

	if got := s.Points(); len(got) != 0 {
		t.Errorf("points = %v, want none when no time passed", got)
	}
}

func TestAReadingFromThePastIsIgnored(t *testing.T) {
	s := New(10)

	s.Add(t0, 1000, 500)
	s.Add(t0.Add(-time.Second), 2000, 900)

	if got := s.Points(); len(got) != 0 {
		t.Errorf("points = %v, want none from a reading that went backwards in time", got)
	}
}

func TestTheSeriesKeepsOnlyItsMostRecentPoints(t *testing.T) {
	// The window is what fits on screen; older than that is off the left edge.
	s := New(3)

	for i := 0; i <= 5; i++ {
		s.Add(t0.Add(time.Duration(i)*time.Second), int64(i*100), 0)
	}

	got := s.Points()
	if len(got) != 3 {
		t.Fatalf("points = %v, want three", got)
	}
	for _, p := range got {
		if p.Down != 100 {
			t.Errorf("down = %v, want 100 per second throughout", p.Down)
		}
	}
}

func TestASeriesWithoutALimitKeepsNothing(t *testing.T) {
	// A window of zero is a window: it holds nothing, rather than everything.
	s := New(0)

	s.Add(t0, 0, 0)
	s.Add(t0.Add(time.Second), 100, 100)

	if got := s.Points(); len(got) != 0 {
		t.Errorf("points = %v, want none", got)
	}
}

func TestPeakReportsTheHighestOfEachDirection(t *testing.T) {
	// The two directions scale on their own, so each has its own peak.
	s := New(10)
	s.Add(t0, 0, 0)
	s.Add(t0.Add(time.Second), 100, 900)
	s.Add(t0.Add(2*time.Second), 900, 1000)

	down, up := s.Peak()

	if down != 800 {
		t.Errorf("down peak = %v, want 800", down)
	}
	if up != 900 {
		t.Errorf("up peak = %v, want 900", up)
	}
}

func TestPeakOfAnEmptySeriesIsZero(t *testing.T) {
	down, up := New(10).Peak()

	if down != 0 || up != 0 {
		t.Errorf("peak = %v/%v, want zero", down, up)
	}
}

func TestDownAndUpAreReadSeparately(t *testing.T) {
	s := New(10)
	s.Add(t0, 0, 0)
	s.Add(t0.Add(time.Second), 100, 200)

	if got := s.Down(); len(got) != 1 || got[0] != 100 {
		t.Errorf("Down = %v, want [100]", got)
	}
	if got := s.Up(); len(got) != 1 || got[0] != 200 {
		t.Errorf("Up = %v, want [200]", got)
	}
}
