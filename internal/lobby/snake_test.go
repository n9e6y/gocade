package lobby

import (
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/game/snake"
)

// snakeRegistry offers real Snake, with the given settings, as menu choice 1.
// It has no bot, so picking it joins a room straight away; snakeBotsRegistry
// is the same game with its bot.
func snakeRegistry(t *testing.T, cfg snake.Config) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("snake", "Snake", func() game.Game { return noBot{snake.NewWithConfig(cfg)} })
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func snakeBotsRegistry(t *testing.T, cfg snake.Config) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("snake", "Snake", func() game.Game { return snake.NewWithConfig(cfg) }, WithBots())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A fast game: a 20x12 board, a tick every 5 ms and a one-tick countdown per
// number, so a whole round takes a fraction of a second. The real ticker still
// drives it, so this exercises the room's tick loop end to end. A fixed seed
// makes the food placement (and so the round) reproducible.
func fastSnake() snake.Config {
	return snake.Config{Width: 20, Height: 12, TicksPerCount: 1, Tick: 5 * time.Millisecond, Seed: 1}
}

// A game with a long countdown (0.5 s per number at a 5 ms tick), so a test
// has time to do things while it is still counting down.
func slowCountdownSnake() snake.Config {
	return snake.Config{Width: 20, Height: 12, TicksPerCount: 100, Tick: 5 * time.Millisecond, Seed: 1}
}

// TestSnake_HeadOnCrashIsADraw plays a whole real-time round through the
// lobby and the tick loop. Nobody steers, so the two heads (spawned facing
// each other, the same quarter-position layout Tron uses) swap places and
// both die on the same tick: the outcome does not depend on timing.
func TestSnake_HeadOnCrashIsADraw(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, snakeRegistry(t, fastSnake()))

	ann := join(t, addr, "ann")
	ann.press("1")
	ann.expect("Waiting for another player")
	ben := join(t, addr, "ben")
	ben.press("1")

	ann.expect("Starting in 3")
	ben.expect("Starting in 3")
	ann.expect("It's a draw.")
	ben.expect("It's a draw.")

	// The lobby's result screen works for Snake too.
	ann.expect("Press Enter to return to the menu.")
	ann.press("\r")
	ann.expect("q) Quit")
}

// TestSnake_PlayersWhoJoinDuringTheCountdownAreInTheSameRoom is the start
// rule seen from outside: with two players the countdown runs, and a third
// who arrives while it runs is seated in the same room.
func TestSnake_PlayersWhoJoinDuringTheCountdownAreInTheSameRoom(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, snakeRegistry(t, slowCountdownSnake()))

	amy := join(t, addr, "amy")
	amy.press("1")
	amy.expect("Waiting for another player")

	bex := join(t, addr, "bex")
	bex.press("1")
	amy.expect("bex") // amy now sees bex in the legend: same room

	cyd := join(t, addr, "cyd")
	cyd.press("1")
	amy.expect("cyd")
	bex.expect("cyd")
	cyd.expect("amy")
	cyd.expect("bex")
}

// TestSnake_LeavingDuringTheCountdownSendsTheOtherBackToWaiting: with a
// player gone the game is short of its two players again.
func TestSnake_LeavingDuringTheCountdownSendsTheOtherBackToWaiting(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, snakeRegistry(t, slowCountdownSnake()))

	amy := join(t, addr, "amy")
	amy.press("1")
	amy.expect("Waiting for another player")
	bex := join(t, addr, "bex")
	bex.press("1")
	amy.expect("Starting in 3")

	bex.press("q") // back to the menu
	bex.expect("q) Quit")
	amy.expect("Waiting for another player")
}

// TestSnake_VsBotPlaysARoundAndReturnsToTheMenu: one player, no friend. The
// countdown starts at once because the bot fills the second seat, the round
// runs on the real tick loop with the bot steering (and chasing food), and it
// ends with the usual result screen.
func TestSnake_VsBotPlaysARoundAndReturnsToTheMenu(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, snakeBotsRegistry(t, fastSnake()))
	ann := join(t, addr, "ann")
	ann.press("1")
	ann.expect("2) Play vs bot")
	ann.press("2")

	ann.expect("Starting in 3")
	ann.expect("*") // the shared pellet is on the board once play begins
	// Nobody steers, so ann and the bot (both facing each other on the same
	// row, the same quarter-position spawns Tron uses) meet and crash, the
	// same way TestSnake_HeadOnCrashIsADraw does with two humans. Unlike
	// Tron, standing still forever is not itself fatal here (there is no
	// wall), so the round's end depends on that meeting, not on a timeout.
	ann.expect("Press Enter to return to the menu.") // the round ran to its end
	ann.press("\r")
	ann.expect("q) Quit")
}
