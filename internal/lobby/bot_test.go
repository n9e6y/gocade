package lobby

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/game/tictactoe"
	"github.com/n9e6y/gocade/internal/input"
)

// noBot hides a game's Advise method (only the embedded game.Game interface is
// visible), so the game is one that cannot play a seat itself.
type noBot struct{ game.Game }

// ticTacToeBotsRegistry offers real Tic-Tac-Toe, with its bot, as menu
// choice 1.
func ticTacToeBotsRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("tictactoe", "Tic-Tac-Toe", func() game.Game { return tictactoe.New() }, WithBots())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// ---- registry ---------------------------------------------------------------

func TestRegistry_WithBots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		f       Factory
		opts    []EntryOption
		wantErr error
		want    bool // Entry.Bots
	}{
		{name: "no option means no bots", f: func() game.Game { return tictactoe.New() }},
		{name: "a game that can play may be offered vs bot", f: func() game.Game { return tictactoe.New() }, opts: []EntryOption{WithBots()}, want: true},
		{name: "a game that cannot play is refused", f: func() game.Game { return noBot{tictactoe.New()} }, opts: []EntryOption{WithBots()}, wantErr: ErrInvalidGame},
		{name: "a factory returning nothing is refused", f: stubFactory, opts: []EntryOption{WithBots()}, wantErr: ErrInvalidGame},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := NewRegistry()
			err := reg.Register("g", "Game", tt.f, tt.opts...)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Register() = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if len(reg.Entries()) != 0 {
					t.Error("a refused game was still registered")
				}
				return
			}
			e, _ := reg.Lookup("g")
			if e.Bots != tt.want {
				t.Errorf("Entry.Bots = %v, want %v", e.Bots, tt.want)
			}
		})
	}
}

// ---- the mode screen ----------------------------------------------------------

func TestMode_OnlyGamesWithABotAskHowToPlay(t *testing.T) {
	t.Parallel()

	t.Run("a game with a bot", func(t *testing.T) {
		t.Parallel()
		l := startLobby(t, ticTacToeBotsRegistry(t))
		p := named(t, l, "ann")
		p.press("1")
		p.expect("Tic-Tac-Toe")
		p.expect("1) Play online")
		p.expect("2) Play vs bot")
		p.expect("b) Back")
	})

	t.Run("a game without one joins straight away", func(t *testing.T) {
		t.Parallel()
		l := startLobby(t, ticTacToeRegistry(t))
		p := named(t, l, "ann")
		p.press("1")
		p.expect("Waiting for an opponent")
	})
}

func TestMode_BackAndQuitReturnToTheMenu(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"b", "B", "q"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			l := startLobby(t, ticTacToeBotsRegistry(t))
			p := named(t, l, "ann")
			p.press("1")
			p.expect("b) Back")
			p.press(key)
			p.expect("Pick a game:") // the menu again, and the connection is still open
			p.press("1")
			p.expect("b) Back") // and the menu still works
		})
	}
}

func TestMode_OtherKeysAreIgnored(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	p := named(t, l, "ann")
	p.press("1")
	p.expect("b) Back")

	p.press("3x\n9") // nothing here means anything
	if st := l.Stats(); st.Rooms != 0 {
		t.Fatalf("Rooms = %d, want 0: a stray key opened a room", st.Rooms)
	}
	p.press("2")
	p.expect("Opponent: Bot (O)") // the screen still answers to the real choices
}

func TestMode_CtrlCClosesTheConnection(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	p := named(t, l, "ann")
	p.press("1")
	p.expect("b) Back")
	p.press("\x03")
	p.expectClosed()
}

func TestMode_DisconnectLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	p := named(t, l, "ann")
	p.press("1")
	p.expect("b) Back")
	p.disconnect()

	if st := l.Stats(); st.Players != 0 || st.Rooms != 0 {
		t.Errorf("Stats() = %+v, want nothing left", st)
	}
}

// ---- playing against a bot -------------------------------------------------

// A single player starts a game against a bot at once, with nobody else
// needed, and the bot answers each move.
func TestVsBot_StartsAtOnceAndTheBotAnswers(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	p := named(t, l, "ann")
	p.press("12") // Tic-Tac-Toe, then vs bot
	p.expect("You are X (ann)")
	p.expect("Opponent: Bot (O)")
	p.expect("Your turn") // the human moves first

	p.press("5") // the center; the bot's reply to it is the first corner
	p.expect("O | 2 | 3")
	p.expect("Your turn")
}

