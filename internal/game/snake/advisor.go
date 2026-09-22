package snake

import (
	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

var _ game.Advisor = (*Game)(nil)

// floodFillSafeThreshold is how many reachable cells count as "plenty of
// room": a direction that clears it is not about to trap the snake, so the
// bot is free to pick among such directions by how close they bring it to
// the food instead of by exact size. It is comfortably larger than a snake
// spends most of a game at, on either board size the game ships with.
const floodFillSafeThreshold = 20

// Advise implements game.Advisor. For each direction it may turn to (never
// the reverse, which the game ignores, and always allowing its own tail,
// which the game also allows), it scores:
//
//   - certain death (a body cell that is not its own vacating tail): out of
//     the running entirely;
//   - otherwise, how many cells a flood fill can still reach from there.
//
// Because the edges wrap, a flood fill on an otherwise empty board reaches
// the same huge number in every direction — there is no wall to make one
// path shorter than another, unlike Tron. So "more open space" only matters
// once a direction is genuinely cramped (its flood fill is below
// floodFillSafeThreshold): among directions that clear it, the bot instead
// heads for whichever is closest to the food (measured the short way around,
// with wrapping); among cramped directions it falls back to the largest
// flood fill, survival first. On a tie it keeps going straight, then prefers
// Up, Down, Left, Right, so the same board always gives the same answer. If
// every direction is fatal it still answers (straight on).
//
// It does not look at where other heads are about to go, so two bots (or a
// human and a bot) can still crash head-on, and it does not plan more than
// one move ahead, so a human can still trap it. It is greedy, not
// unbeatable.
func (g *Game) Advise(p game.PlayerID) (input.Key, bool) {
	if g.phase != phaseCountdown && g.phase != phasePlaying {
		return input.Key{}, false
	}
	seat, ok := g.seatOf(p)
	if !ok || !g.seats[seat].alive {
		return input.Key{}, false
	}
	s := g.seats[seat]
	head := s.body[0]
	tail := s.body[len(s.body)-1]

	type candidate struct {
		d         input.Dir
		floodFill int
		foodDist  int
	}
	var best *candidate

	for _, d := range [...]input.Dir{s.heading, input.DirUp, input.DirDown, input.DirLeft, input.DirRight} {
		if d == opposite(s.heading) {
			continue
		}
		dx, dy := delta(d)
		nx, ny := g.wrap(head.x+dx, head.y+dy)

		fatal := g.isBody(nx, ny) && !(nx == tail.x && ny == tail.y)
		if fatal {
			continue
		}
		cand := candidate{d: d, floodFill: g.room(nx, ny), foodDist: g.wrappedDist(nx, ny, g.food)}
		if best == nil || betterDirection(g.hasFood, *best, cand) {
			best = &cand
		}
	}

	if best == nil {
		return s.heading.Key(), true // every direction is fatal: answer anyway
	}
	return best.d.Key(), true
}

// betterDirection reports whether cand should replace best. Two directions
// that both clear floodFillSafeThreshold (and there is food to head for) are
// compared by food distance; otherwise the larger flood fill wins. Either
// way ties keep the current best, so the evaluation order above (straight
// first, then Up, Down, Left, Right) decides them.
func betterDirection(hasFood bool, best, cand struct {
	d         input.Dir
	floodFill int
	foodDist  int
}) bool {
	bestRoomy := hasFood && best.floodFill >= floodFillSafeThreshold
	candRoomy := hasFood && cand.floodFill >= floodFillSafeThreshold
	switch {
	case bestRoomy && candRoomy:
		return cand.foodDist < best.foodDist
	case bestRoomy != candRoomy:
		return candRoomy
	default:
		return cand.floodFill > best.floodFill
	}
}

// wrappedDist is the Manhattan distance from (x, y) to c, the short way
// around on each axis.
func (g *Game) wrappedDist(x, y int, c cell) int {
	dx := iabs(x - c.x)
	dx = min(dx, g.cfg.Width-dx)
	dy := iabs(y - c.y)
	dy = min(dy, g.cfg.Height-dy)
	return dx + dy
}

func iabs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// room counts how many cells a flood fill can reach from (x, y), itself
// included, treating every body cell (including the mover's own) as a wall
// and wrapping at every edge. It does not know that a tail will move away,
// so it is a slight underestimate for a short snake — a simplification, like
// the rest of this heuristic.
func (g *Game) room(x, y int) int {
	seen := make([]bool, len(g.grid))
	seen[g.idx(x, y)] = true
	queue := make([]int, 1, len(g.grid))
	queue[0] = g.idx(x, y)
	count := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		count++

		cx, cy := cur%g.cfg.Width, cur/g.cfg.Width
		for _, d := range [...]input.Dir{input.DirUp, input.DirDown, input.DirLeft, input.DirRight} {
			dx, dy := delta(d)
			nx, ny := g.wrap(cx+dx, cy+dy)
			n := g.idx(nx, ny)
			if !g.isBody(nx, ny) && !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return count
}
