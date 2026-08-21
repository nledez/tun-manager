package format

import (
	"strings"
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

func TestRateReadsZeroAsIdleRatherThanAbsent(t *testing.T) {
	// Unlike a byte count, a rate of zero is a measurement: the tunnel is up
	// and nothing is going through it.
	if got := Rate(0); got != "0B/s" {
		t.Errorf("Rate(0) = %q, want %q", got, "0B/s")
	}
}

func TestRateScalesLikeAByteCount(t *testing.T) {
	for in, want := range map[float64]string{
		512:     "512B/s",
		1536:    "1.5K/s",
		1258291: "1.2M/s",
	} {
		if got := Rate(in); got != want {
			t.Errorf("Rate(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestRateRoundsToWholeBytes(t *testing.T) {
	if got := Rate(1023.9); got != "1023B/s" {
		t.Errorf("Rate(1023.9) = %q, want %q", got, "1023B/s")
	}
}

func TestDisplayRemovesWhatWouldDriveTheTerminal(t *testing.T) {
	// A terminal is an interpreter, and this program prints values it was
	// given: endpoints out of .conf files, context names out of the user's
	// configuration, the output of commands it ran. An escape sequence in one
	// of those repaints the screen.
	got := Display("alpha\x1b[2Jbravo\x07\r\n")

	// The ESC itself is gone; what followed it is ordinary text and stays,
	// because removing that would be guessing at where a sequence ended.
	if got != "alpha[2Jbravo" {
		t.Errorf("Display = %q, want the escape gone and the text kept", got)
	}
	for _, bad := range []string{"\x1b", "\x07", "\r", "\n"} {
		if strings.Contains(got, bad) {
			t.Errorf("Display kept %q: %q", bad, got)
		}
	}
}

func TestDisplayRemovesWhatWouldReorderTheText(t *testing.T) {
	// U+202E draws "moc.elpmaxe" as "example.com". Most of what this program
	// shows is somewhere traffic goes, so a value that can be drawn as another
	// address is the one worth caring about.
	got := Display("evil.example‮/gro.tsurt")

	if strings.ContainsRune(got, '‮') {
		t.Errorf("Display kept the override: %q", got)
	}
	if !strings.HasPrefix(got, "evil.example") {
		t.Errorf("Display = %q, want the text itself kept", got)
	}
}

func TestDisplayKeepsWhatPeopleActuallyWrite(t *testing.T) {
	// Accents, non-Latin scripts and emoji are not attacks, and a sanitiser
	// that eats them is one somebody works around.
	for _, s := range []string{"café-vpn", "берлин", "東京", "office 🏢", "a_b-c1"} {
		if got := Display(s); got != s {
			t.Errorf("Display(%q) = %q, want it untouched", s, got)
		}
	}
	if got := Display("a\tb"); got != "a b" {
		t.Errorf("Display of a tab = %q, want a space", got)
	}
}

func TestDisplayCutsWhatWouldOwnTheScreen(t *testing.T) {
	long := strings.Repeat("é", 10_000)

	got := Display(long)

	if runes := []rune(got); len(runes) != DisplayLimit {
		t.Errorf("Display kept %d runes, want %d", len(runes), DisplayLimit)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Display = %q, want it to say it was cut", got)
	}
	// Cut on a rune boundary: a half-written é is a replacement character on
	// screen, which reads as corruption rather than as truncation.
	if strings.ContainsRune(got, '�') {
		t.Error("Display cut in the middle of a rune")
	}
}
