// Package rate turns WireGuard's cumulative byte counters into a bounded
// history of transfer rates, and draws that history as a graph.
//
// The counters only ever say how much has passed since the tunnel came up, so
// a rate is a difference between two readings. Three things make that
// difference lie, and all three are handled here rather than by whoever is
// drawing: the first reading has nothing to compare against, a tunnel restarted
// between two readings starts counting again, and two readings taken at the
// same instant would divide by no time at all.
package rate

import "time"

// Point is one measured pair of rates, in bytes per second.
type Point struct {
	Down float64
	Up   float64
}

// Series holds the most recent points, oldest first.
type Series struct {
	points []Point
	limit  int

	last     reading
	haveLast bool
}

type reading struct {
	at     time.Time
	rx, tx int64
}

// New returns a series keeping at most limit points, which is however many fit
// on screen.
func New(limit int) *Series {
	return &Series{limit: limit}
}

// Add records a reading of the cumulative counters and, when it can be compared
// with the one before it, the rate between the two.
func (s *Series) Add(at time.Time, rx, tx int64) {
	prev, had := s.last, s.haveLast
	s.last, s.haveLast = reading{at: at, rx: rx, tx: tx}, true
	if !had {
		return
	}

	elapsed := at.Sub(prev.at).Seconds()
	if elapsed <= 0 {
		// Either the clock did not move or it went backwards. Neither is a rate.
		return
	}

	s.push(Point{
		Down: perSecond(rx-prev.rx, elapsed),
		Up:   perSecond(tx-prev.tx, elapsed),
	})
}

// perSecond reads a counter that went backwards as idle. It means the tunnel
// was restarted between the two readings, not that bytes travelled backwards.
func perSecond(delta int64, elapsed float64) float64 {
	if delta < 0 {
		return 0
	}
	return float64(delta) / elapsed
}

func (s *Series) push(p Point) {
	if s.limit <= 0 {
		return
	}
	s.points = append(s.points, p)
	if len(s.points) > s.limit {
		s.points = s.points[len(s.points)-s.limit:]
	}
}

// Points returns the history, oldest first.
func (s *Series) Points() []Point {
	return append([]Point(nil), s.points...)
}

// Down returns the received rates alone, for drawing.
func (s *Series) Down() []float64 { return s.column(func(p Point) float64 { return p.Down }) }

// Up returns the sent rates alone.
func (s *Series) Up() []float64 { return s.column(func(p Point) float64 { return p.Up }) }

func (s *Series) column(of func(Point) float64) []float64 {
	out := make([]float64, len(s.points))
	for i, p := range s.points {
		out[i] = of(p)
	}
	return out
}

// Peak returns the highest rate seen in each direction. They are separate
// because a download that dwarfs an upload would otherwise flatten it into a
// line along the axis.
func (s *Series) Peak() (down, up float64) {
	for _, p := range s.points {
		down = max(down, p.Down)
		up = max(up, p.Up)
	}
	return down, up
}
