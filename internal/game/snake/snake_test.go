package snake

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// testCfg is a small board (20x12) with a countdown of one tick and a fixed
// seed, so scenarios stay short, readable and reproducible. Seat 0 spawns at
// (2,6), seat 1 at (17,6), seat 2 at (10,1), seat 3 at (10,10) — same
// quarter-position layout as Tron.
func testCfg() Config {
	return Config{Width: 20, Height: 12, TicksPerCount: 1, Tick: time.Millisecond, Seed: 7}
}

// pid is the player id used for a seat: seat 0 is player 1, and so on.
func pid(seat int) game.PlayerID { return game.PlayerID(seat + 1) }

func newGame(t *testing.T, players int) *Game {
	t.Helper()
	return newGameCfg(t, testCfg(), players)
}

func newGameCfg(t *testing.T, cfg Config, players int) *Game {
	t.Helper()
	g := NewWithConfig(cfg)
	for i := 0; i < players; i++ {
		if err := g.Join(pid(i), fmt.Sprintf("p%d", i+1)); err != nil {
			t.Fatalf("Join(seat %d): %v", i, err)
		}
	}
	return g
}

// newPlaying returns a game whose countdown has finished: the next Tick moves
// the heads. Food has already been placed, since Tick spawns it the moment
// play begins.
func newPlaying(t *testing.T, players int) *Game {
	t.Helper()
	g := newGame(t, players)
	for g.phase == phaseCountdown {
		g.Tick()
	}
	if g.phase != phasePlaying {
		t.Fatalf("phase = %v after the countdown, want playing", g.phase)
	}
	return g
}

// clearSeat erases every cell a seat's body occupies, so a test can place it
// somewhere else.
func clearSeat(g *Game, seat int) {
	for i := range g.grid {
		if g.grid[i] == uint8(seat+1) {
			g.grid[i] = 0
		}
	}
}

// arrange moves a seat to a single-cell body at (x, y) heading d, erasing its
// old body. Most tests only care where the head is; arrangeBody below is for
// the ones that need an exact multi-cell shape.
func arrange(g *Game, seat, x, y int, d input.Dir) {
	arrangeBody(g, seat, d, [2]int{x, y})
}

// arrangeBody sets a seat's whole body explicitly, head first, erasing its old
// one. It is how tests set up tail-follow and self-collision situations.
func arrangeBody(g *Game, seat int, d input.Dir, cells ...[2]int) {
	clearSeat(g, seat)
	s := g.seats[seat]
	s.heading, s.pending = d, d
	s.alive = true
	s.body = make([]cell, len(cells))
	for i, c := range cells {
		s.body[i] = cell{c[0], c[1]}
	}
	for _, c := range s.body {
		g.grid[g.idx(c.x, c.y)] = uint8(seat + 1)
	}
}

// markBody makes the given cells belong to a seat on the grid, without
// changing that seat's own body list: a static obstacle for testing another
// seat's collisions, the way a permanent Tron trail would.
func markBody(g *Game, seat int, cells ...[2]int) {
	for _, c := range cells {
		g.grid[g.idx(c[0], c[1])] = uint8(seat + 1)
	}
}

// kill makes a seat's snake dead, as if it had crashed earlier; its body
// stays on the board.
func kill(g *Game, seat int) { g.seats[seat].alive = false }

// setFood places the pellet at an exact cell, bypassing the RNG, so tests can
// set up an exact food situation.
func setFood(g *Game, x, y int) {
	if g.hasFood {
		g.grid[g.idx(g.food.x, g.food.y)] = 0
	}
	g.food = cell{x, y}
	g.hasFood = true
	g.grid[g.idx(x, y)] = foodCell
}

func arrow(d input.Dir) input.Key {
	switch d {
	case input.DirUp:
		return input.Key{Kind: input.KindUp}
	case input.DirDown:
		return input.Key{Kind: input.KindDown}
	case input.DirLeft:
		return input.Key{Kind: input.KindLeft}
	}
	return input.Key{Kind: input.KindRight}
}