// Playing badly loses: the bot punishes 1, 2, 4 (see the moves below), the
// result screen appears and Enter returns to the menu, like any game.
func TestVsBot_TheBotWinsAndTheResultFlowIsTheSame(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	p := named(t, l, "ann")
	p.press("12")
	p.expect("Your turn")

	p.press("1") // the bot takes the center
	p.expect("Your turn")
	p.press("2") // the bot must block cell 3
	p.expect("Your turn")
	p.press("4") // X threatens 7 but the bot takes it first: 3, 5, 7
	p.expect("You lose")
	p.expect("Press Enter to return to the menu.")

	p.press("\n")
	p.expect("Pick a game:")
	if st := l.Stats(); st.Rooms != 0 || st.Players != 1 {
		t.Errorf("Stats() = %+v, want the room closed and the player still connected", st)
	}
}

// A room with a bot belongs to its one human: q returns them to the menu and
// the room closes even though the bot is still "in" it.
func TestVsBot_LeavingClosesTheRoom(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	p := named(t, l, "ann")
	p.press("12")
	p.expect("Your turn")

	p.press("q")
	p.expect("Pick a game:")
	if st := l.Stats(); st.Rooms != 0 {
		t.Errorf("Rooms = %d, want 0", st.Rooms)
	}
}

func TestVsBot_DisconnectClosesTheRoom(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	p := named(t, l, "ann")
	p.press("12")
	p.expect("Your turn")
	p.disconnect()

	if st := l.Stats(); st.Players != 0 || st.Rooms != 0 {
		t.Errorf("Stats() = %+v, want nothing left", st)
	}
}

// openBotGame is a fake game with two to three seats that keeps taking players
// until it is full, and has a bot. After a human and a bot sit down it still has
// a free seat, like Tron during its countdown, which is exactly when a room
// being private matters.
type openBotGame struct{ fakeGame }

func (g *openBotGame) Seats() (min, max int)                  { return 2, 3 }
func (g *openBotGame) Advise(game.PlayerID) (input.Key, bool) { return input.Key{}, false }

func openBotRegistry(t *testing.T) *Registry {
	t.Helper()
	var created atomic.Int64
	reg := NewRegistry()
	err := reg.Register("open", "Open", func() game.Game {
		return &openBotGame{fakeGame{n: int(created.Add(1)), max: 3}}
	}, WithBots())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A bot room is private: someone who picks the game online is not put in with
// a player who is playing the bot, even though the room has a free seat.
func TestVsBot_TheRoomIsPrivate(t *testing.T) {
	t.Parallel()

	l := startLobby(t, openBotRegistry(t))
	ann := named(t, l, "ann")
	ann.press("12")
	ann.expect("ann,Bot") // one bot is enough: the game needs two players

	ben := named(t, l, "ben")
	ben.press("11")     // online
	ben.expect(": ben") // first in his room; in ann's he would be listed after Bot

	if st := l.Stats(); st.Rooms != 2 {
		t.Errorf("Rooms = %d, want 2: ben must have a room of his own", st.Rooms)
	}
}

// Two people who play a bot at the same time each get their own game.
func TestVsBot_EachPlayerGetsTheirOwnBot(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	ann, ben := named(t, l, "ann"), named(t, l, "ben")
	ann.press("12")
	ben.press("12")
	ann.expect("You are X (ann)")
	ben.expect("You are X (ben)")

	if st := l.Stats(); st.Rooms != 2 || st.Players != 2 {
		t.Errorf("Stats() = %+v, want 2 rooms and 2 players", st)
	}
}

// Someone waiting online is not disturbed by another player's bot game, and
// leaving the bot game does not close the waiting room.
func TestVsBot_DoesNotDisturbAWaitingRoom(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeBotsRegistry(t))
	ann := named(t, l, "ann")
	ann.press("11")
	ann.expect("Waiting for an opponent")

	ben := named(t, l, "ben")
	ben.press("12")
	ben.expect("Your turn")
	ben.press("q")
	ben.expect("Pick a game:")

	if st := l.Stats(); st.Rooms != 1 {
		t.Fatalf("Rooms = %d, want 1 (ann's)", st.Rooms)
	}
	cat := named(t, l, "cat")
	cat.press("11")
	ann.expect("Opponent: cat (O)") // cat was matched with ann, the waiting player
}
