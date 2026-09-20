// Package tron implements real-time Tron for two to four players as a
// game.Game.
//
// Every tick each living head moves one cell forward and leaves a trail. A
// head that enters a wall, any trail, or another head dies, and two heads
// entering the same cell both die. The last player alive wins; if the last
// players die on the same tick it is a draw.
//
// The package is pure, like every game: no goroutines, no clock, no
// randomness. The room calls Tick at a fixed rate, so the same inputs on the
// same ticks always give the same game, which is what makes it replayable.
package tron

import (
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

const (
	minSeats = 2
	maxSeats = 4

	// countdownSteps is how many numbers the countdown shows: 3, 2, 1.
	countdownSteps = 3

	// The smallest board we accept. Smaller boards leave no room for the
	// four spawn points to face inward.
	minWidth  = 12
	minHeight = 10
)

// Config is the tunable part of a game. Tests use small boards and a short
// countdown; players get DefaultConfig.
type Config struct {
	Width, Height int           // board size in cells
	TicksPerCount int           // how many ticks each number of the 3-2-1 countdown lasts
	Tick          time.Duration // how often the room should call Tick
}

// DefaultConfig is the standard game: a 40x20 board, a tick every 120 ms, and
// a countdown of about one second per number.
func DefaultConfig() Config {
	return Config{Width: 40, Height: 20, TicksPerCount: 8, Tick: 120 * time.Millisecond}
}

// sanitize replaces unset (zero or negative) values with the defaults and
// raises a board that is too small to the minimum.
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

// snake is one player's head on the board.
type snake struct {
	id      game.PlayerID
	name    string
	x, y    int
	heading input.Dir // the direction of the last move
	pending input.Dir // the direction to move next tick
	alive   bool
}

// Game is one game of Tron. It is not safe for concurrent use; its Room is
// its only caller.
type Game struct {
	cfg   Config
	seats [maxSeats]*snake // fixed order, so nothing depends on map iteration; nil = free seat
	grid  []uint8          // row-major; 0 is empty, otherwise the owning seat + 1

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
	return &Game{cfg: cfg, grid: make([]uint8, cfg.Width*cfg.Height)}
}

// Name implements game.Game.
func (g *Game) Name() string { return "tron" }

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

func (g *Game) inBounds(x, y int) bool {
	return x >= 0 && x < g.cfg.Width && y >= 0 && y < g.cfg.Height
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
// right, top and bottom of the board in seat order.
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
// point. It runs whenever the roster changes before play begins.
func (g *Game) resetBoard() {
	clear(g.grid)
	for seat, s := range g.seats {
		if s == nil {
			continue
		}
		s.x, s.y, s.heading = g.spawn(seat)
		s.pending = s.heading
		s.alive = true
		g.grid[g.idx(s.x, s.y)] = uint8(seat + 1)
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
// leaver's snake dies and the round continues, unless that leaves one
// player alive, who wins. A leaver's trail stays on the board.
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

// Tick implements game.Game. During the countdown it counts down; during
// play it moves every living head one cell.
func (g *Game) Tick() {
	switch g.phase {
	case phaseCountdown:
		g.remaining--
		if g.remaining <= 0 {
			g.phase = phasePlaying
		}
	case phasePlaying:
		g.step()
	}
}

// move is one head's intended destination for this tick.
type move struct {
	seat int
	x, y int
}

// step advances play by one tick. All moves are decided from the board as it
// was at the start of the tick, and only then applied, so no player has an
// advantage from being earlier in seat order.
func (g *Game) step() {
	var moves []move
	for seat, s := range g.seats {
		if s == nil || !s.alive {
			continue
		}
		s.heading = s.pending
		dx, dy := delta(s.heading)
		moves = append(moves, move{seat: seat, x: s.x + dx, y: s.y + dy})
	}

	var crashed [maxSeats]bool
	for _, m := range moves {
		// The board holds every trail and every head, so this catches walls,
		// trails, and a head running into another head (or swapping places).
		if !g.inBounds(m.x, m.y) || g.grid[g.idx(m.x, m.y)] != 0 {
			crashed[m.seat] = true
		}
	}
	// Two heads entering the same empty cell both die.
	for i, a := range moves {
		for _, b := range moves[i+1:] {
			if a.x == b.x && a.y == b.y {
				crashed[a.seat], crashed[b.seat] = true, true
			}
		}
	}

	for _, m := range moves {
		s := g.seats[m.seat]
		if crashed[m.seat] {
			s.alive = false
			continue
		}
		s.x, s.y = m.x, m.y
		g.grid[g.idx(m.x, m.y)] = uint8(m.seat + 1)
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