func letter(c rune) input.Key { return input.Key{Kind: input.KindRune, Rune: c} }

func wantOver(t *testing.T, g *Game, winner game.PlayerID, draw bool) {
	t.Helper()
	if g.State() != game.StateOver {
		t.Fatalf("State() = %v, want Over", g.State())
	}
	if out := g.Outcome(); out.Winner != winner || out.Draw != draw {
		t.Errorf("Outcome() = %+v, want winner %d, draw %v", out, winner, draw)
	}
}

// ---- meta -------------------------------------------------------------------

func TestMeta(t *testing.T) {
	t.Parallel()

	g := New()
	if g.Name() != "snake" {
		t.Errorf("Name() = %q, want snake", g.Name())
	}
	if lo, hi := g.Seats(); lo != 2 || hi != 4 {
		t.Errorf("Seats() = (%d, %d), want (2, 4)", lo, hi)
	}
	if g.TickEvery() != 120*time.Millisecond {
		t.Errorf("TickEvery() = %v, want 120ms", g.TickEvery())
	}
	if g.State() != game.StateWaiting {
		t.Errorf("new game State() = %v, want Waiting", g.State())
	}
	cfg := DefaultConfig()
	if cfg.Width != 40 || cfg.Height != 20 {
		t.Errorf("default board = %dx%d, want 40x20", cfg.Width, cfg.Height)
	}
}

func TestConfigIsSanitized(t *testing.T) {
	t.Parallel()

	g := NewWithConfig(Config{Width: 3, Height: -1, TicksPerCount: 0, Tick: 0})
	if g.cfg.Width < minWidth || g.cfg.Height < minHeight {
		t.Errorf("board = %dx%d, want at least %dx%d", g.cfg.Width, g.cfg.Height, minWidth, minHeight)
	}
	if g.cfg.TicksPerCount < 1 {
		t.Errorf("TicksPerCount = %d, want at least 1", g.cfg.TicksPerCount)
	}
	if g.cfg.Tick <= 0 {
		t.Errorf("Tick = %v, want a positive interval", g.cfg.Tick)
	}
}

// Every seat starts on the board, apart from every other seat, and every
// board size the game accepts is checked, including the smallest.
func TestSpawnsAreDistinct(t *testing.T) {
	t.Parallel()

	for _, cfg := range []Config{testCfg(), DefaultConfig(), {Width: minWidth, Height: minHeight, TicksPerCount: 1, Tick: time.Millisecond}} {
		g := newGameCfg(t, cfg, 4)
		seen := map[[2]int]int{}
		for seat, s := range g.seats {
			if len(s.body) != 1 {
				t.Fatalf("seat %d spawns with %d segments, want 1", seat, len(s.body))
			}
			h := s.body[0]
			if h.x < 0 || h.x >= cfg.Width || h.y < 0 || h.y >= cfg.Height {
				t.Fatalf("%dx%d seat %d spawns off the board at (%d,%d)", cfg.Width, cfg.Height, seat, h.x, h.y)
			}
			if other, dup := seen[[2]int{h.x, h.y}]; dup {
				t.Fatalf("%dx%d seats %d and %d share the spawn (%d,%d)", cfg.Width, cfg.Height, other, seat, h.x, h.y)
			}
			seen[[2]int{h.x, h.y}] = seat
		}
	}
}

// ---- the start rule -----------------------------------------------------------

func TestStart_WaitsForTwoPlayers(t *testing.T) {
	t.Parallel()

	g := newGame(t, 1)
	if g.State() != game.StateWaiting {
		t.Fatalf("one player: State() = %v, want Waiting", g.State())
	}
	g.Tick() // ticks are ignored while waiting
	if g.State() != game.StateWaiting || g.phase != phaseWaiting {
		t.Errorf("a tick changed a waiting game: %v / %v", g.State(), g.phase)
	}

	if err := g.Join(pid(1), "p2"); err != nil {
		t.Fatal(err)
	}
	if g.State() != game.StateRunning || g.phase != phaseCountdown {
		t.Errorf("two players: State() = %v, phase = %v, want Running / countdown", g.State(), g.phase)
	}
}

