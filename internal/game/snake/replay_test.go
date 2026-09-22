package snake

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// The replay tests prove the game is deterministic: a recorded list of
// inputs, on a fixed seed, replayed on a fresh game, always gives the same
// result — including where the food ends up. That only holds because the
// game package reads no clock, has no map iteration in its rules, and the
// one source of randomness is the seeded *rand.Rand Config carries in; ticks
// are driven by hand here, exactly as a room would drive them.

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

// TestReplay_TheSameScriptGivesTheSameResultEveryTime is the core claim: a
// busy two-player script with turns, reversals and wrap-arounds gives byte-
// identical frames, the same outcome, the same tick count and — because the
// frames match exactly — the same food placements, every single time.
func TestReplay_TheSameScriptGivesTheSameResultEveryTime(t *testing.T) {
	t.Parallel()

	script := []scriptStep{
		{0, pid(0), arrow(input.DirDown)},
		{0, pid(1), arrow(input.DirUp)},
		{4, pid(0), arrow(input.DirRight)},
		{4, pid(0), arrow(input.DirLeft)}, // a reverse: ignored
		{5, pid(1), letter('d')},
		{9, pid(0), arrow(input.DirUp)},
		{13, pid(1), letter('a')},
		{20, pid(0), arrow(input.DirLeft)},
		{25, pid(1), arrow(input.DirDown)},
	}

	first := replay(t, testCfg(), 2, script, 300)
	for i := 0; i < 20; i++ {
		if again := replay(t, testCfg(), 2, script, 300); !sameResult(first, again) {
			t.Fatalf("replay %d differs from the first run: %+v vs %+v", i, again.outcome, first.outcome)
		}
	}
}

// Random scripts, with a fixed seed each so a failure can be reproduced: any
// script, however odd, must replay identically, on any board and any config
// seed.
func TestReplay_RandomScriptsAreRepeatable(t *testing.T) {
	t.Parallel()

	dirs := []input.Dir{input.DirUp, input.DirDown, input.DirLeft, input.DirRight}
	sawWinner, sawDraw := false, false

	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		players := 2 + rng.Intn(3) // 2, 3 or 4 players
		cfg := testCfg()
		cfg.Seed = seed // the game's own food-placement seed, independent of the script's

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

		a := replay(t, cfg, players, script, 300)
		b := replay(t, cfg, players, script, 300)
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

// Two different Config.Seed values are expected to place food differently
// (not guaranteed on every tick, but the very first pellet almost certainly
// differs), which is what gives different rooms different games.
func TestReplay_DifferentSeedsUsuallyPlaceFoodDifferently(t *testing.T) {
	t.Parallel()

	differ := 0
	for seed := int64(1); seed <= 20; seed++ {
		cfg := testCfg()
		g1 := newGameCfg(t, cfg, 2)
		cfg2 := cfg
		cfg2.Seed = seed + 1000
		g2 := newGameCfg(t, cfg2, 2)
		for g1.phase == phaseCountdown {
			g1.Tick()
			g2.Tick()
		}
		if g1.food != g2.food {
			differ++
		}
	}
	if differ == 0 {
		t.Error("20 different seeds all placed the first pellet on the same cell")
	}
}
