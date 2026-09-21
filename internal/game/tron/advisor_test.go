package tron

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

// vline marks a whole column of cells as trail of a seat.
func vline(g *Game, seat, x int) {
	for y := 0; y < g.cfg.Height; y++ {
		markTrail(g, seat, [2]int{x, y})
	}
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
			arrange(g, 0, tt.x, tt.y, tt.heading)
			// Every direction is equally open here, so it goes straight on
			// rather than wiggling.
			if got := adviceDir(t, g, 0); got != tt.heading {
				t.Errorf("Advise = %v, want %v (straight on)", got, tt.heading)
			}
		})
	}
}

func TestAdvise_TurnsAwayFromAWall(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 19, 6, input.DirRight) // the last column: the next step is off the board
	got := adviceDir(t, g, 0)
	if got != input.DirUp && got != input.DirDown {
		t.Errorf("Advise = %v, want Up or Down (the wall is ahead)", got)
	}
}

func TestAdvise_TurnsAwayFromATrail(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 10, 6, input.DirRight)
	markTrail(g, 1, [2]int{11, 6}) // right in front of the head
	got := adviceDir(t, g, 0)
	if got != input.DirUp && got != input.DirDown {
		t.Errorf("Advise = %v, want Up or Down (a trail is ahead)", got)
	}
}

func TestAdvise_TakesTheOnlyWayOut(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 10, 6, input.DirRight)
	markTrail(g, 1, [2]int{11, 6}, [2]int{10, 5}) // ahead and above are blocked
	if got := adviceDir(t, g, 0); got != input.DirDown {
		t.Errorf("Advise = %v, want Down, the only open way", got)
	}
}

// The board is split by a wall of trail at x=11 with the head just left of it,
// and a row of trail above the head cuts the top-left into a smaller pocket.
// Up and Down are both open, but Down leads to more room.
func TestAdvise_PrefersTheDirectionWithMoreRoom(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 10, 6, input.DirRight)
	vline(g, 1, 11)
	for x := 0; x <= 9; x++ {
		markTrail(g, 1, [2]int{x, 5})
	}
	// Up: (10,5), then rows 0-4 (x 0..10): 56 cells. Down: rows 6-11: 65 cells.
	if got := adviceDir(t, g, 0); got != input.DirDown {
		t.Errorf("Advise = %v, want Down (65 open cells beat 56)", got)
	}
}

// When everything the bot can do is fatal it still answers, and it never
// answers with the reversal the game would ignore.
func TestAdvise_WhenTrappedItStillAnswersAndNeverReverses(t *testing.T) {
	t.Parallel()

	g := newPlaying(t, 2)
	arrange(g, 0, 10, 6, input.DirRight)
	markTrail(g, 1, [2]int{11, 6}, [2]int{10, 5}, [2]int{10, 7})
	got := adviceDir(t, g, 0)
	if got == input.DirLeft {
		t.Errorf("Advise = Left, a reversal the game ignores")
	}
}

func TestAdvise_AnswersDuringTheCountdown(t *testing.T) {
	t.Parallel()

	g := newGame(t, 2) // countdown running, nobody has moved
	if got := adviceDir(t, g, 0); got != input.DirRight {
		t.Errorf("Advise = %v, want Right (seat 0 faces right)", got)
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

// Property: on any board, if some direction (other than reversing) leads to an
// empty cell, the bot never picks a direction that leads into a wall or a trail.
func TestAdvise_NeverWalksIntoDeathWhenThereIsAWayOut(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(1)) // fixed seed: the same boards every run
	checked := 0
	for i := 0; i < 3000; i++ {
		g := newPlaying(t, 2)
		clear(g.grid)
		for j := 0; j < 60; j++ { // scatter some trail
			g.grid[rng.Intn(len(g.grid))] = uint8(1 + rng.Intn(2))
		}
		x, y := rng.Intn(g.cfg.Width), rng.Intn(g.cfg.Height)
		heading := input.Dir(1 + rng.Intn(4))
		arrange(g, 0, x, y, heading)

		open := func(d input.Dir) bool {
			dx, dy := delta(d)
			nx, ny := x+dx, y+dy
			return g.inBounds(nx, ny) && g.grid[g.idx(nx, ny)] == 0
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
			t.Fatalf("board %d: at (%d,%d) heading %v, Advise = %v walks into death or reverses", i, x, y, heading, got)
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
	g := newGameCfg(t, Config{TicksPerCount: 1, Tick: time.Millisecond}, players) // the default 40x20 board
	for g.State() == game.StateRunning {
		for seat := 0; seat < players; seat++ {
			if k, ok := g.Advise(pid(seat)); ok {
				g.Input(pid(seat), k)
			}
		}
		g.Tick()
		ticks++
		if ticks > 5000 {
			t.Fatal("the game did not end")
		}
	}
	return ticks, g.Outcome(), screen(g, pid(0))
}

// Bots play a whole game the same way every time (Advise has no randomness),
// and they last a while: they are not running into walls straight away.
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

// BenchmarkAdvise measures one bot decision on the default board a few steps
// into a four-player round (the flood fill looks at up to three directions).
func BenchmarkAdvise(b *testing.B) {
	g := playingGame(b, DefaultConfig(), 4)
	for i := 0; i < 5; i++ {
		g.Tick()
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Advise(pid(0))
	}
}