func TestStart_EachJoinerRestartsTheCountdown(t *testing.T) {
	t.Parallel()

	cfg := testCfg()
	cfg.TicksPerCount = 2 // a countdown of six ticks
	g := newGameCfg(t, cfg, 2)

	for i := 0; i < 4; i++ {
		g.Tick()
	}
	if g.remaining != 2 {
		t.Fatalf("remaining = %d after four ticks, want 2", g.remaining)
	}

	if err := g.Join(pid(2), "p3"); err != nil {
		t.Fatal(err)
	}
	if g.remaining != 3*cfg.TicksPerCount || g.phase != phaseCountdown {
		t.Errorf("after a third player joined: remaining = %d, phase = %v, want a fresh countdown", g.remaining, g.phase)
	}
}

func TestStart_JoinRules(t *testing.T) {
	t.Parallel()

	t.Run("a fifth player during the countdown is refused", func(t *testing.T) {
		t.Parallel()
		g := newGame(t, 4)
		if err := g.Join(99, "late"); !errors.Is(err, game.ErrFull) {
			t.Errorf("err = %v, want ErrFull", err)
		}
	})

	t.Run("a duplicate is refused while waiting and during the countdown", func(t *testing.T) {
		t.Parallel()
		g := newGame(t, 1)
		if err := g.Join(pid(0), "again"); !errors.Is(err, game.ErrAlreadyJoined) {
			t.Errorf("waiting: err = %v, want ErrAlreadyJoined", err)
		}
		if err := g.Join(pid(1), "p2"); err != nil {
			t.Fatal(err)
		}
		if err := g.Join(pid(1), "again"); !errors.Is(err, game.ErrAlreadyJoined) {
			t.Errorf("countdown: err = %v, want ErrAlreadyJoined", err)
		}
	})

	t.Run("once play has started, joining is refused with ErrStarted", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		if err := g.Join(99, "late"); !errors.Is(err, game.ErrStarted) {
			t.Errorf("err = %v, want ErrStarted", err)
		}
		if g.State() != game.StateRunning {
			t.Errorf("State() = %v, want Running", g.State())
		}
	})

	t.Run("after the game is over, joining is refused with ErrOver", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		markBody(g, 1, [2]int{3, 6}) // an obstacle right in front of seat 0
		g.Tick()
		if err := g.Join(99, "late"); !errors.Is(err, game.ErrOver) {
			t.Errorf("err = %v, want ErrOver", err)
		}
	})
}

func TestStart_CountdownNumbers(t *testing.T) {
	t.Parallel()

	cfg := testCfg()
	cfg.TicksPerCount = 2
	g := newGameCfg(t, cfg, 2)

	var got []int
	for g.phase == phaseCountdown {
		got = append(got, g.countdownNumber())
		g.Tick()
	}
	want := []int{3, 3, 2, 2, 1, 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("countdown showed %v, want %v", got, want)
	}
	if g.phase != phasePlaying {
		t.Errorf("phase = %v after the countdown, want playing", g.phase)
	}
}

func TestStart_NobodyMovesDuringTheCountdownAndFoodAppearsWhenItEnds(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2)
	if g.hasFood {
		t.Fatal("food exists before play has begun")
	}
	x0, y0 := g.seats[0].body[0].x, g.seats[0].body[0].y
	for g.phase == phaseCountdown {
		if h := g.seats[0].body[0]; h.x != x0 || h.y != y0 {
			t.Fatalf("seat 0 moved to (%d,%d) during the countdown", h.x, h.y)
		}
		g.Tick()
	}
	if !g.hasFood {
		t.Error("no food on the board once play began")
	}
}

