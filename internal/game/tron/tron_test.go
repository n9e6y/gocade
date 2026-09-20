package tron

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// testCfg is a small board (20x12) with a countdown of three ticks, so
// scenarios stay short and readable. Seat 0 spawns at (2,6) heading right,
// seat 1 at (17,6) heading left, seat 2 at (10,1) heading down and seat 3 at
// (10,10) heading up.
func testCfg() Config {
	return Config{Width: 20, Height: 12, TicksPerCount: 1, Tick: time.Millisecond}
}

// pid is the player id used for a seat: seat 0 is player 1, and so on.
func pid(seat int) game.PlayerID { return game.PlayerID(seat + 1) }

// newGame returns a game with that many players seated, so the countdown is
// running if there are at least two.
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
// the heads.
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

// arrange moves a seat's head to (x, y) heading d, erasing its old trail, so
// a test can set up an exact situation.
func arrange(g *Game, seat, x, y int, d input.Dir) {
	for i := range g.grid {
		if g.grid[i] == uint8(seat+1) {
			g.grid[i] = 0
		}
	}
	s := g.seats[seat]
	s.x, s.y, s.heading, s.pending = x, y, d, d
	g.grid[g.idx(x, y)] = uint8(seat + 1)
}

// markTrail makes the given cells trail belonging to a seat.
func markTrail(g *Game, seat int, cells ...[2]int) {
	for _, c := range cells {
		g.grid[g.idx(c[0], c[1])] = uint8(seat + 1)
	}
}

// kill makes a seat's snake dead, as if it had crashed earlier; its trail
// stays on the board.
func kill(g *Game, seat int) { g.seats[seat].alive = false }

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

// ---- meta -----------------------------------------------------------------

func TestMeta(t *testing.T) {
	t.Parallel()

	g := New()
	if g.Name() != "tron" {
		t.Errorf("Name() = %q, want tron", g.Name())
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

// Every seat starts on the board, apart from the others, facing inward.
func TestSpawnsAreDistinctAndFaceInward(t *testing.T) {
	t.Parallel()

	for _, cfg := range []Config{testCfg(), DefaultConfig(), {Width: minWidth, Height: minHeight, TicksPerCount: 1, Tick: time.Millisecond}} {
		g := newGameCfg(t, cfg, 4)
		seen := map[[2]int]int{}
		for seat, s := range g.seats {
			if !g.inBounds(s.x, s.y) {
				t.Fatalf("%dx%d seat %d spawns off the board at (%d,%d)", cfg.Width, cfg.Height, seat, s.x, s.y)
			}
			if other, dup := seen[[2]int{s.x, s.y}]; dup {
				t.Fatalf("%dx%d seats %d and %d share the spawn (%d,%d)", cfg.Width, cfg.Height, other, seat, s.x, s.y)
			}
			seen[[2]int{s.x, s.y}] = seat

			// One step forward must stay on the board, and one step "backward"
			// (towards the wall behind the spawn) must be closer to the wall
			// than the centre is: that is what facing inward means.
			dx, dy := delta(s.heading)
			if !g.inBounds(s.x+dx, s.y+dy) {
				t.Errorf("%dx%d seat %d faces a wall at its spawn", cfg.Width, cfg.Height, seat)
			}
			toCentreX, toCentreY := cfg.Width/2-s.x, cfg.Height/2-s.y
			if dx*toCentreX < 0 || dy*toCentreY < 0 {
				t.Errorf("%dx%d seat %d heads away from the centre", cfg.Width, cfg.Height, seat)
			}
		}
	}
}

// ---- the start rule -------------------------------------------------------

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
		arrange(g, 0, 0, 5, input.DirLeft)
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

func TestStart_NobodyMovesDuringTheCountdown(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2)
	x0, y0 := g.seats[0].x, g.seats[0].y
	for g.phase == phaseCountdown {
		if s := g.seats[0]; s.x != x0 || s.y != y0 {
			t.Fatalf("seat 0 moved to (%d,%d) during the countdown", s.x, s.y)
		}
		g.Tick()
	}
	// The tick that ends the countdown does not move anybody either.
	if s := g.seats[0]; s.x != x0 || s.y != y0 {
		t.Errorf("seat 0 moved on the tick that ended the countdown")
	}
}

func TestStart_PreSteeringDuringTheCountdown(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2)
	g.Input(pid(0), arrow(input.DirUp))
	x, y := g.seats[0].x, g.seats[0].y
	for g.phase == phaseCountdown {
		g.Tick()
	}
	g.Tick() // the first move
	if s := g.seats[0]; s.x != x || s.y != y-1 {
		t.Errorf("after pre-steering up the head is at (%d,%d), want (%d,%d)", s.x, s.y, x, y-1)
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

		// The seat is free again, and a new arrival restarts the countdown.
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

// ---- movement and steering ------------------------------------------------

func TestMovement_EveryHeadAdvancesAndLeavesATrail(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 3)
	starts := [3][2]int{}
	for i := range starts {
		starts[i] = [2]int{g.seats[i].x, g.seats[i].y}
	}
	g.Tick()

	for i := 0; i < 3; i++ {
		s := g.seats[i]
		dx, dy := delta(s.heading)
		if s.x != starts[i][0]+dx || s.y != starts[i][1]+dy {
			t.Errorf("seat %d is at (%d,%d), want one step from (%d,%d)", i, s.x, s.y, starts[i][0], starts[i][1])
		}
		if g.grid[g.idx(starts[i][0], starts[i][1])] != uint8(i+1) {
			t.Errorf("seat %d left no trail on its starting cell", i)
		}
		if g.grid[g.idx(s.x, s.y)] != uint8(i+1) {
			t.Errorf("seat %d's head cell is not marked as its own", i)
		}
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
		{name: "w a s d steer too: capital letters", keys: []input.Key{letter('W')}, wantX: 5, wantY: 4},
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

			// Nothing moves until the tick.
			if s := g.seats[0]; s.x != 5 || s.y != 5 {
				t.Fatalf("the head moved before the tick, to (%d,%d)", s.x, s.y)
			}
			g.Tick()
			if s := g.seats[0]; s.x != tt.wantX || s.y != tt.wantY {
				t.Errorf("head is at (%d,%d), want (%d,%d)", s.x, s.y, tt.wantX, tt.wantY)
			}
		})
	}
}

