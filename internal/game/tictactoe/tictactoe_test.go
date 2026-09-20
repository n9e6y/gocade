package tictactoe

import (
	"errors"
	"strings"
	"testing"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/render"
)

const (
	px game.PlayerID = 1 // joins first, plays X, moves first
	po game.PlayerID = 2 // joins second, plays O
)

// winLines are the 8 winning lines, as cell numbers 1-9 (row-major).
var winLines = [][3]int{
	{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, // rows
	{1, 4, 7}, {2, 5, 8}, {3, 6, 9}, // columns
	{1, 5, 9}, {3, 5, 7}, // diagonals
}

func isWinLine(cells [3]int) bool {
	for _, l := range winLines {
		if sameSet(l, cells) {
			return true
		}
	}
	return false
}

func sameSet(a, b [3]int) bool {
	for _, x := range a {
		found := false
		for _, y := range b {
			if x == y {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func digit(n int) input.Key {
	return input.Key{Kind: input.KindRune, Rune: rune('0' + n)}
}

// newStarted returns a game with both players seated, so X is to move.
func newStarted(t *testing.T) *Game {
	t.Helper()
	g := New()
	if err := g.Join(px); err != nil {
		t.Fatalf("Join(px): %v", err)
	}
	if err := g.Join(po); err != nil {
		t.Fatalf("Join(po): %v", err)
	}
	return g
}

type move struct {
	p    game.PlayerID
	cell int
}

func play(g *Game, moves ...move) {
	for _, m := range moves {
		g.Input(m.p, digit(m.cell))
	}
}

// screen returns the text of p's view, one line per row.
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

func TestMeta(t *testing.T) {
	t.Parallel()

	g := New()
	if g.Name() != "tictactoe" {
		t.Errorf("Name() = %q", g.Name())
	}
	if lo, hi := g.Seats(); lo != 2 || hi != 2 {
		t.Errorf("Seats() = (%d, %d), want (2, 2)", lo, hi)
	}
	if g.TickEvery() != 0 {
		t.Errorf("TickEvery() = %v, want 0 (turn-based)", g.TickEvery())
	}
	if g.State() != game.StateWaiting {
		t.Errorf("new game State() = %v, want Waiting", g.State())
	}
}

func TestStartsWhenSecondPlayerJoins(t *testing.T) {
	t.Parallel()

	g := New()
	if err := g.Join(px); err != nil {
		t.Fatal(err)
	}
	if g.State() != game.StateWaiting {
		t.Fatalf("after one player State() = %v, want Waiting", g.State())
	}
	if err := g.Join(po); err != nil {
		t.Fatal(err)
	}
	if g.State() != game.StateRunning {
		t.Errorf("after two players State() = %v, want Running", g.State())
	}
}

func TestWinningLines(t *testing.T) {
	t.Parallel()

	for _, line := range winLines {
		// X wins: X takes the line; O plays the first two cells not on it.
		t.Run("X wins "+lineName(line), func(t *testing.T) {
			t.Parallel()

			others := cellsNotIn(line, 2)
			seq := []move{
				{px, line[0]}, {po, others[0]},
				{px, line[1]}, {po, others[1]},
			}
			g := newStarted(t)
			play(g, seq...)
			if g.State() != game.StateRunning {
				t.Fatalf("game ended early: State() = %v", g.State())
			}
			play(g, move{px, line[2]})
			assertWinner(t, g, px)
		})

		// O wins: X plays three cells off the line that are not themselves a
		// line; O takes the line and completes it on the sixth move.
		t.Run("O wins "+lineName(line), func(t *testing.T) {
			t.Parallel()

			xs := safeCellsNotIn(t, line)
			g := newStarted(t)
			play(g,
				move{px, xs[0]}, move{po, line[0]},
				move{px, xs[1]}, move{po, line[1]},
				move{px, xs[2]},
			)
			if g.State() != game.StateRunning {
				t.Fatalf("game ended early: State() = %v", g.State())
			}
			play(g, move{po, line[2]})
			assertWinner(t, g, po)
		})
	}
}

func lineName(l [3]int) string {
	return string(rune('0'+l[0])) + string(rune('0'+l[1])) + string(rune('0'+l[2]))
}

// cellsNotIn returns the first n cells (1-9) that are not on line.
func cellsNotIn(line [3]int, n int) []int {
	var out []int
	for c := 1; c <= 9 && len(out) < n; c++ {
		if c != line[0] && c != line[1] && c != line[2] {
			out = append(out, c)
		}
	}
	return out
}

// safeCellsNotIn returns three cells off line that do not form a winning
// line, so X can play them without winning.
func safeCellsNotIn(t *testing.T, line [3]int) [3]int {
	t.Helper()
	rest := cellsNotIn(line, 6)
	for i := 0; i < len(rest); i++ {
		for j := i + 1; j < len(rest); j++ {
			for k := j + 1; k < len(rest); k++ {
				c := [3]int{rest[i], rest[j], rest[k]}
				if !isWinLine(c) {
					return c
				}
			}
		}
	}
	t.Fatalf("no safe cells off line %v", line)
	return [3]int{}
}

func assertWinner(t *testing.T, g *Game, want game.PlayerID) {
	t.Helper()
	if g.State() != game.StateOver {
		t.Fatalf("State() = %v, want Over", g.State())
	}
	if out := g.Outcome(); out.Draw || out.Winner != want {
		t.Errorf("Outcome() = %+v, want winner %d", out, want)
	}
}

func TestDraw(t *testing.T) {
	t.Parallel()

	// Final board:  X O X
	//               X O O
	//               O X X
	g := newStarted(t)
	seq := []move{
		{px, 1}, {po, 2}, {px, 3}, {po, 5}, {px, 4}, {po, 6}, {px, 8}, {po, 7},
	}
	play(g, seq...)
	if g.State() != game.StateRunning {
		t.Fatalf("game ended early: State() = %v", g.State())
	}
	play(g, move{px, 9})

	if g.State() != game.StateOver {
		t.Fatalf("State() = %v, want Over", g.State())
	}
	if out := g.Outcome(); !out.Draw || out.Winner != 0 {
		t.Errorf("Outcome() = %+v, want a draw with no winner", out)
	}
	mustContain(t, screen(g, px), "draw")
	mustContain(t, screen(g, po), "draw")
}

func TestRejectedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      []move
		who        game.PlayerID
		key        input.Key
		wantNotice string // shown to who; empty means no notice expected
		wantCells  [9]mark
	}{
		{
			name: "O moves before X", who: po, key: digit(5),
			wantNotice: "Not your turn",
		},
		{
			name: "X moves twice in a row", setup: []move{{px, 5}}, who: px, key: digit(1),
			wantNotice: "Not your turn",
			wantCells:  [9]mark{4: markX},
		},
		{
			name: "occupied cell", setup: []move{{px, 5}}, who: po, key: digit(5),
			wantNotice: "Cell 5 is taken",
			wantCells:  [9]mark{4: markX},
		},
		{
			name: "zero is not a cell", who: px, key: digit(0),
			wantNotice: "Pick a cell from 1 to 9",
		},
		{
			name: "letter is ignored", who: px, key: input.Key{Kind: input.KindRune, Rune: 'x'},
		},
		{
			name: "enter is ignored", who: px, key: input.Key{Kind: input.KindEnter},
		},
		{
			name: "arrow is ignored", who: px, key: input.Key{Kind: input.KindUp},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newStarted(t)
			play(g, tt.setup...)
			g.Input(tt.who, tt.key)

			if g.cells != tt.wantCells {
				t.Errorf("cells = %v, want %v", g.cells, tt.wantCells)
			}
			if g.State() != game.StateRunning {
				t.Errorf("State() = %v, want Running", g.State())
			}
			if tt.wantNotice != "" {
				mustContain(t, screen(g, tt.who), tt.wantNotice)
			}
		})
	}
}

func TestRejectedMoveKeepsTheTurn(t *testing.T) {
	t.Parallel()

	g := newStarted(t)
	play(g, move{px, 5}, move{po, 5}) // O picks X's cell
	mustContain(t, screen(g, po), "Your turn")

	play(g, move{po, 1}) // O may still move
	if g.cells[0] != markO {
		t.Errorf("cell 1 = %v, want O after the retry", g.cells[0])
	}
	if strings.Contains(screen(g, po), "taken") {
		t.Errorf("notice should clear after a valid move:\n%s", screen(g, po))
	}
}

func TestInputWhileWaitingIsIgnored(t *testing.T) {
	t.Parallel()

	g := New()
	if err := g.Join(px); err != nil {
		t.Fatal(err)
	}
	play(g, move{px, 5})
	if g.cells != ([9]mark{}) {
		t.Errorf("cells = %v, want an empty board", g.cells)
	}
	mustContain(t, screen(g, px), "Waiting for an opponent")

	// The premature key must not have been remembered once play begins.
	if err := g.Join(po); err != nil {
		t.Fatal(err)
	}
	if g.cells != ([9]mark{}) {
		t.Errorf("cells after start = %v, want an empty board", g.cells)
	}
}

func TestInputAfterOverIsIgnored(t *testing.T) {
	t.Parallel()

	g := newStarted(t)
	play(g, move{px, 1}, move{po, 4}, move{px, 2}, move{po, 5}, move{px, 3}) // X wins on the top row
	before := g.cells
	play(g, move{po, 9}, move{px, 8})

	if g.cells != before {
		t.Errorf("board changed after game over: %v -> %v", before, g.cells)
	}
	assertWinner(t, g, px)
}

func TestInputFromStrangerIsIgnored(t *testing.T) {
	t.Parallel()

	g := newStarted(t)
	play(g, move{99, 5})
	if g.cells != ([9]mark{}) {
		t.Errorf("stranger changed the board: %v", g.cells)
	}
}

func TestJoinErrors(t *testing.T) {
	t.Parallel()

	t.Run("duplicate while waiting", func(t *testing.T) {
		t.Parallel()
		g := New()
		_ = g.Join(px)
		if err := g.Join(px); !errors.Is(err, game.ErrAlreadyJoined) {
			t.Errorf("err = %v, want ErrAlreadyJoined", err)
		}
	})

	t.Run("duplicate while running", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		if err := g.Join(po); !errors.Is(err, game.ErrAlreadyJoined) {
			t.Errorf("err = %v, want ErrAlreadyJoined", err)
		}
	})

	t.Run("third player", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		if err := g.Join(3); !errors.Is(err, game.ErrFull) {
			t.Errorf("err = %v, want ErrFull", err)
		}
	})

	t.Run("after game over", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		g.Leave(po) // forfeit ends the game
		if err := g.Join(3); !errors.Is(err, game.ErrOver) {
			t.Errorf("err = %v, want ErrOver", err)
		}
	})
}