func TestStart_PreSteeringDuringTheCountdown(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2)
	g.Input(pid(0), arrow(input.DirUp))
	x, y := g.seats[0].body[0].x, g.seats[0].body[0].y
	for g.phase == phaseCountdown {
		g.Tick()
	}
	g.Tick() // the first move
	if h := g.seats[0].body[0]; h.x != x || h.y != y-1 {
		t.Errorf("after pre-steering up the head is at (%d,%d), want (%d,%d)", h.x, h.y, x, y-1)
	}
}

func TestStart_LeavingDuringTheCountdown(t *testing.T) {
	t.Parallel()

	t.Run("below two players it goes back to waiting", func(t *testing.T) {
		t.Parallel()
		g := newGame(t, 2)
		g.Leave(pid(1))
		if g.State() != game.StateWaiting || g.phase != phaseWaiting {
			t.Fatalf("State() = %v, phase = %v, want Waiting", g.State(), g.phase)
		}

		if err := g.Join(pid(5), "new"); err != nil {
			t.Fatalf("Join after a leave: %v", err)
		}
		if g.phase != phaseCountdown {
			t.Errorf("phase = %v, want countdown", g.phase)
		}
	})

	t.Run("with two players left the countdown continues", func(t *testing.T) {
		t.Parallel()
		g := newGame(t, 3)
		g.Tick()
		before := g.remaining
		g.Leave(pid(2))
		if g.phase != phaseCountdown || g.remaining != before {
			t.Errorf("phase = %v, remaining = %d, want the countdown untouched (remaining %d)", g.phase, g.remaining, before)
		}
	})
}

// ---- movement, wrap and steering -------------------------------------------------

func TestMovement_EveryHeadAdvances(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 3)
	starts := [3]cell{}
	for i := range starts {
		starts[i] = g.seats[i].body[0]
	}
	g.Tick()

	for i := 0; i < 3; i++ {
		h := g.seats[i].body[0]
		dx, dy := delta(g.seats[i].heading)
		wantX, wantY := g.wrap(starts[i].x+dx, starts[i].y+dy)
		if h.x != wantX || h.y != wantY {
			t.Errorf("seat %d is at (%d,%d), want (%d,%d)", i, h.x, h.y, wantX, wantY)
		}
		if g.grid[g.idx(h.x, h.y)] != uint8(i+1) {
			t.Errorf("seat %d's head cell is not marked as its own", i)
		}
	}
}

func TestMovement_WrapsAtEveryEdge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		x, y         int
		d            input.Dir
		wantX, wantY int
	}{
		{"off the right edge appears on the left", 19, 6, input.DirRight, 0, 6},
		{"off the left edge appears on the right", 0, 6, input.DirLeft, 19, 6},
		{"off the bottom edge appears on top", 10, 11, input.DirDown, 10, 0},
		{"off the top edge appears at the bottom", 10, 0, input.DirUp, 10, 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, 2)
			arrange(g, 1, 10, 6, input.DirRight) // out of the way of every edge tested here
			arrange(g, 0, tt.x, tt.y, tt.d)
			g.Tick()

			h := g.seats[0].body[0]
			if h.x != tt.wantX || h.y != tt.wantY {
				t.Errorf("head is at (%d,%d), want (%d,%d)", h.x, h.y, tt.wantX, tt.wantY)
			}
			if !g.seats[0].alive {
				t.Error("wrapping around an edge should not be a crash")
			}
		})
	}
}