// A reverse is judged against the direction the head last moved in, so after
// a turn has taken effect, the opposite of the NEW heading is the reverse.
func TestSteering_ReverseIsJudgedAgainstTheCurrentHeading(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 5, 5, input.DirRight)

	g.Input(pid(0), arrow(input.DirUp))
	g.Tick() // now heading up, at (5,4)
	g.Input(pid(0), arrow(input.DirDown))
	g.Tick() // down is a reverse of up: ignored
	if s := g.seats[0]; s.x != 5 || s.y != 3 {
		t.Errorf("head is at (%d,%d), want (5,3): still going up", s.x, s.y)
	}

	g.Input(pid(0), arrow(input.DirLeft)) // left is fine now
	g.Tick()
	if s := g.seats[0]; s.x != 4 || s.y != 3 {
		t.Errorf("head is at (%d,%d), want (4,3)", s.x, s.y)
	}
}

func TestInput_IgnoredWhenItShouldBe(t *testing.T) {
	t.Parallel()

	t.Run("from a stranger", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		g.Input(99, arrow(input.DirUp)) // must not panic or change anything
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

	t.Run("after the game is over", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		arrange(g, 0, 0, 5, input.DirLeft)
		g.Tick()
		before := g.seats[1].pending
		g.Input(pid(1), arrow(input.DirUp))
		if g.seats[1].pending != before {
			t.Error("a key after game over changed a direction")
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

// ---- crashes --------------------------------------------------------------

func TestCrash_IntoAWall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		x, y int
		d    input.Dir
	}{
		{"left wall", 0, 5, input.DirLeft},
		{"right wall", 19, 5, input.DirRight},
		{"top wall", 8, 0, input.DirUp},
		{"bottom wall", 8, 11, input.DirDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, 2)
			arrange(g, 0, tt.x, tt.y, tt.d)
			g.Tick()

			s := g.seats[0]
			if s.alive {
				t.Error("the head that hit the wall is still alive")
			}
			if s.x != tt.x || s.y != tt.y {
				t.Errorf("a crashed head moved to (%d,%d)", s.x, s.y)
			}
			wantOver(t, g, pid(1), false)
		})
	}
}

