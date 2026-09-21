package room

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// advisorGame is a fakeGame that can also play a seat: every bot is told to
// press the same key. It is only touched by the room goroutine.
type advisorGame struct {
	fakeGame
	advice input.Key
}

func (a *advisorGame) Advise(game.PlayerID) (input.Key, bool) { return a.advice, true }

const botID game.PlayerID = 100

func TestAddBot_RefusedWhenTheGameCannotPlay(t *testing.T) {
	t.Parallel()

	g := &fakeGame{} // no Advise method
	r := start(t, g, nil)

	err := r.AddBot(botID, "Bot")
	if !errors.Is(err, ErrNoBot) {
		t.Fatalf("AddBot() = %v, want ErrNoBot", err)
	}

	// The refusal must leave nothing behind: the game never saw the bot.
	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if len(g.joined) != 1 || g.joined[0] != 1 {
		t.Errorf("game.joined = %v, want only the human", g.joined)
	}
}

func TestAddBot_SeatsTheBotThroughGameJoin(t *testing.T) {
	t.Parallel()

	g := &advisorGame{advice: keyRune('z')}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "human", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	if err := r.AddBot(botID, "Bot"); err != nil {
		t.Fatalf("AddBot() = %v", err)
	}
	recv(t, s) // the human sees the change

	if want := []game.PlayerID{1, botID}; !reflect.DeepEqual(g.joined, want) {
		t.Errorf("joined = %v, want %v", g.joined, want)
	}
	if g.names[botID] != "Bot" {
		t.Errorf("bot joined as %q, want %q", g.names[botID], "Bot")
	}
}

func TestAddBot_ReturnsTheGamesRefusal(t *testing.T) {
	t.Parallel()

	g := &advisorGame{advice: keyRune('z')}
	g.joinErr = game.ErrFull
	r := start(t, g, nil)

	if err := r.AddBot(botID, "Bot"); !errors.Is(err, game.ErrFull) {
		t.Fatalf("AddBot() = %v, want game.ErrFull", err)
	}

	// A bot the game refused is not a bot: it must never be asked for a move.
	g.joinErr = nil
	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	r.Input(1, keyRune('a'))
	recv(t, s)
	for _, in := range g.inputs {
		if in.p == botID {
			t.Errorf("the refused bot pressed %v", in.k)
		}
	}
}

func TestAddBot_AfterTheRoomStoppedReturnsErrClosed(t *testing.T) {
	t.Parallel()

	r := New(&advisorGame{}, nil, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	cancel()
	<-done

	if err := r.AddBot(botID, "Bot"); !errors.Is(err, ErrClosed) {
		t.Errorf("AddBot() = %v, want ErrClosed", err)
	}
}

// The bot answers after every change, and its key reaches the game through
// Game.Input, the same call a human's key ends in.
func TestBot_AnswersAfterJoinAndAfterEachHumanKey(t *testing.T) {
	t.Parallel()

	g := &advisorGame{advice: keyRune('z')}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "human", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if err := r.AddBot(botID, "Bot"); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	r.Input(1, keyRune('a'))
	recv(t, s)

	want := []inputRec{
		{botID, keyRune('z')}, // when the bot sat down
		{1, keyRune('a')},     // the human's key...
		{botID, keyRune('z')}, // ...and the bot's answer to it
	}
	if !reflect.DeepEqual(g.inputs, want) {
		t.Errorf("game saw %v, want %v", g.inputs, want)
	}
}

// The bot's answer is in the same frame as the change that caused it, so a
// human never sees a half-updated game.
func TestBot_AnswerIsInTheSameFrameAsTheChange(t *testing.T) {
	t.Parallel()

	g := &advisorGame{advice: keyRune('z')}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "human", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if err := r.AddBot(botID, "Bot"); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	before := g.version
	r.Input(1, keyRune('a'))
	frame := recv(t, s) // exactly one frame for the key and the answer
	if want := before + 2; g.version != want {
		t.Fatalf("game version = %d, want %d (human key + bot key)", g.version, want)
	}
	if !strings.Contains(frame, fmt.Sprintf("v%d", g.version)) {
		t.Errorf("frame %q does not show the game after both keys (version %d)", frame, g.version)
	}
	select {
	case extra := <-s.frames:
		t.Errorf("a second frame arrived: %q", extra)
	default:
	}
}

func TestBot_AnswersAfterATick(t *testing.T) {
	t.Parallel()

	g := &advisorGame{advice: keyRune('z')}
	g.tickEvery = time.Millisecond
	tick := make(chan time.Time)
	r := start(t, g, tick)
	s := newSink()
	if err := r.Join(1, "human", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if err := r.AddBot(botID, "Bot"); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	botKeys := func() (n int) {
		for _, in := range g.inputs {
			if in.p == botID {
				n++
			}
		}
		return n
	}
	afterJoin := botKeys()

	tick <- time.Time{}
	recv(t, s)
	if g.ticks != 1 {
		t.Fatalf("ticks = %d, want 1", g.ticks)
	}
	if got := botKeys(); got != afterJoin+1 {
		t.Errorf("the bot pressed %d keys after the tick, want %d", got-afterJoin, 1)
	}
}

// Once the game is over the room stops asking, even if an advisor would still
// answer.
func TestBot_IsNotAskedOnceTheGameIsOver(t *testing.T) {
	t.Parallel()

	g := &advisorGame{advice: keyRune('z')}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "human", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if err := r.AddBot(botID, "Bot"); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	r.Input(1, input.Key{Kind: input.KindEnter}) // ends the fake game
	recv(t, s)

	want := []inputRec{{botID, keyRune('z')}, {1, input.Key{Kind: input.KindEnter}}}
	if !reflect.DeepEqual(g.inputs, want) {
		t.Errorf("game saw %v, want %v (no bot key after the game ended)", g.inputs, want)
	}
}

// A key sent in a bot's name from outside the room is ignored: bots are not
// players with a connection, so only the room presses their keys.
func TestBot_CannotBeSteeredFromOutside(t *testing.T) {
	t.Parallel()

	g := &advisorGame{advice: keyRune('z')}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "human", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if err := r.AddBot(botID, "Bot"); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	r.Input(botID, keyRune('!'))
	r.Input(1, keyRune('a')) // a later event proves the room handled the one before it
	recv(t, s)
	for _, in := range g.inputs {
		if in.k == keyRune('!') {
			t.Errorf("a key sent as the bot reached the game: %v", in)
		}
	}
}
