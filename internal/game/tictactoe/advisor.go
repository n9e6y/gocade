package tictactoe

import (
	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

var _ game.Advisor = (*Game)(nil)

// Advise implements game.Advisor with minimax: it looks at every way the rest
// of the game could go and picks the cell with the best guaranteed result, so
// the bot never loses. A quicker win scores higher than a slower one, and
// among equal cells the lowest number wins, so the answer is always the same
// for the same board.
func (g *Game) Advise(p game.PlayerID) (input.Key, bool) {
	seat, ok := g.seatOf(p)
	if !ok || g.state != game.StateRunning || seat != g.turn {
		return input.Key{}, false
	}
	me := markFor(seat)

	bestCell, bestScore := -1, -winScore-1
	for i := range g.cells {
		if g.cells[i] != empty {
			continue
		}
		board := g.cells // a copy: the game itself is never touched
		board[i] = me
		score := scoreAfter(board, me, 0)
		if score > bestScore { // strictly greater keeps the lowest cell on ties
			bestCell, bestScore = i, score
		}
	}
	if bestCell < 0 {
		return input.Key{}, false // no empty cell; the game would already be over
	}
	return input.Key{Kind: input.KindRune, Rune: rune('1' + bestCell)}, true
}

// winScore is the score of winning on the spot; each move later lowers it by
// one, so faster wins are preferred.
const winScore = 10

// scoreAfter scores a board, for the player who has just moved (mover), right
// after that move. depth is how many moves ahead of the real game we are.
// Positive means mover wins, zero a draw, negative a loss.
func scoreAfter(board [9]mark, mover mark, depth int) int {
	if won(&board, mover) {
		return winScore - depth
	}

	// The other player moves next. Their best result is our worst.
	other := markO
	if mover == markO {
		other = markX
	}
	best, moved := -winScore-1, false
	for i := range board {
		if board[i] != empty {
			continue
		}
		moved = true
		board[i] = other
		best = max(best, scoreAfter(board, other, depth+1))
		board[i] = empty
	}
	if !moved {
		return 0 // the board is full: a draw
	}
	return -best
}
