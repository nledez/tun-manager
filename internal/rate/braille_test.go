package rate

import (
	"strings"
	"testing"
)

const blank = "⠀" // U+2800, a braille cell with no dot raised

func TestAGraphIsAsWideAndTallAsAsked(t *testing.T) {
	got := Braille([]float64{1, 2, 3}, 10, 3, 3)

	if len(got) != 3 {
		t.Fatalf("got %d row(s), want 3", len(got))
	}
	for i, row := range got {
		if n := len([]rune(row)); n != 10 {
			t.Errorf("row %d is %d cells wide, want 10", i, n)
		}
	}
}

func TestNothingToDrawIsABlankGraphOfTheRightShape(t *testing.T) {
	// An empty graph still has to hold its place, or the pane below it moves
	// the moment the first reading lands.
	got := Braille(nil, 6, 2, 0)

	if len(got) != 2 {
		t.Fatalf("got %d row(s), want 2", len(got))
	}
	for _, row := range got {
		if row != strings.Repeat(blank, 6) {
			t.Errorf("row = %q, want six blank cells", row)
		}
	}
}

func TestSilenceDrawsNothing(t *testing.T) {
	got := Braille([]float64{0, 0, 0, 0}, 2, 2, 100)

	for _, row := range got {
		if strings.Trim(row, blank) != "" {
			t.Errorf("row = %q, want nothing raised for zero traffic", row)
		}
	}
}

func TestTheTopOfTheScaleFillsTheColumn(t *testing.T) {
	// A value at the peak reaches the top row; anything less leaves it empty,
	// which is what makes the peak readable without reading the label.
	full := Braille([]float64{100, 100, 100, 100}, 2, 2, 100)

	if strings.Trim(full[0], blank) == "" {
		t.Errorf("top row = %q, want the peak to reach it", full[0])
	}
	if strings.Trim(full[1], blank) == "" {
		t.Errorf("bottom row = %q, want it filled underneath", full[1])
	}
}

func TestAGraphGrowsFromTheBottom(t *testing.T) {
	// Half the scale fills the lower half and leaves the upper half alone.
	got := Braille([]float64{50, 50, 50, 50}, 2, 2, 100)

	if strings.Trim(got[0], blank) != "" {
		t.Errorf("top row = %q, want it empty at half the scale", got[0])
	}
	if strings.Trim(got[1], blank) == "" {
		t.Errorf("bottom row = %q, want it drawn", got[1])
	}
}

func TestTwoValuesShareACell(t *testing.T) {
	// Braille has two dots across, so a cell holds two readings and the graph
	// is twice as dense as the terminal is wide.
	got := Braille([]float64{0, 100}, 1, 1, 100)

	if len(got) != 1 || len([]rune(got[0])) != 1 {
		t.Fatalf("got %q, want a single cell", got)
	}
	if got[0] == blank {
		t.Errorf("cell = %q, want the second reading raised in it", got[0])
	}
}

func TestTheNewestReadingsAreTheOnesDrawn(t *testing.T) {
	// More history than fits is windowed to the right: a graph reads left to
	// right ending at now.
	wide := make([]float64, 100)
	for i := range wide {
		wide[i] = 0
	}
	wide[len(wide)-1] = 100

	got := Braille(wide, 2, 1, 100)

	if strings.Trim(got[0][:len(blank)], blank) != "" {
		t.Errorf("row = %q, want the old silence dropped from the left", got[0])
	}
	if got[0] == strings.Repeat(blank, 2) {
		t.Errorf("row = %q, want the newest reading drawn", got[0])
	}
}

func TestAPeakOfZeroDrawsNothingRatherThanDividingByIt(t *testing.T) {
	got := Braille([]float64{0, 0}, 2, 1, 0)

	if got[0] != strings.Repeat(blank, 2) {
		t.Errorf("row = %q, want nothing raised", got[0])
	}
}

func TestAValueOverThePeakIsClamped(t *testing.T) {
	// The scale is decided before drawing, and a reading past it must fill the
	// column rather than run off the top.
	got := Braille([]float64{500}, 1, 1, 100)

	if got[0] == blank {
		t.Errorf("cell = %q, want the column filled", got[0])
	}
}

func TestAGraphWithNoRoomIsEmpty(t *testing.T) {
	if got := Braille([]float64{1}, 0, 2, 1); len(got) != 2 || got[0] != "" {
		t.Errorf("got %q, want rows with no cells", got)
	}
	if got := Braille([]float64{1}, 4, 0, 1); len(got) != 0 {
		t.Errorf("got %q, want no rows", got)
	}
}
