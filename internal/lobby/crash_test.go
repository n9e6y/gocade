package lobby

import (
	"sync/atomic"
	"testing"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// crashGame is a two-player game with a bug: pressing '!' makes it panic. Its
// Join panics for a player named "joinbug".
type crashGame struct{ fakeGame }

func (g *crashGame) Input(p game.PlayerID, k input.Key) {
	if k.Kind == input.KindRune && k.Rune == '!' {
		panic("boom")
	}
}

func (g *crashGame) Join(p game.PlayerID, name string) error {
	if name == "joinbug" {
		panic("boom in join")
	}
	return g.fakeGame.Join(p, name)
}

func crashRegistry(t *testing.T) *Registry {
	t.Helper()
	var created atomic.Int64
	reg := NewRegistry()
	err := reg.Register("crash", "Crashy", func() game.Game {
		return &crashGame{fakeGame{n: int(created.Add(1)), max: 2}}
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A game that panics takes down its own room and nothing else: the players in
// it get the menu back with an explanation, another room carries on, and the
// lobby keeps working.
func TestCrash_OnlyThatRoomEnds(t *testing.T) {
	t.Parallel()

	l := startLobby(t, crashRegistry(t))
	ann, ben := named(t, l, "ann"), named(t, l, "ben")
	ann.press("1")
	ben.press("1")
	ben.expect("ann,ben")

	cat, dan := named(t, l, "cat"), named(t, l, "dan")
	cat.press("1")
	dan.press("1")
	dan.expect("cat,dan")

	ann.press("!") // the bug
	ann.expect("That game crashed")
	ben.expect("That game crashed")

	// The other room is untouched, and the lobby holds no trace of the dead one.
	if st := l.Stats(); st.Rooms != 1 || st.Players != 4 {
		t.Errorf("Stats() = %+v, want the crashed room gone and the other still open", st)
	}

	// The two who crashed are at a working menu and can play again.
	ann.press("1")
	ben.press("1")
	ben.expect("ann,ben")
}

func TestCrash_APlayerCanLeaveAfterTheirRoomCrashed(t *testing.T) {
	t.Parallel()

	l := startLobby(t, crashRegistry(t))
	ann, ben := named(t, l, "ann"), named(t, l, "ben")
	ann.press("1")
	ben.press("1")
	ben.expect("ann,ben")

	ann.press("!")
	ann.expect("That game crashed")
	ben.disconnect() // a departure racing with the crash must be harmless
	ann.disconnect()

	if st := l.Stats(); st.Rooms != 0 || st.Players != 0 {
		t.Errorf("Stats() = %+v, want nothing left", st)
	}
}

// A crash while someone is joining sends them back to the menu with the usual
// "could not join" message instead of leaving them hanging.
func TestCrash_DuringJoin(t *testing.T) {
	t.Parallel()

	l := startLobby(t, crashRegistry(t))
	p := named(t, l, "joinbug")
	p.press("1")
	p.expect("Could not join that game")

	if st := l.Stats(); st.Rooms != 0 || st.Players != 1 {
		t.Errorf("Stats() = %+v, want no room and the player still connected", st)
	}

	// The lobby is healthy: an ordinary pair can play.
	ann, ben := named(t, l, "ann"), named(t, l, "ben")
	ann.press("1")
	ben.press("1")
	ben.expect("ann,ben")
}