func TestSteering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		keys         []input.Key
		wantX, wantY int // where seat 0 is after one tick; it starts at (5,5) heading right
	}{
		{name: "no key keeps the heading", keys: nil, wantX: 6, wantY: 5},
		{name: "a turn takes effect on the next tick", keys: []input.Key{arrow(input.DirUp)}, wantX: 5, wantY: 4},
		{name: "turn down", keys: []input.Key{arrow(input.DirDown)}, wantX: 5, wantY: 6},
		{name: "the same direction is fine", keys: []input.Key{arrow(input.DirRight)}, wantX: 6, wantY: 5},
		{name: "a 180 degree reverse is ignored", keys: []input.Key{arrow(input.DirLeft)}, wantX: 6, wantY: 5},
		{name: "the last valid key wins", keys: []input.Key{arrow(input.DirUp), arrow(input.DirDown)}, wantX: 5, wantY: 6},
		{name: "a reverse after a valid key is ignored, so the earlier key stands", keys: []input.Key{arrow(input.DirUp), arrow(input.DirLeft)}, wantX: 5, wantY: 4},
		{name: "keys other than directions are ignored", keys: []input.Key{letter('x'), {Kind: input.KindEnter}, letter('5')}, wantX: 6, wantY: 5},
		{name: "w a s d steer too: w is up", keys: []input.Key{letter('w')}, wantX: 5, wantY: 4},
		{name: "w a s d steer too: s is down", keys: []input.Key{letter('s')}, wantX: 5, wantY: 6},
		{name: "a is a reverse when heading right", keys: []input.Key{letter('a')}, wantX: 6, wantY: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, 2)
			arrange(g, 0, 5, 5, input.DirRight)
			for _, k := range tt.keys {
				g.Input(pid(0), k)
			}

			if h := g.seats[0].body[0]; h.x != 5 || h.y != 5 {
				t.Fatalf("the head moved before the tick, to (%d,%d)", h.x, h.y)
			}
			g.Tick()
			if h := g.seats[0].body[0]; h.x != tt.wantX || h.y != tt.wantY {
				t.Errorf("head is at (%d,%d), want (%d,%d)", h.x, h.y, tt.wantX, tt.wantY)
			}
		})
	}
}

func TestSteering_ReverseIsJudgedAgainstTheCurrentHeading(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 5, 5, input.DirRight)

	g.Input(pid(0), arrow(input.DirUp))
	g.Tick() // now heading up, at (5,4)
	g.Input(pid(0), arrow(input.DirDown))
	g.Tick() // down is a reverse of up: ignored
	if h := g.seats[0].body[0]; h.x != 5 || h.y != 3 {
		t.Errorf("head is at (%d,%d), want (5,3): still going up", h.x, h.y)
	}
}

func TestInput_IgnoredWhenItShouldBe(t *testing.T) {
	t.Parallel()

	t.Run("from a stranger", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		g.Input(99, arrow(input.DirUp))
	})

	t.Run("from a dead player", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 3)
		kill(g, 2)
		before := g.seats[2].pending
		g.Input(pid(2), arrow(input.DirUp))
		if g.seats[2].pending != before {
			t.Error("a dead player's key changed its direction")
		}
	})

	t.Run("while waiting", func(t *testing.T) {
		t.Parallel()
		g := newGame(t, 1)
		before := g.seats[0].pending
		g.Input(pid(0), arrow(input.DirUp))
		if g.seats[0].pending != before {
			t.Error("a key while waiting changed a direction")
		}
	})
}

// ---- collisions -------------------------------------------------------------

func TestCrash_IntoAnotherSnakesBody(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 5, 5, input.DirRight)
	markBody(g, 1, [2]int{6, 5}) // directly in front of seat 0
	g.Tick()

	if g.seats[0].alive {
		t.Error("the head that ran into another body is still alive")
	}
	wantOver(t, g, pid(1), false)
}

func TestCrash_IntoADeadPlayersBody(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 3)
	arrange(g, 0, 5, 5, input.DirRight)
	markBody(g, 2, [2]int{6, 5})
	kill(g, 2)
	g.Tick()

	if g.seats[0].alive {
		t.Error("the head that ran into a dead player's body is still alive")
	}
	wantOver(t, g, pid(1), false)
}

