package lobby

import (
	"sync"
	"testing"
	"time"
)

// fakeClock stands in for time.AfterFunc: timers are recorded, and a test
// "lets time pass" by firing them by hand. Nothing here waits or sleeps.
type fakeClock struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (c *fakeClock) after(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	tm := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, tm)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !tm.stopped
		tm.stopped = true
		return was
	}
}

// started is how many timers have ever been created.
func (c *fakeClock) started() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// active is how many timers have not been stopped.
func (c *fakeClock) active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, tm := range c.timers {
		if !tm.stopped {
			n++
		}
	}
	return n
}

// fire runs timer i's function, as if its time had come. It runs even if the
// timer was stopped: a real timer can fire just before Stop is called, and the
// lobby has to cope with that.
func (c *fakeClock) fire(i int) {
	c.mu.Lock()
	f := c.timers[i].f
	c.mu.Unlock()
	f()
}

const fillWait = 10 * time.Second

// fillLobby runs a lobby with auto-fill on and a fake clock.
func fillLobby(t *testing.T, reg *Registry) (*Lobby, *fakeClock) {
	t.Helper()
	clock := &fakeClock{}
	return startLobby(t, reg, WithFillWait(fillWait), withAfterFunc(clock.after)), clock
}

func TestFill_ABotJoinsAWaitingPlayerAfterTheWait(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, ticTacToeBotsRegistry(t))
	ann := named(t, l, "ann")
	ann.press("11") // online
	ann.expect("Waiting for an opponent")
	l.Stats() // the lobby has handled everything so far, so the timer exists
	if clock.started() != 1 || clock.timers[0].d != fillWait {
		t.Fatalf("timers = %d (first %v), want one timer of %v", clock.started(), clock.timers[0].d, fillWait)
	}

	clock.fire(0)
	ann.expect("Opponent: Bot (O)")
	ann.expect("Your turn")
	if st := l.Stats(); st.Bots != 1 {
		t.Errorf("Bots = %d, want 1", st.Bots)
	}

	// The room is full, so it is no longer on offer: a newcomer starts their own.
	ben := named(t, l, "ben")
	ben.press("11")
	ben.expect("Waiting for an opponent")
	if st := l.Stats(); st.Rooms != 2 {
		t.Errorf("Rooms = %d, want 2", st.Rooms)
	}
}

func TestFill_NothingHappensBeforeTheTimerFires(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, ticTacToeBotsRegistry(t))
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for an opponent")

	ann.press("5") // a key while waiting changes nothing either
	l.Stats()
	if clock.active() != 1 {
		t.Errorf("active timers = %d, want the one still running", clock.active())
	}
}

func TestFill_ASecondPlayerArrivingCancelsIt(t *testing.T) {
	t.Parallel()

	// Tron, because its room still has free seats after two players join, so a
	// wrongly added bot would be accepted.
	l, clock := fillLobby(t, tronBotsRegistry(t, slowCountdownTron()))
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for another player")
	ben := named(t, l, "ben")
	ben.press("11")
	ann.expect("Starting in 3")
	l.Stats() // ann's frame is sent while ben is still joining; this waits until the lobby has finished with him

	if clock.active() != 0 {
		t.Errorf("active timers = %d, want 0: ann has company now", clock.active())
	}

	// Even a timer that fired just as ben arrived must not add a bot.
	clock.fire(0)
	if st := l.Stats(); st.Bots != 0 || st.Rooms != 1 || st.Players != 2 {
		t.Errorf("Stats() = %+v, want one room, two people and no bot", st)
	}
}

func TestFill_LeavingCancelsIt(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, ticTacToeBotsRegistry(t))
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for an opponent")
	ann.press("q")
	ann.expect("Pick a game:")

	if clock.active() != 0 {
		t.Errorf("active timers = %d, want 0: the room is gone", clock.active())
	}
	clock.fire(0) // a late fire for a room that no longer exists
	if st := l.Stats(); st.Rooms != 0 || st.Players != 1 {
		t.Errorf("Stats() = %+v, want no room", st)
	}
}

func TestFill_DisconnectCancelsIt(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, ticTacToeBotsRegistry(t))
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for an opponent")
	ann.disconnect()

	if st := l.Stats(); st.Rooms != 0 || st.Players != 0 {
		t.Fatalf("Stats() = %+v, want nothing left", st)
	}
	if clock.active() != 0 {
		t.Errorf("active timers = %d, want 0", clock.active())
	}
	clock.fire(0)
	if st := l.Stats(); st.Rooms != 0 {
		t.Errorf("Rooms = %d after a late fire, want 0", st.Rooms)
	}
}

func TestFill_OffUnlessAskedFor(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{}
	l := startLobby(t, ticTacToeBotsRegistry(t), withAfterFunc(clock.after)) // no WithFillWait
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for an opponent")
	l.Stats()

	if clock.started() != 0 {
		t.Errorf("timers started = %d, want none when auto-fill is off", clock.started())
	}
}

func TestFill_OnlyForGamesWithABot(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, ticTacToeRegistry(t)) // Tic-Tac-Toe without its bot
	ann := named(t, l, "ann")
	ann.press("1")
	ann.expect("Waiting for an opponent")
	l.Stats()

	if clock.started() != 0 {
		t.Errorf("timers started = %d, want none for a game without a bot", clock.started())
	}
}

func TestFill_NotForARoomThatAlreadyHasABot(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, ticTacToeBotsRegistry(t))
	ann := named(t, l, "ann")
	ann.press("12") // vs bot: a private room with a bot already in it
	ann.expect("Your turn")
	l.Stats()

	if clock.started() != 0 {
		t.Errorf("timers started = %d, want none for a private bot room", clock.started())
	}
}

// Tron starts before it is full, so a bot joining a lone player starts the
// countdown.
func TestFill_TronCountdownStartsWhenTheBotJoins(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, tronBotsRegistry(t, slowCountdownTron()))
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for another player")
	l.Stats()

	clock.fire(0)
	ann.expect("Starting in 3")
	ann.expect("Bot") // the legend lists the bot
}

// If the second player leaves during the countdown, the first is alone again
// and the wait for a bot starts over.
func TestFill_StartsOverWhenTheRoomIsBackToOnePlayer(t *testing.T) {
	t.Parallel()

	l, clock := fillLobby(t, tronBotsRegistry(t, slowCountdownTron()))
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for another player")
	ben := named(t, l, "ben")
	ben.press("11")
	ann.expect("Starting in 3")
	l.Stats()
	if clock.active() != 0 {
		t.Fatalf("active timers = %d, want 0 while two people are in the room", clock.active())
	}

	ben.press("q")
	ben.expect("Pick a game:")
	ann.expect("Waiting for another player")
	l.Stats()
	if clock.active() != 1 || clock.started() != 2 {
		t.Fatalf("started/active timers = %d/%d, want a second timer running", clock.started(), clock.active())
	}

	clock.fire(1)
	ann.expect("Starting in 3")
	ann.expect("Bot")
}
