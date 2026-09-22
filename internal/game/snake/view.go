package snake

import (
	"fmt"
	"unicode/utf8"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/render"
)

// canvasMinWidth keeps the legend of four 12-character names on one row. The
// default view is therefore 70 columns by 24 rows: it fits a standard
// 80x24 terminal, and a frame taller than the terminal would scroll and
// garble the display.
const canvasMinWidth = 70

// seatColors are the body colors, by seat: red, cyan, green, yellow. All four
// read well on a dark background. foodColor is a fifth color none of them
// use, so the pellet is never mistaken for anyone's body.
var seatColors = [maxSeats]render.Color{render.Red, render.Cyan, render.Green, render.Yellow}

const foodColor = render.Magenta
const foodGlyph = '*'

// View implements game.Game. Layout, top to bottom: a legend of the players,
// the board inside a border, and a status line. The viewer's own head is
// drawn highlighted so it is easy to find among the others. The border is
// only a visual frame — the edges themselves wrap, there is no wall to hit.
func (g *Game) View(p game.PlayerID) *render.Canvas {
	w, h := g.cfg.Width, g.cfg.Height
	c := render.NewCanvas(max(w+2, canvasMinWidth), h+4)

	viewer, seated := g.seatOf(p)

	g.drawLegend(c, viewer, seated)
	drawBorder(c, w, h)
	g.drawBoard(c)
	g.drawHeads(c, viewer, seated)
	if g.phase == phaseCountdown {
		// A big digit in the middle of the board, where nothing else is.
		c.Set(w/2+1, h/2+2, render.Cell{Rune: rune('0' + g.countdownNumber()), Fg: render.White})
	}
	c.Text(0, h+3, g.status(viewer, seated, p), render.Default)
	return c
}

// drawLegend lists the players in their colors: a block for a living snake,
// an x for one who has crashed or left, and "(you)" beside the viewer.
func (g *Game) drawLegend(c *render.Canvas, viewer int, seated bool) {
	x := 0
	for seat, s := range g.seats {
		if s == nil {
			continue
		}
		marker := "█ "
		if !s.alive {
			marker = "x "
		}
		label := marker + s.name
		if seated && seat == viewer {
			label += " (you)"
		}
		c.Text(x, 0, label, seatColors[seat])
		x += utf8.RuneCountInString(label) + 2
	}
}

// drawBorder draws the frame around the board, which sits one cell in from
// the left edge and two rows down (legend row, then the top border).
func drawBorder(c *render.Canvas, w, h int) {
	for x := 0; x <= w+1; x++ {
		edge := '-'
		if x == 0 || x == w+1 {
			edge = '+'
		}
		c.Set(x, 1, render.Cell{Rune: edge})
		c.Set(x, h+2, render.Cell{Rune: edge})
	}
	for y := 2; y <= h+1; y++ {
		c.Set(0, y, render.Cell{Rune: '|'})
		c.Set(w+1, y, render.Cell{Rune: '|'})
	}
}

// drawBoard draws every occupied cell: a body cell as a block in its owner's
// color, and the food cell (if any) as its own glyph and color.
func (g *Game) drawBoard(c *render.Canvas) {
	for y := 0; y < g.cfg.Height; y++ {
		for x := 0; x < g.cfg.Width; x++ {
			switch owner := g.grid[g.idx(x, y)]; {
			case owner == foodCell:
				c.Set(x+1, y+2, render.Cell{Rune: foodGlyph, Fg: foodColor})
			case owner != 0:
				c.Set(x+1, y+2, render.Cell{Rune: '█', Fg: seatColors[owner-1]})
			}
		}
	}
}

// drawHeads draws each head over its body: a crashed head as an x, the
// viewer's own head as a black @ on their color, and other heads as a plain
// @ in theirs.
func (g *Game) drawHeads(c *render.Canvas, viewer int, seated bool) {
	for seat, s := range g.seats {
		if s == nil {
			continue
		}
		color := seatColors[seat]
		var cell render.Cell
		switch {
		case !s.alive:
			cell = render.Cell{Rune: 'x', Fg: color}
		case seated && seat == viewer:
			cell = render.Cell{Rune: '@', Fg: render.Black, Bg: color}
		default:
			cell = render.Cell{Rune: '@', Fg: color}
		}
		h := s.body[0]
		c.Set(h.x+1, h.y+2, cell)
	}
}

// status is the line under the board.
func (g *Game) status(viewer int, seated bool, p game.PlayerID) string {
	if !seated {
		return "You are not in this game"
	}
	me := g.seats[viewer]

	switch g.phase {
	case phaseWaiting:
		return "Waiting for another player. Edges wrap around — there are no walls."
	case phaseCountdown:
		return fmt.Sprintf("Starting in %d", g.countdownNumber())
	case phasePlaying:
		if me.alive {
			return "Steer with the arrow keys or W A S D. Reach the * to grow!"
		}
		return "You crashed! Watching until the round ends (q leaves)."
	}

	switch {
	case g.outcome.Draw:
		return "It's a draw."
	case g.outcome.Winner == p && g.forfeit:
		return "Everyone else left, you win!"
	case g.outcome.Winner == p:
		return "You win!"
	}
	return "You lose"
}