func TestCrash_IntoOwnBody(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	// Head (5,5) heading up wants (5,4), which is this snake's own second
	// (non-tail) segment; the tail is (6,4), elsewhere, so it does not vacate.
	arrangeBody(g, 0, input.DirUp, [2]int{5, 5}, [2]int{5, 4}, [2]int{6, 4})
	setFood(g, 19, 11) // out of the way
	g.Tick()

	if g.seats[0].alive {
		t.Error("running into a non-tail body segment should be fatal")
	}
}

func TestCrash_IntoAnotherHead(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 5, 5, input.DirRight)
	// Seat 1's head sits on (6,5), directly in front of seat 0, and is moving
	// away downwards. Seat 0 still runs into the cell it occupied (a head, not
	// a tail, so it does not vacate).
	arrange(g, 1, 6, 5, input.DirDown)
	g.Tick()

	if g.seats[0].alive {
		t.Error("seat 0 ran into a head and survived")
	}
	if !g.seats[1].alive {
		t.Error("seat 1 moved away and should live")
	}
	wantOver(t, g, pid(1), false)
}

func TestCrash_TwoHeadsIntoTheSameEmptyCell(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 5, 5, input.DirRight)
	arrange(g, 1, 7, 5, input.DirLeft) // both want (6,5)
	g.Tick()

	if g.seats[0].alive || g.seats[1].alive {
		t.Error("both heads should have died")
	}
	wantOver(t, g, 0, true)
}

func TestCrash_HeadsSwappingPlaces(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 5, 5, input.DirRight)
	arrange(g, 1, 6, 5, input.DirLeft) // each is entering the other's cell
	g.Tick()

	if g.seats[0].alive || g.seats[1].alive {
		t.Error("both heads should have died")
	}
	wantOver(t, g, 0, true)
}

// A snake can always follow into the cell its own tail is vacating, so a
// snake never crashes into itself by "catching its own tail".
func TestCrash_FollowingIntoOwnVacatingTailIsSafe(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	// Head (5,5), heading up: the cell above, (5,4), is where the tail (5,6)
	// currently is (the snake is curled in a tight U).
	arrangeBody(g, 0, input.DirUp, [2]int{5, 5}, [2]int{6, 5}, [2]int{6, 6}, [2]int{5, 6})
	setFood(g, 19, 11)
	g.Tick()

	if !g.seats[0].alive {
		t.Fatal("following into the tail's vacated cell should be safe")
	}
	if h := g.seats[0].body[0]; h.x != 5 || h.y != 4 {
		t.Errorf("head is at (%d,%d), want (5,4)", h.x, h.y)
	}
	if len(g.seats[0].body) != 4 {
		t.Errorf("body has %d segments, want 4 (unchanged length)", len(g.seats[0].body))
	}
}

// The vacating-tail exception is only for a snake's own tail: another
// snake's tail is a solid obstacle regardless of what that snake does this
// same tick, whether it is moving on normally or growing.
func TestCrash_AnotherSnakesTailIsAlwaysSolid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(g *Game) // arranges seat 1 so its tail sits at (6,5)
	}{
		{"seat 1 is moving on normally", func(g *Game) {
			arrangeBody(g, 1, input.DirUp, [2]int{6, 4}, [2]int{6, 5})
			setFood(g, 19, 11) // out of the way
		}},
		{"seat 1 is growing this tick, so its tail would not vacate anyway", func(g *Game) {
			arrangeBody(g, 1, input.DirRight, [2]int{6, 6}, [2]int{6, 5})
			setFood(g, 7, 6) // seat 1's next head
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, 2)
			tt.setup(g)
			arrange(g, 0, 5, 5, input.DirRight) // wants to step onto (6,5), seat 1's tail
			g.Tick()

			if g.seats[0].alive {
				t.Error("seat 0 followed into another snake's tail and survived")
			}
			if !g.seats[1].alive {
				t.Error("seat 1 should be unaffected and alive")
			}
		})
	}
}

// ---- food and growth --------------------------------------------------------