func TestCrash_IntoATrail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		players   int
		trailSeat int
		deadSeat  int // -1 for none: the trail owner is already dead
	}{
		{name: "own trail", players: 2, trailSeat: 0, deadSeat: -1},
		{name: "another player's trail", players: 2, trailSeat: 1, deadSeat: -1},
		{name: "a dead player's trail", players: 3, trailSeat: 2, deadSeat: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, tt.players)
			arrange(g, 0, 5, 5, input.DirRight)
			markTrail(g, tt.trailSeat, [2]int{6, 5}) // directly in front of seat 0
			if tt.deadSeat >= 0 {
				kill(g, tt.deadSeat)
			}
			g.Tick()

			if g.seats[0].alive {
				t.Error("the head that ran into a trail is still alive")
			}
			wantOver(t, g, pid(1), false) // seat 1 is the only one left
		})
	}
}

func TestCrash_IntoAnotherHead(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 5, 5, input.DirRight)
	// Seat 1's head sits on (6,5), directly in front of seat 0, and is moving
	// away downwards. Seat 0 still runs into the cell it occupied.
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

func TestCrash_TwoHeadsIntoTheSameCell(t *testing.T) {
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

// ---- who wins -------------------------------------------------------------

func TestOutcome_LastAliveWins(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 3)
	arrange(g, 0, 0, 5, input.DirLeft)   // dies on the first tick
	arrange(g, 1, 18, 8, input.DirRight) // lives one more tick, then hits the wall
	arrange(g, 2, 10, 2, input.DirDown)  // lives throughout

	g.Tick()
	if g.State() != game.StateRunning {
		t.Fatalf("after one death of three, State() = %v, want Running", g.State())
	}
	if g.seats[0].alive || !g.seats[1].alive || !g.seats[2].alive {
		t.Fatalf("alive after tick 1: %v %v %v, want false true true", g.seats[0].alive, g.seats[1].alive, g.seats[2].alive)
	}

	g.Tick() // seat 1 moves to (19,8), alive
	g.Tick() // seat 1 hits the wall: seat 2 is the last one alive
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
			name:    "all three die together: a draw",
			players: 3,
			setup: func(g *Game) {
				arrange(g, 0, 0, 5, input.DirLeft)
				arrange(g, 1, 19, 5, input.DirRight)
				arrange(g, 2, 8, 0, input.DirUp)
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
			name:    "four players: three die together, the fourth wins",
			players: 4,
			setup: func(g *Game) {
				arrange(g, 0, 0, 5, input.DirLeft)
				arrange(g, 1, 19, 5, input.DirRight)
				arrange(g, 2, 8, 0, input.DirUp)
				arrange(g, 3, 10, 8, input.DirUp)
			},
			wantWinner: pid(3),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, tt.players)
			tt.setup(g)
			g.Tick()
			wantOver(t, g, tt.wantWinner, tt.wantDraw)
		})
	}
}

func TestOutcome_NothingHappensAfterTheGameIsOver(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 0, 5, input.DirLeft)
	g.Tick()
	x, y, want := g.seats[1].x, g.seats[1].y, g.Outcome()

	g.Tick()
	g.Tick()
	if s := g.seats[1]; s.x != x || s.y != y {
		t.Errorf("the winner kept moving after the game ended: (%d,%d)", s.x, s.y)
	}
	if g.Outcome() != want || g.State() != game.StateOver {
		t.Errorf("the result changed after the game ended")
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

	t.Run("in a three-player game the round goes on", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 3)
		g.Leave(pid(0))
		if g.State() != game.StateRunning {
			t.Fatalf("State() = %v, want Running", g.State())
		}
		if g.seats[0].alive {
			t.Error("the leaver's snake should be dead")
		}

		// The leaver's trail stays as an obstacle, and the last one alive wins.
		arrange(g, 1, 5, 5, input.DirRight)
		markTrail(g, 0, [2]int{6, 5})
		arrange(g, 2, 10, 2, input.DirDown)
		g.Tick()
		wantOver(t, g, pid(2), false)
		if g.forfeit {
			t.Error("a win by crashing is not a forfeit")
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

	t.Run("leaving after the game is over changes nothing", func(t *testing.T) {
		t.Parallel()
		g := newPlaying(t, 2)
		arrange(g, 0, 0, 5, input.DirLeft)
		g.Tick()
		want := g.Outcome()
		g.Leave(pid(0))
		g.Leave(pid(1))
		if g.Outcome() != want {
			t.Error("the outcome changed when players left after the end")
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
	arrange(g, 0, 0, 5, input.DirLeft)
	g.Tick()
	before := fmt.Sprint(g.grid)
	g.Tick()
	if fmt.Sprint(g.grid) != before {
		t.Error("the board changed on a tick after the game was over")
	}
}
