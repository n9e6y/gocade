package snake

import (
	"math/rand"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

var _ game.Advisor = (*Game)(nil)

// adviceDir returns the direction Advise gives seat 0, failing if it gives none.
func adviceDir(t *testing.T, g *Game, seat int) input.Dir {
	t.Helper()
	k, ok := g.Advise(pid(seat))
	if !ok {
		t.Fatalf("Advise(seat %d) gave no advice", seat)
	}
	d, isDir := k.Direction()
	if !isDir {
		t.Fatalf("Advise(seat %d) = %v, which is not a direction", seat, k)
	}
	return d
}

func TestAdvise_KeepsGoingWhenTheWayIsOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		x, y    int
		heading input.Dir
	}{
		{"right", 5, 6, input.DirRight},
		{"left", 12, 6, input.DirLeft},
		{"down", 10, 3, input.DirDown},
		{"up", 10, 8, input.DirUp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newPlaying(t, 2)
			g.hasFood = false // isolate the safety comparison from food-seeking
			arrange(g, 1, 0, 0, input.DirDown)
			arrange(g, 0, tt.x, tt.y, tt.heading)
			// With no food to chase, every direction is equally open (the board
			// wraps, so a flood fill reaches the same huge number either way),
			// so it goes straight on rather than wiggling.
			if got := adviceDir(t, g, 0); got != tt.heading {
				t.Errorf("Advise = %v, want %v (straight on)", got, tt.heading)
			}
		})
	}
}

// There are no walls, so the bot can only be turned away by a body — never by
// an edge.
func TestAdvise_EdgesNeverTurnItAway(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.hasFood = false
	arrange(g, 1, 0, 0, input.DirDown)
	arrange(g, 0, 19, 6, input.DirRight) // the last column: wraps to column 0
	if got := adviceDir(t, g, 0); got != input.DirRight {
		t.Errorf("Advise = %v, want Right (wrapping onward is exactly as safe)", got)
	}
}

func TestAdvise_TurnsAwayFromABody(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.hasFood = false
	arrange(g, 0, 10, 6, input.DirRight)
	markBody(g, 1, [2]int{11, 6}) // right in front of the head
	got := adviceDir(t, g, 0)
	if got != input.DirUp && got != input.DirDown {
		t.Errorf("Advise = %v, want Up or Down (a body is ahead)", got)
	}
}

func TestAdvise_TakesTheOnlyWayOut(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.hasFood = false
	arrange(g, 0, 10, 6, input.DirRight)
	markBody(g, 1, [2]int{11, 6}, [2]int{10, 5}) // ahead and above are blocked
	if got := adviceDir(t, g, 0); got != input.DirDown {
		t.Errorf("Advise = %v, want Down, the only open way", got)
	}
}

// Right is blocked outright, Up leads into a small enclosed pocket (three
// cells), and Down leads into the rest of the open board. A single line of
// obstacles would not do this on a wrapping board — going around the seam
// where column 19 meets column 0 gets past it — so the pocket needs walls on
// every side.
func TestAdvise_PrefersTheDirectionWithMoreRoomWhenBothAreTight(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.hasFood = false // a pure safety decision, not a food one
	arrange(g, 0, 10, 6, input.DirRight)
	markBody(g, 1, [2]int{11, 6}) // blocks Right outright
	markBody(g, 1, [2]int{9, 2}, [2]int{10, 2}, [2]int{11, 2})
	markBody(g, 1, [2]int{9, 3}, [2]int{11, 3})
	markBody(g, 1, [2]int{9, 4}, [2]int{11, 4})
	markBody(g, 1, [2]int{9, 5}, [2]int{11, 5})
	// Up reaches only (10,5), (10,4), (10,3): 3 cells, boxed in on every side.
	if got := adviceDir(t, g, 0); got != input.DirDown {
		t.Errorf("Advise = %v, want Down (the pocket up top has only 3 cells)", got)
	}
}

// When everything the bot can do is fatal it still answers, and it never
// answers with the reversal the game would ignore.
func TestAdvise_WhenTrappedItStillAnswersAndNeverReverses(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.hasFood = false
	arrange(g, 0, 10, 6, input.DirRight)
	markBody(g, 1, [2]int{11, 6}, [2]int{10, 5}, [2]int{10, 7})
	got := adviceDir(t, g, 0)
	if got == input.DirLeft {
		t.Errorf("Advise = Left, a reversal the game ignores")
	}
}

// The bot always gets to follow its own tail: it must never call that
// direction fatal, even though the cell is occupied at the start of the tick.
func TestAdvise_OwnTailIsNeverTreatedAsDanger(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.hasFood = false
	// A U-shaped body: head (5,5) heading right leads nowhere interesting,
	// but heading up leads onto the snake's own tail at (5,4).
	arrangeBody(g, 0, input.DirRight, [2]int{5, 5}, [2]int{6, 5}, [2]int{6, 4}, [2]int{5, 4})
	arrange(g, 1, 0, 0, input.DirDown)

	// Force the comparison: block every direction except Up so the bot's only
	// non-fatal move is onto its own tail.
	markBody(g, 1, [2]int{6, 5}) // right: blocked (also its own neck, but this confirms it)
	markBody(g, 1, [2]int{5, 6}) // down: blocked
	got := adviceDir(t, g, 0)
	if got != input.DirUp {
		t.Errorf("Advise = %v, want Up: onto its own tail, the only way out", got)
	}
}

func TestAdvise_AnswersDuringTheCountdown(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2) // countdown running, nobody has moved, no food yet
	if got := adviceDir(t, g, 0); got != input.DirRight {
		t.Errorf("Advise = %v, want Right (seat 0 faces right)", got)
	}
}

