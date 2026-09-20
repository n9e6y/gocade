// Package render turns a grid of colored cells into terminal output.
//
// It is pure: no I/O, no goroutines, no clock. A game fills a Canvas, and
// Canvas.Frame returns the bytes that draw it.
package render

import (
	"strconv"
	"unicode"
	"unicode/utf8"
)

// Color is a terminal color. The zero value is the terminal's default color.
type Color uint8

// The default color and the eight basic ANSI colors.
const (
	Default Color = iota
	Black
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	White
)

// valid reports whether c is one of the colors above. Anything else is drawn
// as Default.
func (c Color) valid() bool { return c <= White }

// Cell is one character position: a rune and its colors. The zero Cell is a
// blank space in the default colors.
type Cell struct {
	Rune rune
	Fg   Color
	Bg   Color
}

// Canvas is a fixed-size grid of cells. It assumes every rune is one
// terminal column wide (ASCII and box-drawing characters are).
//
// A Canvas is not safe for concurrent use; the goroutine that owns the game
// fills it and renders it.
type Canvas struct {
	w, h  int
	cells []Cell // row-major: cells[y*w+x]
}

// NewCanvas returns a blank w by h canvas. A non-positive width or height
// gives an empty canvas rather than a panic.
func NewCanvas(w, h int) *Canvas {
	if w <= 0 || h <= 0 {
		return &Canvas{}
	}
	return &Canvas{w: w, h: h, cells: make([]Cell, w*h)}
}

// Width returns the number of columns.
func (c *Canvas) Width() int { return c.w }

// Height returns the number of rows.
func (c *Canvas) Height() int { return c.h }

// inBounds reports whether (x, y) is on the canvas.
func (c *Canvas) inBounds(x, y int) bool {
	return x >= 0 && x < c.w && y >= 0 && y < c.h
}

// Set stores cell at (x, y). Positions outside the canvas are ignored, so
// callers can draw near the edges without checking.
func (c *Canvas) Set(x, y int, cell Cell) {
	if c.inBounds(x, y) {
		c.cells[y*c.w+x] = cell
	}
}

// Cell returns the cell at (x, y), or the zero Cell if it is outside the
// canvas.
func (c *Canvas) Cell(x, y int) Cell {
	if !c.inBounds(x, y) {
		return Cell{}
	}
	return c.cells[y*c.w+x]
}

// Text writes s left to right starting at (x, y) in color fg, on the default
// background. Runes that fall outside the canvas are dropped.
func (c *Canvas) Text(x, y int, s string, fg Color) {
	for _, r := range s {
		c.Set(x, y, Cell{Rune: r, Fg: fg})
		x++
	}
}

// Escape sequences used by Frame.
const (
	hideCursor = "\x1b[?25l"
	cursorHome = "\x1b[H"
	resetStyle = "\x1b[0m"
	clearBelow = "\x1b[J"
	eraseLine  = "\x1b[K"
)

// style is the colors of a cell, with invalid colors already normalized.
type style struct{ fg, bg Color }

func styleOf(cell Cell) style {
	s := style{fg: cell.Fg, bg: cell.Bg}
	if !s.fg.valid() {
		s.fg = Default
	}
	if !s.bg.valid() {
		s.bg = Default
	}
	return s
}

// appendSGR appends the escape sequence that switches to s. It always starts
// from a reset, so it does not depend on what the previous style was.
func appendSGR(b []byte, s style) []byte {
	b = append(b, "\x1b[0"...)
	if s.fg != Default {
		b = append(b, ";3"...) // Black is 1 here and ANSI 30, so subtract one
		b = strconv.AppendInt(b, int64(s.fg-1), 10)
	}
	if s.bg != Default {
		b = append(b, ";4"...)
		b = strconv.AppendInt(b, int64(s.bg-1), 10)
	}
	return append(b, 'm')
}

// Frame returns the bytes that draw the whole canvas: hide the cursor, move
// home, write every cell, and clear anything below (left over from an older,
// larger frame). It is a full redraw each time, with no screen clear, so
// there is no flicker.
//
// A style escape is written only when the style changes, and every row ends
// in the default style so colors never bleed past the line. Each row is then
// followed by an erase-to-end-of-line, so text already on the terminal to the
// right of the canvas (such as the shell prompt above a first frame) is
// wiped instead of left showing.
//
// Control characters (including ESC) are drawn as spaces. Text on a canvas
// can come from other players, and it must not be able to send escape codes
// to the terminals of everyone else.
func (c *Canvas) Frame() []byte {
	b := make([]byte, 0, len(hideCursor)+len(cursorHome)+c.w*c.h+len(clearBelow))
	b = append(b, hideCursor...)
	b = append(b, cursorHome...)

	cur := style{} // the terminal is at default styling after the previous frame
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			cell := c.cells[y*c.w+x]

			if s := styleOf(cell); s != cur {
				b = appendSGR(b, s)
				cur = s
			}

			r := cell.Rune
			if unicode.IsControl(r) { // includes the zero rune
				r = ' '
			}
			b = utf8.AppendRune(b, r)
		}
		if cur != (style{}) {
			b = append(b, resetStyle...)
			cur = style{}
		}
		b = append(b, eraseLine...) // wipe whatever was on the terminal to the right of the canvas
		if y < c.h-1 {
			b = append(b, "\r\n"...)
		}
	}

	return append(b, clearBelow...)
}