func TestLeave(t *testing.T) {
	t.Parallel()

	t.Run("mid-game leaver forfeits", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		play(g, move{px, 5})
		g.Leave(px)

		assertWinner(t, g, po)
		mustContain(t, screen(g, po), "forfeit")
	})

	t.Run("O leaving also forfeits", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		g.Leave(po)
		assertWinner(t, g, px)
	})

	t.Run("leaving while waiting frees the seat", func(t *testing.T) {
		t.Parallel()
		g := New()
		_ = g.Join(px)
		g.Leave(px)
		if g.State() != game.StateWaiting {
			t.Fatalf("State() = %v, want Waiting", g.State())
		}
		if err := g.Join(3); err != nil {
			t.Fatalf("Join after seat freed: %v", err)
		}
		mustContain(t, screen(g, 3), "You are X")
	})

	t.Run("leaving after over changes nothing", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		play(g, move{px, 1}, move{po, 4}, move{px, 2}, move{po, 5}, move{px, 3}) // X wins
		g.Leave(px)
		g.Leave(po)
		assertWinner(t, g, px)
	})

	t.Run("stranger leaving is a no-op", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		g.Leave(99)
		if g.State() != game.StateRunning {
			t.Errorf("State() = %v, want Running", g.State())
		}
	})
}

