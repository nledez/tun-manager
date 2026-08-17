package rate

import "strings"

// A braille cell is a 2x4 grid of dots, which is what makes it a graph rather
// than a row of bars: a terminal row holds four levels and a terminal column
// holds two readings.
const (
	dotsWide = 2
	dotsTall = 4

	// brailleBase is U+2800, the cell with no dot raised. Every pattern is this
	// plus the bits of the dots that are up.
	brailleBase = 0x2800
)

// dotBit maps a dot's position in the cell, counted from the top left, to the
// bit that raises it. The layout is historical rather than regular: the fourth
// row was added to the six-dot alphabet later and took the high bits.
var dotBit = [dotsWide][dotsTall]rune{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

// Braille draws values as a graph of width cells and height rows, scaled so
// that peak reaches the top.
//
// The last values are the ones drawn: a graph reads left to right and ends at
// now, so it is the oldest readings that fall off when there is more history
// than room.
func Braille(values []float64, width, height int, peak float64) []string {
	if height <= 0 {
		return nil
	}
	rows := make([]string, height)
	if width <= 0 {
		return rows
	}

	levels := levelsOf(values, width*dotsWide, height*dotsTall, peak)

	for y := range rows {
		var row strings.Builder
		row.Grow(width * 3)
		for x := 0; x < width; x++ {
			row.WriteRune(cell(levels, x, y, height))
		}
		rows[y] = row.String()
	}
	return rows
}

// levelsOf turns each value into a number of dots raised from the bottom,
// keeping the last columns worth of them.
func levelsOf(values []float64, columns, tall int, peak float64) []int {
	if len(values) > columns {
		values = values[len(values)-columns:]
	}

	levels := make([]int, columns)
	if peak <= 0 {
		// Nothing has moved, so there is no scale to draw against and nothing
		// to draw either.
		return levels
	}

	// Right-aligned: the newest reading is the rightmost column.
	offset := columns - len(values)
	for i, v := range values {
		level := int(v / peak * float64(tall))
		levels[offset+i] = min(max(level, 0), tall)
	}
	return levels
}

// cell builds the character at one position from the dots that fall inside it.
func cell(levels []int, x, y, height int) rune {
	pattern := rune(brailleBase)
	for dx := 0; dx < dotsWide; dx++ {
		level := levels[x*dotsWide+dx]
		for dy := 0; dy < dotsTall; dy++ {
			// Rows are drawn top down and the graph grows bottom up, so how
			// high this dot sits is counted from the last row.
			fromBottom := (height-1-y)*dotsTall + (dotsTall - 1 - dy)
			if fromBottom < level {
				pattern |= dotBit[dx][dy]
			}
		}
	}
	return pattern
}
