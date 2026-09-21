package lobby

import (
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/game/tron"
)

// tronRegistry offers real Tron, with the given settings, as menu choice 1. It
// has no bot, so picking it joins a room straight away; tronBotsRegistry is the
// same game with its bot.
func tronRegistry(t *testing.T, cfg tron.Config) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("tron", "Tron", func() game.Game { return noBot{tron.NewWithConfig(cfg)} })
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func tronBotsRegistry(t *testing.T, cfg tron.Config) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("tron", "Tron", func() game.Game { return tron.NewWithConfig(cfg) }, WithBots())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A fast game: a 20x12 board, a tick every 5 ms and a one-tick countdown per
// number, so a whole round takes a fraction of a second. The real ticker still
// drives it, so this exercises the room's tick loop end to end.
func fastTron() tron.Config {
	return tron.Config{Width: 20, Height: 12, TicksPerCount: 1, Tick: 5 * time.Millisecond}
}

// A game with a long countdown (0.5 s per number at a 5 ms tick), so a test
// has time to do things while it is still counting down.
func slowCountdownTron() tron.Config {
	return tron.Config{Width: 20, Height: 12, TicksPerCount: 100, Tick: 5 * time.Millisecond}
}

// TestTron_HeadOnCrashIsADraw plays a whole real-time round through the lobby
// and the tick loop. Nobody steers, so the two heads drive straight at each
// other and both die on the same tick: the outcome does not depend on timing.
func TestTron_HeadOnCrashIsADraw(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, tronRegistry(t, fastTron()))

	ann := join(t, addr, "ann")
	ann.press("1")
	ann.expect("Waiting for another player")
	ben := join(t, addr, "ben")
	ben.press("1")

	ann.expect("Starting in 3")
	ben.expect("Starting in 3")
	ann.expect("It's a draw.")
	ben.expect("It's a draw.")

	// The lobby's result screen works for a real-time game too.
	ann.expect("Press Enter to return to the menu.")
	ann.press("\r")
	ann.expect("q) Quit")
}

// TestTron_PlayersWhoJoinDuringTheCountdownAreInTheSameRoom is the start rule
// seen from outside: with two players the countdown runs, and a third who
// arrives while it runs is seated in the same room.
func TestTron_PlayersWhoJoinDuringTheCountdownAreInTheSameRoom(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, tronRegistry(t, slowCountdownTron()))

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

// TestTron_LeavingDuringTheCountdownSendsTheOtherBackToWaiting: with a
// player gone the game is short of its two players again.
func TestTron_LeavingDuringTheCountdownSendsTheOtherBackToWaiting(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, tronRegistry(t, slowCountdownTron()))

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

// TestTron_VsBotPlaysARoundAndReturnsToTheMenu: one player, no friend. The
// countdown starts at once because the bot fills the second seat, the round
// runs on the real tick loop with the bot steering, and it ends (the human
// never steers, so they crash sooner or later) with the usual result screen.
func TestTron_VsBotPlaysARoundAndReturnsToTheMenu(t *testing.T) {
	t.Parallel()

	addr := startArenaWith(t, tronBotsRegistry(t, fastTron()))
	ann := join(t, addr, "ann")
	ann.press("1")
	ann.expect("2) Play vs bot")
	ann.press("2")

	ann.expect("Starting in 3")
	ann.expect("Press Enter to return to the menu.") // the round ran to its end
	ann.press("\r")
	ann.expect("q) Quit")
}