func TestTickDoesNothing(t *testing.T) {
	t.Parallel()

	g := newStarted(t)
	play(g, move{px, 5})
	before := g.cells
	g.Tick()
	if g.cells != before || g.State() != game.StateRunning {
		t.Errorf("Tick changed a turn-based game")
	}
}

func TestView(t *testing.T) {
	t.Parallel()

	t.Run("each player sees their own mark and turn", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)

		x, o := screen(g, px), screen(g, po)
		mustContain(t, x, "You are X")
		mustContain(t, x, "Your turn")
		mustContain(t, o, "You are O")
		mustContain(t, o, "Opponent's turn")
	})

	t.Run("empty cells show their number, taken cells show the mark", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		play(g, move{px, 5})

		s := screen(g, po)
		mustContain(t, s, " 4 | X | 6 ")
		mustContain(t, s, " 1 | 2 | 3 ")
	})

	t.Run("winner and loser see different results", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		play(g, move{px, 1}, move{po, 4}, move{px, 2}, move{po, 5}, move{px, 3})

		mustContain(t, screen(g, px), "You win!")
		mustContain(t, screen(g, po), "You lose")
	})

	t.Run("marks are colored", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		play(g, move{px, 1}, move{po, 2})

		c := g.View(px)
		if got := findInBoard(c, 'X'); got.Fg != render.Red {
			t.Errorf("X color = %v, want Red", got.Fg)
		}
		if got := findInBoard(c, 'O'); got.Fg != render.Cyan {
			t.Errorf("O color = %v, want Cyan", got.Fg)
		}
	})

	t.Run("a stranger gets a canvas, not a panic", func(t *testing.T) {
		t.Parallel()
		g := newStarted(t)
		if g.View(99) == nil {
			t.Error("View(99) = nil")
		}
	})
}

// findInBoard returns the first cell holding r within the board rows (y 2-6).
func findInBoard(c *render.Canvas, r rune) render.Cell {
	for y := 2; y <= 6; y++ {
		for x := 0; x < c.Width(); x++ {
			if cell := c.Cell(x, y); cell.Rune == r {
				return cell
			}
		}
	}
	return render.Cell{}
}
