// Package tictactoe implements two-player Tic-Tac-Toe as a game.Game.
//
// The first player to join is X and moves first; the second is O. Players
// press 1-9 to claim a cell (numbered row by row, and shown on the board
// while the cell is empty).
package tictactoe

import (
	"fmt"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/render"
)

// mark is what a cell holds.
type mark uint8

const (
	empty mark = iota
	markX
	markO
)

func (m mark) rune() rune {
	switch m {
	case markX:
		return 'X'
	case markO:
		return 'O'
	}
	return ' '
}

func (m mark) color() render.Color {
	switch m {
	case markX:
		return render.Red
	case markO:
		return render.Cyan
	}
	return render.Default
}

// lines are the eight ways to win, as zero-based cell indexes.
var lines = [8][3]int{
	{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, // rows
	{0, 3, 6}, {1, 4, 7}, {2, 5, 8}, // columns
	{0, 4, 8}, {2, 4, 6}, // diagonals
}

// Board layout on the canvas.
const (
	canvasW = 40 // wide enough for the longest status line
	canvasH = 12
	boardX  = 2 // left edge of the board
	boardY  = 2 // top row of the board
)

// Game is one game of Tic-Tac-Toe. It is not safe for concurrent use; its
// Room is its only caller.
type Game struct {
	seats   [2]game.PlayerID // seats[0] plays X, seats[1] plays O; 0 = empty seat
	cells   [9]mark
	turn    int // index into seats of the player to move
	state   game.State
	outcome game.Outcome
	forfeit bool      // the game ended because a player left
	notice  [2]string // last rejected-move message per seat, shown by View
}

var _ game.Game = (*Game)(nil)

// New returns a game waiting for two players.
func New() *Game {
	return &Game{}
}

// Name implements game.Game.
func (g *Game) Name() string { return "tictactoe" }

// Seats implements game.Game: exactly two players.
func (g *Game) Seats() (min, max int) { return 2, 2 }

// TickEvery implements game.Game: Tic-Tac-Toe is turn-based.
func (g *Game) TickEvery() time.Duration { return 0 }

// Tick implements game.Game and does nothing.
func (g *Game) Tick() {}

// State implements game.Game.
func (g *Game) State() game.State { return g.state }

// Outcome implements game.Game.
func (g *Game) Outcome() game.Outcome { return g.outcome }

// seatOf returns p's seat index, or false if p is not in the game.
func (g *Game) seatOf(p game.PlayerID) (int, bool) {
	for i, id := range g.seats {
		if id == p && id != 0 {
			return i, true
		}
	}
	return 0, false
}

func (g *Game) clearNotices() {
	g.notice = [2]string{}
}

// Join implements game.Game. The game starts when the second player joins.
func (g *Game) Join(p game.PlayerID) error {
	if g.state == game.StateOver {
		return game.ErrOver
	}
	if _, ok := g.seatOf(p); ok {
		return game.ErrAlreadyJoined
	}
	for i, id := range g.seats {
		if id == 0 {
			g.seats[i] = p
			g.clearNotices()
			if g.seats[0] != 0 && g.seats[1] != 0 {
				g.state = game.StateRunning
				g.turn = 0
			}
			return nil
		}
	}
	return game.ErrFull
}

// Leave implements game.Game. Leaving mid-game forfeits: the other player
// wins. Leaving while waiting frees the seat. Leaving after the game is over
// changes nothing.
func (g *Game) Leave(p game.PlayerID) {
	seat, ok := g.seatOf(p)
	if !ok {
		return
	}
	switch g.state {
	case game.StateWaiting:
		g.seats[seat] = 0
	case game.StateRunning:
		g.state = game.StateOver
		g.forfeit = true
		g.outcome = game.Outcome{Winner: g.seats[1-seat]}
	}
	g.clearNotices()
}

// Input implements game.Game. Keys other than digits are ignored. A digit
// that cannot be played leaves the board alone and sets a notice for that
// player, and it does not use up their turn.
func (g *Game) Input(p game.PlayerID, k input.Key) {
	if g.state != game.StateRunning {
		return
	}
	seat, ok := g.seatOf(p)
	if !ok {
		return
	}
	n, ok := k.Digit()
	if !ok {
		return
	}

	switch {
	case n == 0:
		g.notice[seat] = "Pick a cell from 1 to 9"
		return
	case seat != g.turn:
		g.notice[seat] = "Not your turn"
		return
	case g.cells[n-1] != empty:
		g.notice[seat] = fmt.Sprintf("Cell %d is taken", n)
		return
	}

	g.cells[n-1] = markFor(seat)
	g.clearNotices()

	switch {
	case g.hasWon(markFor(seat)):
		g.state = game.StateOver
		g.outcome = game.Outcome{Winner: p}
	case g.full():
		g.state = game.StateOver
		g.outcome = game.Outcome{Draw: true}
	default:
		g.turn = 1 - g.turn
	}
}

func markFor(seat int) mark {
	if seat == 0 {
		return markX
	}
	return markO
}

func (g *Game) hasWon(m mark) bool {
	for _, l := range lines {
		if g.cells[l[0]] == m && g.cells[l[1]] == m && g.cells[l[2]] == m {
			return true
		}
	}
	return false
}

func (g *Game) full() bool {
	for _, c := range g.cells {
		if c == empty {
			return false
		}
	}
	return true
}

// View implements game.Game. Every player sees the same board with their own
// mark, status and notice. Someone who is not in the game gets a plain
// spectator screen.
func (g *Game) View(p game.PlayerID) *render.Canvas {
	c := render.NewCanvas(canvasW, canvasH)
	c.Text(boardX, 0, "Tic-Tac-Toe", render.Yellow)

	g.drawBoard(c)

	seat, seated := g.seatOf(p)
	if !seated {
		c.Text(boardX, 8, "You are not in this game", render.Default)
		return c
	}

	me := markFor(seat)
	c.Text(boardX, 8, "You are "+string(me.rune()), me.color())
	c.Text(boardX, 9, g.status(seat, p), render.Default)
	c.Text(boardX, 10, g.notice[seat], render.Yellow)
	return c
}

func (g *Game) drawBoard(c *render.Canvas) {
	for row := 0; row < 3; row++ {
		y := boardY + row*2
		for col := 0; col < 3; col++ {
			i := row*3 + col
			x := boardX + col*4 + 1
			if m := g.cells[i]; m != empty {
				c.Set(x, y, render.Cell{Rune: m.rune(), Fg: m.color()})
			} else {
				c.Set(x, y, render.Cell{Rune: rune('1' + i)})
			}
			if col < 2 {
				c.Set(x+2, y, render.Cell{Rune: '|'})
			}
		}
		if row < 2 {
			c.Text(boardX, y+1, "---+---+---", render.Default)
		}
	}
}

// status is the one-line summary of the game for the player in seat.
func (g *Game) status(seat int, p game.PlayerID) string {
	switch g.state {
	case game.StateWaiting:
		return "Waiting for an opponent..."
	case game.StateRunning:
		if seat == g.turn {
			return "Your turn"
		}
		return "Opponent's turn"
	}

	switch {
	case g.outcome.Draw:
		return "It's a draw."
	case g.outcome.Winner == p && g.forfeit:
		return "Opponent left, you win by forfeit"
	case g.outcome.Winner == p:
		return "You win!"
	}
	return "You lose"
}