func TestFood_EatingGrowsByOneSegmentAndKeepsTheOldBody(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrangeBody(g, 0, input.DirRight, [2]int{5, 5}, [2]int{4, 5}, [2]int{3, 5})
	setFood(g, 6, 5) // directly ahead
	g.Tick()

	s := g.seats[0]
	if !s.alive {
		t.Fatal("eating should not be fatal")
	}
	want := []cell{{6, 5}, {5, 5}, {4, 5}, {3, 5}}
	if fmt.Sprint(s.body) != fmt.Sprint(want) {
		t.Errorf("body = %v, want %v (grew by one, kept the old tail)", s.body, want)
	}
	if g.grid[g.idx(3, 5)] != uint8(1) {
		t.Error("the old tail cell should still belong to seat 0")
	}
}

func TestFood_ANewPelletAppearsSomewhereElse(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	before := g.food
	ax, ay := g.wrap(before.x-1, before.y) // one step from the food, wrap-safe if before.x is 0
	arrange(g, 0, ax, ay, input.DirRight)
	g.Tick()

	if !g.hasFood {
		t.Fatal("no food after it was eaten")
	}
	if g.food == before {
		t.Error("the new pellet is in the same place as the old one")
	}
	if g.grid[g.idx(g.food.x, g.food.y)] != foodCell {
		t.Error("the grid does not show the new pellet")
	}
}

// Two heads reaching the food at once both die (the same-cell rule), and
// nobody ate it: it stays exactly where it was.
func TestFood_TwoHeadsRacingForItBothDieAndItStays(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	setFood(g, 6, 5)
	arrange(g, 0, 5, 5, input.DirRight)
	arrange(g, 1, 7, 5, input.DirLeft)
	g.Tick()

	if g.seats[0].alive || g.seats[1].alive {
		t.Error("both heads should have died racing for the food")
	}
	if !g.hasFood || g.food != (cell{6, 5}) {
		t.Errorf("food = %v (hasFood %v), want it still at (6,5)", g.food, g.hasFood)
	}
}

// ---- who wins ---------------------------------------------------------------

// seat 3 is not a real player in these three-player games; markBody with it
// is only ever used as a raw, unmoving obstacle marker.
const wallSeat = 3

func TestOutcome_LastAliveWins(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 3)
	setFood(g, 19, 11)                   // keep food out of the way of this scripted sequence
	markBody(g, wallSeat, [2]int{3, 6})  // seat 0 will hit this on the first tick
	arrange(g, 0, 2, 6, input.DirRight)  // dies on the first tick
	arrange(g, 1, 15, 6, input.DirRight) // lives one more tick, then hits an obstacle
	arrange(g, 2, 10, 2, input.DirDown)  // lives throughout

	g.Tick() // seat 0 crashes; seat 1 moves to (16,6); seat 2 moves to (10,3)
	if g.State() != game.StateRunning {
		t.Fatalf("after one death of three, State() = %v, want Running", g.State())
	}
	if g.seats[0].alive || !g.seats[1].alive || !g.seats[2].alive {
		t.Fatalf("alive after tick 1: %v %v %v, want false true true", g.seats[0].alive, g.seats[1].alive, g.seats[2].alive)
	}

	markBody(g, wallSeat, [2]int{17, 6}) // seat 1's next cell
	g.Tick()                             // seat 1 hits it: seat 2 is the last one alive
	wantOver(t, g, pid(2), false)
}

