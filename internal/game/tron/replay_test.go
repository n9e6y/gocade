package tron

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// The replay tests prove the game is deterministic: a recorded list of
// inputs, replayed on a fresh game, always gives the same result. That only
// holds because the game package has no clock, no randomness and no map
// iteration in its rules; ticks are driven by hand here, exactly as a room
// would drive them.

// scriptStep is one recorded key press. tick is how many ticks had completed
// when it arrived: 0 means before the first tick.
type scriptStep struct {
	tick   int
	player game.PlayerID
	key    input.Key
}

// replayResult is everything observable about a finished replay.
type replayResult struct {
	outcome game.Outcome
	ticks   int
	frames  [][]byte // each player's last frame, byte for byte
}

// replay plays script on a fresh game with the given number of players, until
// the game ends or maxTicks ticks have run.
func replay(t *testing.T, cfg Config, players int, script []scriptStep, maxTicks int) replayResult {
	t.Helper()

	g := newGameCfg(t, cfg, players)
	ticks := 0
	apply := func() {
		for _, s := range script {
			if s.tick == ticks {
				g.Input(s.player, s.key)
			}
		}
	}

	apply()
	for g.State() != game.StateOver && ticks < maxTicks {
		g.Tick()
		ticks++
		apply()
	}

	res := replayResult{outcome: g.Outcome(), ticks: ticks}
	for i := 0; i < players; i++ {
		res.frames = append(res.frames, g.View(pid(i)).Frame())
	}
	return res
}

func sameResult(a, b replayResult) bool {
	if a.outcome != b.outcome || a.ticks != b.ticks || len(a.frames) != len(b.frames) {
		return false
	}
	for i := range a.frames {
		if !bytes.Equal(a.frames[i], b.frames[i]) {
			return false
		}
	}
	return true
}

// A hand-written script with an outcome worked out on paper. Board 20x12,
// countdown of 3 ticks. Both players steer before the first tick:
//   - player 1 (seat 0, at (2,6)) turns up: after 6 moves it is at y=0, and
//     the 7th move would leave the board.
//   - player 2 (seat 1, at (17,6)) turns down: after 5 moves it is at y=11,
//     and the 6th move leaves the board first.
//
// So player 2 crashes on move 6, which is tick 3+6 = 9, and player 1 wins.
func TestReplay_HandWrittenScriptHasAKnownOutcome(t *testing.T) {
	t.Parallel()

	script := []scriptStep{
		{tick: 0, player: pid(0), key: arrow(input.DirUp)},
		{tick: 0, player: pid(1), key: arrow(input.DirDown)},
	}

	got := replay(t, testCfg(), 2, script, 100)
	if got.outcome != (game.Outcome{Winner: pid(0)}) {
		t.Errorf("outcome = %+v, want player 1 to win", got.outcome)
	}
	if got.ticks != 9 {
		t.Errorf("the game ended after %d ticks, want 9", got.ticks)
	}
}

func TestReplay_TheSameScriptGivesTheSameResultEveryTime(t *testing.T) {
	t.Parallel()

	// A busy three-player script with turns, reversals and a lot of noise.
	script := []scriptStep{
		{0, pid(0), arrow(input.DirDown)},
		{0, pid(2), arrow(input.DirLeft)},
		{4, pid(0), arrow(input.DirRight)},
		{4, pid(0), arrow(input.DirLeft)}, // a reverse: ignored
		{5, pid(1), letter('s')},
		{6, pid(2), arrow(input.DirDown)},
		{6, pid(2), arrow(input.DirUp)}, // last valid key wins
		{8, pid(1), letter('a')},
		{9, pid(0), arrow(input.DirUp)},
		{12, pid(2), letter('d')},
		{13, pid(1), letter('w')},
	}

	first := replay(t, testCfg(), 3, script, 200)
	for i := 0; i < 20; i++ {
		if again := replay(t, testCfg(), 3, script, 200); !sameResult(first, again) {
			t.Fatalf("replay %d differs from the first run: %+v vs %+v", i, again.outcome, first.outcome)
		}
	}
}

// Random scripts, with a fixed seed each so a failure can be reproduced: any
// script, however odd, must replay identically.
func TestReplay_RandomScriptsAreRepeatable(t *testing.T) {
	t.Parallel()

	dirs := []input.Dir{input.DirUp, input.DirDown, input.DirLeft, input.DirRight}
	sawWinner, sawDraw := false, false

	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		players := 2 + rng.Intn(3) // 2, 3 or 4 players

		var script []scriptStep
		for tick := 0; tick < 150; tick++ {
			for rng.Intn(3) == 0 { // some ticks get several keys, some none
				script = append(script, scriptStep{
					tick:   tick,
					player: pid(rng.Intn(players)),
					key:    arrow(dirs[rng.Intn(len(dirs))]),
				})
			}
		}

		a := replay(t, testCfg(), players, script, 300)
		b := replay(t, testCfg(), players, script, 300)
		if !sameResult(a, b) {
			t.Fatalf("seed %d (%d players): two replays of the same script differ: %+v vs %+v", seed, players, a.outcome, b.outcome)
		}

		if a.outcome.Draw {
			sawDraw = true
		}
		if a.outcome.Winner != 0 {
			sawWinner = true
		}
	}

	// A sanity check that the scripts really exercise the rules: across 40
	// random games we expect both kinds of ending to occur.
	if !sawWinner {
		t.Error("no random game had a winner: the scripts are not testing much")
	}
	if !sawDraw {
		t.Log("no random game ended in a draw (possible, not an error)")
	}
}
