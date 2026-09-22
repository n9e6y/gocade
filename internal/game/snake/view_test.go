package snake

import (
	"strings"
	"testing"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/render"
)

// screen returns the text of p's view, one line per canvas row.
func screen(g *Game, p game.PlayerID) string {
	c := g.View(p)
	var sb strings.Builder
	for y := 0; y < c.Height(); y++ {
		for x := 0; x < c.Width(); x++ {
			r := c.Cell(x, y).Rune
			if r == 0 {
				r = ' '
			}
			sb.WriteRune(r)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func mustContain(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Errorf("screen does not contain %q:\n%s", want, s)
	}
}

// boardCell is the canvas cell for board position (x, y): the canvas has a
// one-cell border on the left and a legend row and a border row on top.
func boardCell(c *render.Canvas, x, y int) render.Cell { return c.Cell(x+1, y+2) }

func TestView_SizeFitsATerminal(t *testing.T) {
	t.Parallel()

	g := New()
	_ = g.Join(1, "a")
	_ = g.Join(2, "b")
	c := g.View(1)
	if c.Height() != 24 || c.Width() != 70 {
		t.Errorf("default canvas is %dx%d, want 70x24 (fits a 24-row terminal)", c.Width(), c.Height())
	}
}

func TestView_BorderAndBoard(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2)
	c := g.View(pid(0))
	w, h := g.cfg.Width, g.cfg.Height

	corners := [][2]int{{0, 1}, {w + 1, 1}, {0, h + 2}, {w + 1, h + 2}}
	for _, p := range corners {
		if got := c.Cell(p[0], p[1]).Rune; got != '+' {
			t.Errorf("border corner (%d,%d) = %q, want '+'", p[0], p[1], got)
		}
	}
	if got := c.Cell(5, 1).Rune; got != '-' {
		t.Errorf("top border = %q, want '-'", got)
	}
	if got := c.Cell(0, 5).Rune; got != '|' {
		t.Errorf("side border = %q, want '|'", got)
	}
}

func TestView_LegendShowsNamesAndWhoYouAre(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2)
	a, b := screen(g, pid(0)), screen(g, pid(1))
	mustContain(t, a, "p1 (you)")
	mustContain(t, a, "p2")
	mustContain(t, b, "p2 (you)")
	if strings.Contains(a, "p2 (you)") {
		t.Errorf("player 1's legend marks player 2 as you:\n%s", a)
	}
}

func TestView_DeadPlayersAreMarkedInTheLegend(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 3)
	kill(g, 0)
	mustContain(t, screen(g, pid(1)), "x p1")
}

// A fresh, un-fed snake is only ever one cell (it has nothing to leave behind
// when it moves), so to see a body block distinct from its head, each seat is
// given a two-segment body directly.
func TestView_EachSeatHasItsOwnColor(t *testing.T) {
	t.Parallel()

	g := newGame(t, 4)
	tails := [4][2]int{{1, 2}, {5, 2}, {9, 2}, {13, 2}}
	for seat, tail := range tails {
		arrangeBody(g, seat, input.DirRight, [2]int{tail[0] + 1, 2}, tail)
	}

	want := []render.Color{render.Red, render.Cyan, render.Green, render.Yellow}
	c := g.View(pid(0))
	for i, w := range want {
		cell := boardCell(c, tails[i][0], tails[i][1])
		if cell.Rune != '█' || cell.Fg != w {
			t.Errorf("seat %d body = %+v, want a block in color %v", i, cell, w)
		}
	}
}

func TestView_OwnHeadIsHighlighted(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.Tick()
	h := g.seats[0].body[0]

	mine := boardCell(g.View(pid(0)), h.x, h.y)
	if mine != (render.Cell{Rune: '@', Fg: render.Black, Bg: render.Red}) {
		t.Errorf("own head = %+v, want a black @ on a red background", mine)
	}
	theirs := boardCell(g.View(pid(1)), h.x, h.y)
	if theirs != (render.Cell{Rune: '@', Fg: render.Red}) {
		t.Errorf("another player's head = %+v, want a plain red @", theirs)
	}
}

