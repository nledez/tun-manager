package probe

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"
)

// Simulated answers without sending anything.
//
// It exists for the demo: the tunnels a screenshot shows are served by
// internal/tools/wgsim, so their check addresses answer nothing, and a table of
// red crosses says less about the program than a table of latencies. It is
// reached only through the `--fake-ping` flag, which says what it is.
//
// Deliberately not a random generator. Two runs of the same demo produce the
// same numbers, so a screenshot can be retaken and a test can assert on the
// values rather than on a range.
type Simulated struct{}

// simulatedFloor and simulatedCeiling bound the invented round trips. Wide
// enough to look like several different paths, narrow enough that none of them
// looks like a fault.
const (
	simulatedFloor   = 8 * time.Millisecond
	simulatedCeiling = 60 * time.Millisecond

	// simulatedFailureIn is how often an address is answered as unreachable:
	// one in this many. A demo where everything works never shows what a
	// failure looks like, which is half of what the column is for.
	simulatedFailureIn = 5
)

// Ping returns an invented round trip, or an invented failure.
//
// The context is accepted to satisfy Pinger and ignored: there is nothing to
// cancel, because nothing is sent.
func (Simulated) Ping(_ context.Context, host string) (time.Duration, error) {
	sum := fnv.New32a()
	// Writing to a hash cannot fail; the interface says otherwise because it is
	// io.Writer.
	_, _ = sum.Write([]byte(host))
	n := sum.Sum32()

	if n%simulatedFailureIn == 0 {
		return 0, fmt.Errorf("ping %s: no reply", host)
	}

	span := simulatedCeiling - simulatedFloor
	return simulatedFloor + time.Duration(uint64(n)%uint64(span)), nil
}