func TestOutcome_SimultaneousDeaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		players    int
		setup      func(g *Game)
		wantWinner game.PlayerID
		wantDraw   bool
	}{
		{
			name:    "the last two die together: a draw",
			players: 2,
			setup: func(g *Game) {
				arrange(g, 0, 5, 5, input.DirRight)
				arrange(g, 1, 7, 5, input.DirLeft)
			},
			wantDraw: true,
		},
		{
			name:    "the last two die together after a third died earlier: a draw",
			players: 3,
			setup: func(g *Game) {
				kill(g, 2)
				arrange(g, 0, 5, 5, input.DirRight)
				arrange(g, 1, 7, 5, input.DirLeft)
			},
			wantDraw: true,
		},
		{
			name:    "two die together while a third survives: the survivor wins",
			players: 3,
			setup: func(g *Game) {
				arrange(g, 0, 5, 5, input.DirRight)
				arrange(g, 1, 7, 5, input.DirLeft)
				arrange(g, 2, 10, 2, input.DirDown)
			},
			wantWinner: pid(2),
		},
		{
			// Every seat is real in a four-player game, so there is no spare
			// owner number for a decorative obstacle (and arrange would wipe
			// one anyway, since it clears its own seat's cells first). Seat 2
			// instead dies by curling into its own body: heading down from
			// (9,9), the next cell (9,10) is its own second segment, not its
			// tail, so it does not get to vacate it.
			name:    "four players: three die together, the fourth wins",
			players: 4,
			setup: func(g *Game) {
				arrange(g, 0, 5, 5, input.DirRight)
				arrange(g, 1, 7, 5, input.DirLeft)
				arrangeBody(g, 2, input.DirDown, [2]int{9, 9}, [2]int{9, 10}, [2]int{10, 10}, [2]int{10, 9})
				arrange(g, 3, 10, 8, input.DirUp)
			},
			wantWinner: pid(3),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, tt.players)
			setFood(g, 19, 11) // keep the shared pellet out of every scripted crash
			tt.setup(g)
			g.Tick()
			wantOver(t, g, tt.wantWinner, tt.wantDraw)
		})
	}
}

func TestOutcome_NothingHappensAfterTheGameIsOver(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	setFood(g, 19, 11)
	arrange(g, 0, 5, 5, input.DirRight)
	arrange(g, 1, 7, 5, input.DirLeft)
	g.Tick() // both die: a draw, State Over
	want := g.Outcome()
	before := fmt.Sprint(g.grid)

	g.Tick()
	g.Tick()
	if fmt.Sprint(g.grid) != before {
		t.Error("the board changed on a tick after the game was over")
	}
	if g.Outcome() != want || g.State() != game.StateOver {
		t.Error("the result changed after the game ended")
	}
}

// ---- leaving --------------------------------------------------------------

func TestLeave_MidRound(t *testing.T) {
	t.Parallel()

	t.Run("in a two-player game the other player wins by forfeit", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		g.Leave(pid(0))
		wantOver(t, g, pid(1), false)
		if !g.forfeit {
			t.Error("the win should be marked as a forfeit")
		}
	})

	t.Run("in a three-player game the round goes on and the leaver's body stays", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 3)
		g.Leave(pid(0))
		if g.State() != game.StateRunning {
			t.Fatalf("State() = %v, want Running", g.State())
		}
		if g.seats[0].alive {
			t.Error("the leaver's snake should be dead")
		}
		if g.grid[g.idx(g.seats[0].body[0].x, g.seats[0].body[0].y)] != uint8(1) {
			t.Error("the leaver's body should stay on the board as an obstacle")
		}
	})

	t.Run("a second player leaving a three-player game ends it", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 3)
		g.Leave(pid(0))
		g.Leave(pid(1))
		wantOver(t, g, pid(2), false)
	})

	t.Run("a dead player leaving changes nothing", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 3)
		kill(g, 2)
		g.Leave(pid(2))
		if g.State() != game.StateRunning {
			t.Errorf("State() = %v, want Running", g.State())
		}
	})

	t.Run("a stranger leaving is a no-op", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		g.Leave(99)
		if g.State() != game.StateRunning {
			t.Errorf("State() = %v, want Running", g.State())
		}
	})
}

func TestTick_IgnoredOnceOver(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	setFood(g, 19, 11)
	arrange(g, 0, 5, 5, input.DirRight)
	arrange(g, 1, 7, 5, input.DirLeft)
	g.Tick()
	before := fmt.Sprint(g.grid)
	g.Tick()
	if fmt.Sprint(g.grid) != before {
		t.Error("the board changed on a tick after the game was over")
	}
}
