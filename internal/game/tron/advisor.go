package tron

import (
	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

var _ game.Advisor = (*Game)(nil)

// dead is the score of a direction that runs straight into a wall or a trail.
const dead = -1

// Advise implements game.Advisor. The bot looks at each direction it may turn
// to (never the reverse, which the game ignores) and scores it:
//
//   - a step into a wall or any trail or head scores -1: certain death;
//   - any other step scores how many empty cells can still be reached from the
//     cell it lands on (a flood fill), so the bot heads for open room and
//     avoids boxing itself in.
//
// It picks the highest score. On a tie it keeps going straight, then prefers
// Up, Down, Left, Right, so the same board always gives the same answer. If
// every direction is fatal it still answers (straight on): a bot that gives
// up would look broken.
//
// It does not look at where other heads are about to go, so two bots (or a
// human and a bot) can still crash head-on. It can also be trapped by a
// human who cuts off its room: it is greedy, not unbeatable.
func (g *Game) Advise(p game.PlayerID) (input.Key, bool) {
	if g.phase != phaseCountdown && g.phase != phasePlaying {
		return input.Key{}, false
	}
	seat, ok := g.seatOf(p)
	if !ok || !g.seats[seat].alive {
		return input.Key{}, false
	}
	s := g.seats[seat]

	best, bestScore := s.heading, dead-1
	// Straight ahead comes first, so on equal scores (strictly greater below)
	// the bot keeps its line.
	for _, d := range [...]input.Dir{s.heading, input.DirUp, input.DirDown, input.DirLeft, input.DirRight} {
		if d == opposite(s.heading) {
			continue
		}
		dx, dy := delta(d)
		if score := g.room(s.x+dx, s.y+dy); score > bestScore {
			best, bestScore = d, score
		}
	}
	return best.Key(), true
}

// room scores stepping onto (x, y): dead if it is off the board or occupied,
// otherwise the number of empty cells reachable from it (itself included).
func (g *Game) room(x, y int) int {
	if !g.inBounds(x, y) || g.grid[g.idx(x, y)] != 0 {
		return dead
	}

	seen := make([]bool, len(g.grid))
	seen[g.idx(x, y)] = true
	queue := make([]int, 1, len(g.grid)) // room for every cell, so it never has to grow
	queue[0] = g.idx(x, y)
	count := 0
	for len(queue) > 0 {
		cell := queue[0]
		queue = queue[1:]
		count++

		cx, cy := cell%g.cfg.Width, cell/g.cfg.Width
		for _, d := range [...]input.Dir{input.DirUp, input.DirDown, input.DirLeft, input.DirRight} {
			dx, dy := delta(d)
			nx, ny := cx+dx, cy+dy
			if !g.inBounds(nx, ny) {
				continue
			}
			n := g.idx(nx, ny)
			if g.grid[n] == 0 && !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return count
}
