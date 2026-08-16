package format

import (
	"testing"
	"time"
)

var now = time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)

func TestBytesReadsZeroAsNoTraffic(t *testing.T) {
	// "0B" invites reading a number where there is nothing to read.
	if got := Bytes(0); got != None {
		t.Errorf("Bytes(0) = %q, want %q", got, None)
	}
}

func TestBytesBelowAKilobyteStaysExact(t *testing.T) {
	for in, want := range map[int64]string{1: "1B", 512: "512B", 1023: "1023B"} {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBytesScalesThroughEveryUnit(t *testing.T) {
	const k = int64(1024)
	cases := map[int64]string{
		k:             "1.0K",
		1536:          "1.5K",
		1258291:       "1.2M",
		k * k * k:     "1.0G",
		k * k * k * k: "1.0T",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBytesRoundsToOneDecimal(t *testing.T) {
	if got := Bytes(1024 * 1024 * 3 / 2); got != "1.5M" {
		t.Errorf("Bytes(1.5MiB) = %q, want %q", got, "1.5M")
	}
}

func TestAgeOfAZeroTimeIsNothing(t *testing.T) {
	// A tunnel that never handshook has no age, not an age of zero.
	if got := Age(time.Time{}, now); got != None {
		t.Errorf("Age(zero) = %q, want %q", got, None)
	}
}

func TestAgeUnderAMinuteIsInSeconds(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                "0s",
		time.Second:      "1s",
		42 * time.Second: "42s",
		59 * time.Second: "59s",
		// Rounding lands on a whole minute, which belongs to the next unit.
		59500 * time.Millisecond: "1m00s",
	} {
		if got := Age(now.Add(-d), now); got != want {
			t.Errorf("Age(%v ago) = %q, want %q", d, got, want)
		}
	}
}

func TestAgeUnderAnHourIsInMinutesAndSeconds(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Minute:                     "1m00s",
		90 * time.Second:                "1m30s",
		59*time.Minute + 59*time.Second: "59m59s",
	} {
		if got := Age(now.Add(-d), now); got != want {
			t.Errorf("Age(%v ago) = %q, want %q", d, got, want)
		}
	}
}

func TestAgeBeyondAnHourIsInHoursAndMinutes(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Hour:                    "1h00m",
		2 * time.Hour:                "2h00m",
		2*time.Hour + 34*time.Minute: "2h34m",
		50 * time.Hour:               "50h00m",
	} {
		if got := Age(now.Add(-d), now); got != want {
			t.Errorf("Age(%v ago) = %q, want %q", d, got, want)
		}
	}
}

func TestAgeOfAFutureTimestampIsZero(t *testing.T) {
	// A clock adjustment can put a handshake in the future; a negative age
	// would render as garbage.
	if got := Age(now.Add(time.Minute), now); got != "0s" {
		t.Errorf("Age(future) = %q, want %q", got, "0s")
	}
}

func TestAgeIsZeroPaddedSoColumnsStayAligned(t *testing.T) {
	if got := Age(now.Add(-61*time.Second), now); got != "1m01s" {
		t.Errorf("Age(61s ago) = %q, want %q", got, "1m01s")
	}
	if got := Age(now.Add(-(time.Hour + time.Minute)), now); got != "1h01m" {
		t.Errorf("Age(1h01m ago) = %q, want %q", got, "1h01m")
	}
}

func TestOrNoneReplacesAnEmptyString(t *testing.T) {
	if got := OrNone(""); got != None {
		t.Errorf("OrNone(\"\") = %q, want %q", got, None)
	}
}

func TestOrNoneKeepsARealValue(t *testing.T) {
	if got := OrNone("utun7"); got != "utun7" {
		t.Errorf("OrNone(%q) = %q, want it unchanged", "utun7", got)
	}
}
