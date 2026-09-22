// Package snake implements real-time multiplayer Snake for two to four
// players as a game.Game.
//
// Every tick each living snake's head moves one cell in its heading, and the
// edges wrap: moving off one side of the board brings the head back on the
// opposite side, so there are no walls to die on. A head that lands on any
// snake's body dies — except the cell its own tail is vacating this tick,
// which is safe to move into — and two heads landing on the same cell,
// including the food cell, both die. Whoever reaches the shared food pellet
// uncontested grows by one segment, and a new pellet appears elsewhere. The
// last snake alive wins; if the last ones die on the same tick it is a draw.
//
// The package is pure, like every game: no goroutines, no clock, and the one
// piece of randomness (where food appears) comes from a *rand.Rand seeded by
// Config.Seed, an explicit constructor argument. So the same inputs — Joins,
// Inputs and Ticks, on the same seed — always give the same game, which is
// what makes it replayable.
package snake

import (
	"math/rand"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

const (
	minSeats = 2
	maxSeats = 4

	// countdownSteps is how many numbers the countdown shows: 3, 2, 1.
	countdownSteps = 3

	// The smallest board accepted. A single-cell spawn per seat is all Join
	// ever places, so (unlike a game that starts with a multi-cell body) this
	// only needs to be big enough to keep the four spawn points apart.
	minWidth  = 12
	minHeight = 10

	// foodCell marks the pellet on the grid: one more than the highest seat
	// owner value (1..maxSeats), so it can never be mistaken for a body cell.
	foodCell = maxSeats + 1
)

// Config is the tunable part of a game. Tests use small boards, a short
// countdown and a fixed seed; players get DefaultConfig with a seed chosen
// by the caller (see cmd/arena), so every room's food is placed differently.
type Config struct {
	Width, Height int           // board size in cells
	TicksPerCount int           // how many ticks each number of the 3-2-1 countdown lasts
	Tick          time.Duration // how often the room should call Tick
	Seed          int64         // seeds where food appears; the only source of randomness
}

// DefaultConfig is the standard game: a 40x20 board, a tick every 120 ms, and
// a countdown of about one second per number. Seed is left at zero (see
// sanitize); callers that want varied food placement across rooms should set
// their own.
func DefaultConfig() Config {
	return Config{Width: 40, Height: 20, TicksPerCount: 8, Tick: 120 * time.Millisecond}
}

// sanitize replaces unset (zero or negative) values with the defaults and
// raises a board that is too small to the minimum. A zero Seed is left as
// zero: it is a valid seed (not "unset"), so every all-defaults game is still
// reproducible.
func (c Config) sanitize() Config {
	def := DefaultConfig()
	if c.Width <= 0 {
		c.Width = def.Width
	}
	if c.Height <= 0 {
		c.Height = def.Height
	}
	c.Width = max(c.Width, minWidth)
	c.Height = max(c.Height, minHeight)
	if c.TicksPerCount < 1 {
		c.TicksPerCount = def.TicksPerCount
	}
	if c.Tick <= 0 {
		c.Tick = def.Tick
	}
	return c
}

// phase is where a game is in its life. It is finer than game.State: the
// room only needs to know whether to tick, so the countdown counts as
// Running, but the game must still accept new players during it.
type phase uint8

const (
	phaseWaiting   phase = iota // fewer than two players
	phaseCountdown              // 3-2-1; nobody moves yet, and players may still join
	phasePlaying                // heads are moving; joining is closed
	phaseOver                   // finished; see the outcome
)

func (p phase) String() string {
	switch p {
	case phaseWaiting:
		return "waiting"
	case phaseCountdown:
		return "countdown"
	case phasePlaying:
		return "playing"
	case phaseOver:
		return "over"
	}
	return "phase(?)"
}

// cell is one board position.
type cell struct{ x, y int }

// snake is one player's body on the board. body[0] is the head; body grows by
// one cell at the front each time it eats, and loses one cell at the back
// each move it does not.
type snake struct {
	id      game.PlayerID
	name    string
	body    []cell
	heading input.Dir // the direction of the last move
	pending input.Dir // the direction to move next tick
	alive   bool
}

// Game is one game of Snake. It is not safe for concurrent use; its Room is
// its only caller.
type Game struct {
	cfg   Config
	seats [maxSeats]*snake // fixed order, so nothing depends on map iteration; nil = free seat
	grid  []uint8          // row-major; 0 empty, 1..maxSeats a body cell, foodCell the pellet
	rng   *rand.Rand       // seeded from cfg.Seed; the only randomness in the package

	food    cell
	hasFood bool

	phase     phase
	remaining int // countdown ticks left
	outcome   game.Outcome
	forfeit   bool // the round ended because players left, not because of a crash
}

var _ game.Game = (*Game)(nil)

// New returns a game with the default settings.
func New() *Game {
	return NewWithConfig(DefaultConfig())
}

// NewWithConfig returns a game with the given settings. Unset values take
// their defaults, and a board smaller than 12x10 is enlarged to that.
func NewWithConfig(cfg Config) *Game {
	cfg = cfg.sanitize()
	return &Game{
		cfg:  cfg,
		grid: make([]uint8, cfg.Width*cfg.Height),
		rng:  rand.New(rand.NewSource(cfg.Seed)),
	}
}

// Name implements game.Game.
func (g *Game) Name() string { return "snake" }

// Seats implements game.Game: two to four players.
func (g *Game) Seats() (min, max int) { return minSeats, maxSeats }

// TickEvery implements game.Game.
func (g *Game) TickEvery() time.Duration { return g.cfg.Tick }

// State implements game.Game. The countdown counts as Running, so the room
// keeps ticking through it.
func (g *Game) State() game.State {
	switch g.phase {
	case phaseCountdown, phasePlaying:
		return game.StateRunning
	case phaseOver:
		return game.StateOver
	}
	return game.StateWaiting
}

// Outcome implements game.Game.
func (g *Game) Outcome() game.Outcome { return g.outcome }

// ---- geometry ---------------------------------------------------------------

func (g *Game) idx(x, y int) int { return y*g.cfg.Width + x }

// isBody reports whether (x, y) holds part of some snake. The food cell is
// not a body cell, even though it is marked non-zero on the grid too.
func (g *Game) isBody(x, y int) bool {
	v := g.grid[g.idx(x, y)]
	return v >= 1 && v <= maxSeats
}

// wrap brings (x, y) back onto the board: stepping off one edge arrives at
// the opposite one. Every move goes through this, so the package never has an
// inBounds check or a wall to hit.
func (g *Game) wrap(x, y int) (int, int) {
	w, h := g.cfg.Width, g.cfg.Height
	return ((x % w) + w) % w, ((y % h) + h) % h
}

// delta is the step one move in direction d makes.
func delta(d input.Dir) (dx, dy int) {
	switch d {
	case input.DirUp:
		return 0, -1
	case input.DirDown:
		return 0, 1
	case input.DirLeft:
		return -1, 0
	}
	return 1, 0 // DirRight
}

func opposite(d input.Dir) input.Dir {
	switch d {
	case input.DirUp:
		return input.DirDown
	case input.DirDown:
		return input.DirUp
	case input.DirLeft:
		return input.DirRight
	}
	return input.DirLeft
}

// spawn is where a seat starts and which way it faces: inward, from the left,
// right, top and bottom of the board in seat order. The heading only affects
// the first move (there is no wall to be "away from" once play starts, since
// edges wrap), but it still gives every seat a distinct, readable start.
func (g *Game) spawn(seat int) (x, y int, d input.Dir) {
	w, h := g.cfg.Width, g.cfg.Height
	switch seat {
	case 0:
		return w / 8, h / 2, input.DirRight
	case 1:
		return w - 1 - w/8, h / 2, input.DirLeft
	case 2:
		return w / 2, h / 8, input.DirDown
	}
	return w / 2, h - 1 - h/8, input.DirUp
}

// resetBoard clears the board and puts every seated snake back on its spawn
// point, as a single-cell body. It runs whenever the roster changes before
// play begins.
func (g *Game) resetBoard() {
	clear(g.grid)
	g.hasFood = false
	for seat, s := range g.seats {
		if s == nil {
			continue
		}
		x, y, d := g.spawn(seat)
		s.body = []cell{{x, y}}
		s.heading, s.pending = d, d
		s.alive = true
		g.grid[g.idx(x, y)] = uint8(seat + 1)
	}
}

// ---- the roster -------------------------------------------------------------

func (g *Game) seatOf(p game.PlayerID) (int, bool) {
	for i, s := range g.seats {
		if s != nil && s.id == p {
			return i, true
		}
	}
	return 0, false
}

// count is how many players are seated.
func (g *Game) count() int {
	n := 0
	for _, s := range g.seats {
		if s != nil {
			n++
		}
	}
	return n
}

// startCountdown begins (or restarts) the 3-2-1.
func (g *Game) startCountdown() {
	g.phase = phaseCountdown
	g.remaining = countdownSteps * g.cfg.TicksPerCount
}

// countdownNumber is the number the countdown is showing: 3, 2 or 1.
func (g *Game) countdownNumber() int {
	n := (g.remaining + g.cfg.TicksPerCount - 1) / g.cfg.TicksPerCount
	return min(max(n, 1), countdownSteps)
}

// Join implements game.Game. Once two players are seated a countdown starts,
// and every further player who joins restarts it, up to four players. When
// the countdown ends, play begins and joining is closed.
func (g *Game) Join(p game.PlayerID, name string) error {
	switch {
	case g.phase == phaseOver:
		return game.ErrOver
	case g.hasPlayer(p):
		return game.ErrAlreadyJoined
	case g.phase == phasePlaying:
		return game.ErrStarted
	}

	seat := -1
	for i, s := range g.seats {
		if s == nil {
			seat = i
			break
		}
	}
	if seat < 0 {
		return game.ErrFull
	}

	g.seats[seat] = &snake{id: p, name: name, alive: true}
	g.resetBoard()
	if g.count() >= minSeats {
		g.startCountdown()
	}
	return nil
}

func (g *Game) hasPlayer(p game.PlayerID) bool {
	_, ok := g.seatOf(p)
	return ok
}

// Leave implements game.Game. Before play begins the seat is freed (and the
// game goes back to waiting if fewer than two remain). During play the
// leaver's snake dies and the round continues, unless that leaves one player
// alive, who wins. A leaver's body stays on the board.
func (g *Game) Leave(p game.PlayerID) {
	seat, ok := g.seatOf(p)
	if !ok {
		return
	}

	switch g.phase {
	case phaseWaiting, phaseCountdown:
		g.seats[seat] = nil
		g.resetBoard()
		if g.count() < minSeats {
			g.phase = phaseWaiting
			g.remaining = 0
		}
	case phasePlaying:
		if s := g.seats[seat]; s.alive {
			s.alive = false
			g.checkEnd(true)
		}
	}
}

// ---- play -------------------------------------------------------------------

// Input implements game.Game. Arrow keys and W A S D steer. A key that would
// reverse the head is ignored, judged against the direction of its last move.
// If several keys arrive within one tick, the last valid one wins, and the
// turn happens on the next tick. Players can steer during the countdown.
func (g *Game) Input(p game.PlayerID, k input.Key) {
	if g.phase != phaseCountdown && g.phase != phasePlaying {
		return
	}
	seat, ok := g.seatOf(p)
	if !ok {
		return
	}
	s := g.seats[seat]
	if !s.alive {
		return
	}
	d, ok := k.Direction()
	if !ok || d == opposite(s.heading) {
		return
	}
	s.pending = d
}

// Tick implements game.Game. During the countdown it counts down and, on the
// tick that ends it, places the first food. During play it moves every
// living head one cell.
func (g *Game) Tick() {
	switch g.phase {
	case phaseCountdown:
		g.remaining--
		if g.remaining <= 0 {
			g.phase = phasePlaying
			g.spawnFood()
		}
	case phasePlaying:
		g.step()
	}
}

// spawnFood puts the pellet on a random empty cell, chosen by the game's own
// seeded generator. If the board happens to have no empty cell (every cell is
// some snake's body), it leaves hasFood false rather than looping forever;
// the next successful eat tries again.
func (g *Game) spawnFood() {
	var empty []cell
	for y := 0; y < g.cfg.Height; y++ {
		for x := 0; x < g.cfg.Width; x++ {
			if g.grid[g.idx(x, y)] == 0 {
				empty = append(empty, cell{x, y})
			}
		}
	}
	if len(empty) == 0 {
		g.hasFood = false
		return
	}
	g.food = empty[g.rng.Intn(len(empty))]
	g.hasFood = true
	g.grid[g.idx(g.food.x, g.food.y)] = foodCell
}

// headMove is one head's intended destination for this tick.
type headMove struct {
	seat    int
	x, y    int
	growing bool // this head's destination is the food cell
}

// step advances play by one tick. All moves are decided from the board as it
// was at the start of the tick, and only then applied, so no player has an
// advantage from being earlier in seat order.
//
// A snake may always follow into the cell its OWN tail is vacating this tick
// (classic Snake's "chase your own tail"), but nobody else's tail: another
// snake's body, head or tail, is a solid obstacle regardless of what that
// snake does this same tick. A cell holding the food is not an obstacle —
// only a body cell is — so reaching it is never fatal by itself; the
// same-cell check below is what makes a contested pellet fatal.
func (g *Game) step() {
	var moves []headMove
	for seat, s := range g.seats {
		if s == nil || !s.alive {
			continue
		}
		s.heading = s.pending
		dx, dy := delta(s.heading)
		nx, ny := g.wrap(s.body[0].x+dx, s.body[0].y+dy)
		growing := g.hasFood && nx == g.food.x && ny == g.food.y
		moves = append(moves, headMove{seat: seat, x: nx, y: ny, growing: growing})
	}

	var crashed [maxSeats]bool
	for _, m := range moves {
		occupied := g.isBody(m.x, m.y)
		if occupied && !m.growing {
			s := g.seats[m.seat]
			tail := s.body[len(s.body)-1]
			if tail.x == m.x && tail.y == m.y {
				occupied = false // free to follow your own vacating tail
			}
		}
		crashed[m.seat] = occupied
	}
	// Two heads entering the same cell — food or not — both die.
	for i, a := range moves {
		for _, b := range moves[i+1:] {
			if a.x == b.x && a.y == b.y {
				crashed[a.seat], crashed[b.seat] = true, true
			}
		}
	}

	ate := false
	for _, m := range moves {
		s := g.seats[m.seat]
		if crashed[m.seat] {
			s.alive = false
			continue
		}
		head := cell{m.x, m.y}
		if m.growing {
			s.body = append([]cell{head}, s.body...)
			ate = true
		} else {
			tail := s.body[len(s.body)-1]
			g.grid[g.idx(tail.x, tail.y)] = 0
			s.body = append([]cell{head}, s.body[:len(s.body)-1]...)
		}
		g.grid[g.idx(head.x, head.y)] = uint8(m.seat + 1)
	}
	if ate {
		g.spawnFood()
	}
	g.checkEnd(false)
}

// checkEnd ends the game if at most one snake is left alive. forfeit says the
// deaths just now were players leaving rather than crashing.
func (g *Game) checkEnd(forfeit bool) {
	alive := 0
	var last *snake
	for _, s := range g.seats {
		if s != nil && s.alive {
			alive++
			last = s
		}
	}

	switch alive {
	case 0:
		g.phase = phaseOver
		g.outcome = game.Outcome{Draw: true}
	case 1:
		g.phase = phaseOver
		g.outcome = game.Outcome{Winner: last.id}
		g.forfeit = forfeit
	}
}