func TestAdvise_ChasesFoodWhenItIsSafeTo(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 1, 0, 0, input.DirDown) // out of the way
	arrange(g, 0, 10, 6, input.DirRight)
	setFood(g, 10, 3) // straight up: closer that way than continuing right

	if got := adviceDir(t, g, 0); got != input.DirUp {
		t.Errorf("Advise = %v, want Up (toward the food, on an open board)", got)
	}
}

// Safety still comes first: a bot does not walk into a tight, risky pocket
// just because food is that way.
func TestAdvise_DoesNotChaseFoodIntoDanger(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	setFood(g, 10, 4) // straight up from the head — but boxed in, see below
	arrange(g, 0, 10, 6, input.DirRight)
	arrange(g, 1, 0, 0, input.DirDown)
	// Trap "up" into a tiny 2-cell pocket that also contains the food.
	markBody(g, 1, [2]int{9, 5}, [2]int{11, 5}, [2]int{9, 4}, [2]int{11, 4}, [2]int{9, 3}, [2]int{10, 3}, [2]int{11, 3})

	if got := adviceDir(t, g, 0); got == input.DirUp {
		t.Errorf("Advise = Up, chasing food into a two-cell dead end")
	}
}

func TestAdvise_NoAdviceWhenThereIsNothingToDo(t *testing.T) {
	t.Parallel()

	waiting := NewWithConfig(testCfg())
	if err := waiting.Join(pid(0), "p1"); err != nil {
		t.Fatal(err)
	}

	dead := newPlaying(t, 3)
	kill(dead, 0)

	over := newPlaying(t, 2)
	kill(over, 1)
	over.checkEnd(false)

	tests := []struct {
		name string
		g    *Game
		p    game.PlayerID
	}{
		{"waiting for a second player", waiting, pid(0)},
		{"a crashed player", dead, pid(0)},
		{"someone who is not in the game", newPlaying(t, 2), game.PlayerID(99)},
		{"a finished game", over, pid(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if k, ok := tt.g.Advise(tt.p); ok {
				t.Errorf("Advise = %v, true; want no advice", k)
			}
		})
	}
}

func TestAdvise_DoesNotChangeTheGame(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	g.Tick()
	before := screen(g, pid(0))
	for i := 0; i < 3; i++ {
		g.Advise(pid(0))
		g.Advise(pid(1))
	}
	if after := screen(g, pid(0)); after != before {
		t.Errorf("Advise changed the game:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Property: on any board, if some direction (other than reversing) leads to a
// cell that is not a body cell, the bot never picks a direction that leads
// into a body.
func TestAdvise_NeverWalksIntoABodyWhenThereIsAWayOut(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(1)) // fixed seed: the same boards every run
	checked := 0
	for i := 0; i < 3000; i++ {
		g := newPlaying(t, 2)
		clear(g.grid)
		g.hasFood = false
		for j := 0; j < 60; j++ { // scatter some body cells
			g.grid[rng.Intn(len(g.grid))] = uint8(1 + rng.Intn(2))
		}
		x, y := rng.Intn(g.cfg.Width), rng.Intn(g.cfg.Height)
		heading := input.Dir(1 + rng.Intn(4))
		arrangeBody(g, 0, heading, [2]int{x, y}) // a single-cell body: any occupied neighbor is a real body, not its own tail

		open := func(d input.Dir) bool {
			dx, dy := delta(d)
			nx, ny := g.wrap(x+dx, y+dy)
			return !g.isBody(nx, ny)
		}
		anyOpen := false
		for d := input.DirUp; d <= input.DirRight; d++ {
			if d != opposite(heading) && open(d) {
				anyOpen = true
			}
		}
		if !anyOpen {
			continue
		}
		checked++
		got := adviceDir(t, g, 0)
		if got == opposite(heading) || !open(got) {
			t.Fatalf("board %d: at (%d,%d) heading %v, Advise = %v walks into a body or reverses", i, x, y, heading, got)
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d of the boards had a way out; the test is not testing much", checked)
	}
}

// playBots plays a game with a bot in every seat until it ends, and reports
// how many ticks it lasted and how it ended.
func playBots(t *testing.T, players int) (ticks int, out game.Outcome, last string) {
	t.Helper()
	cfg := Config{TicksPerCount: 1, Tick: time.Millisecond, Seed: 3} // the default 40x20 board
	g := newGameCfg(t, cfg, players)
	for g.State() == game.StateRunning {
		for seat := 0; seat < players; seat++ {
			if k, ok := g.Advise(pid(seat)); ok {
				g.Input(pid(seat), k)
			}
		}
		g.Tick()
		ticks++
		if ticks > 8000 {
			t.Fatal("the game did not end")
		}
	}
	return ticks, g.Outcome(), screen(g, pid(0))
}

// Bots play a whole game the same way every time (Advise has no randomness of
// its own, and the same Config.Seed places the same food), and they last a
// while: they are not running into their own bodies straight away.
func TestAdvise_BotsPlayARepeatableGame(t *testing.T) {
	t.Parallel()

	for _, players := range []int{2, 3, 4} {
		ticks1, out1, screen1 := playBots(t, players)
		ticks2, out2, screen2 := playBots(t, players)
		if ticks1 != ticks2 || out1 != out2 || screen1 != screen2 {
			t.Errorf("%d bots: two runs differ (%d vs %d ticks, %+v vs %+v)", players, ticks1, ticks2, out1, out2)
		}
		if ticks1 < 30 {
			t.Errorf("%d bots: the game ended after %d ticks, want a real game", players, ticks1)
		}
		t.Logf("%d bots: %d ticks, outcome %+v", players, ticks1, out1)
	}
}