func TestView_CrashedHeadIsMarked(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 3)
	markBody(g, 1, [2]int{3, 6}) // an obstacle right in front of seat 0
	g.Tick()

	for _, viewer := range []game.PlayerID{pid(0), pid(1)} {
		cell := boardCell(g.View(viewer), 2, 6)
		if cell.Rune != 'x' || cell.Fg != render.Red {
			t.Errorf("viewer %d sees the crash cell as %+v, want a red x", viewer, cell)
		}
	}
}

func TestView_FoodIsShownInItsOwnColor(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2)
	for g.phase == phaseCountdown {
		g.Tick()
	}
	cell := boardCell(g.View(pid(0)), g.food.x, g.food.y)
	if cell.Rune != '*' || cell.Fg != render.Magenta {
		t.Errorf("food cell = %+v, want a magenta *", cell)
	}
	for _, seatColor := range seatColors {
		if cell.Fg == seatColor {
			t.Error("food uses a color a seat also uses")
		}
	}
}

func TestView_CountdownDigitIsShownInTheMiddle(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2) // the countdown starts at 3
	w, h := g.cfg.Width, g.cfg.Height
	if got := boardCell(g.View(pid(0)), w/2, h/2).Rune; got != '3' {
		t.Errorf("centre shows %q, want '3'", got)
	}

	g.Tick()
	g.Tick()
	if got := boardCell(g.View(pid(0)), w/2, h/2).Rune; got != '1' {
		t.Errorf("after two ticks the centre shows %q, want '1'", got)
	}

	for g.phase == phaseCountdown {
		g.Tick()
	}
	if got := boardCell(g.View(pid(0)), w/2, h/2).Rune; got == '1' || got == '2' || got == '3' {
		t.Errorf("a digit is still shown once play has started: %q", got)
	}
}

func TestView_StatusLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  func(t *testing.T) *Game
		viewer game.PlayerID
		want   string
	}{
		{"waiting for a second player", func(t *testing.T) *Game { return newGame(t, 1) }, pid(0), "wrap"},
		{"countdown", func(t *testing.T) *Game { return newGame(t, 2) }, pid(0), "Starting in 3"},
		{"playing", func(t *testing.T) *Game { return newPlaying(t, 2) }, pid(0), "Steer with the arrow keys or W A S D"},
		{"you crashed while others play on", func(t *testing.T) *Game {
			g := newPlaying(t, 3)
			markBody(g, 1, [2]int{3, 6})
			g.Tick()
			return g
		}, pid(0), "You crashed"},
		{"a survivor of a crash sees the game go on", func(t *testing.T) *Game {
			g := newPlaying(t, 3)
			markBody(g, 1, [2]int{3, 6})
			g.Tick()
			return g
		}, pid(1), "Steer with the arrow keys or W A S D"},
		{"winner", func(t *testing.T) *Game {
			g := newPlaying(t, 2)
			markBody(g, 1, [2]int{3, 6})
			g.Tick()
			return g
		}, pid(1), "You win!"},
		{"loser", func(t *testing.T) *Game {
			g := newPlaying(t, 2)
			markBody(g, 1, [2]int{3, 6})
			g.Tick()
			return g
		}, pid(0), "You lose"},
		{"draw", func(t *testing.T) *Game {
			g := newPlaying(t, 2)
			arrange(g, 0, 5, 5, input.DirRight)
			arrange(g, 1, 7, 5, input.DirLeft)
			g.Tick()
			return g
		}, pid(0), "It's a draw."},
		{"win by forfeit", func(t *testing.T) *Game {
			g := newPlaying(t, 2)
			g.Leave(pid(0))
			return g
		}, pid(1), "Everyone else left, you win!"},
		{"not in the game", func(t *testing.T) *Game { return newPlaying(t, 2) }, 99, "You are not in this game"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mustContain(t, screen(tt.setup(t), tt.viewer), tt.want)
		})
	}
}
