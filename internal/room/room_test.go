package room

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/render"
)

// testTimeout bounds every wait in these tests. Nothing sleeps to "let the
// room catch up": tests wait for a frame to arrive, with this as the limit.
const testTimeout = 5 * time.Second

// fakeGame records what the Room does to it. It is only touched by the
// Room's goroutine; tests read it after receiving a frame, and the channel
// receive is what orders the two.
type fakeGame struct {
	tickEvery time.Duration
	joinErr   error

	state   game.State
	version int // bumped on every change; drawn by View so frames differ
	joined  []game.PlayerID
	names   map[game.PlayerID]string // the name each player joined with
	left    []game.PlayerID
	inputs  []inputRec
	ticks   int
}

type inputRec struct {
	p game.PlayerID
	k input.Key
}

func (f *fakeGame) Name() string             { return "fake" }
func (f *fakeGame) Seats() (min, max int)    { return 1, 4 }
func (f *fakeGame) TickEvery() time.Duration { return f.tickEvery }
func (f *fakeGame) State() game.State        { return f.state }
func (f *fakeGame) Outcome() game.Outcome    { return game.Outcome{} }

func (f *fakeGame) Join(p game.PlayerID, name string) error {
	if f.joinErr != nil {
		return f.joinErr
	}
	f.joined = append(f.joined, p)
	if f.names == nil {
		f.names = make(map[game.PlayerID]string)
	}
	f.names[p] = name
	f.state = game.StateRunning
	f.version++
	return nil
}

func (f *fakeGame) Leave(p game.PlayerID) {
	f.left = append(f.left, p)
	f.version++
}

// Input records the key. Enter ends the game, so tests can reach Over.
func (f *fakeGame) Input(p game.PlayerID, k input.Key) {
	f.inputs = append(f.inputs, inputRec{p, k})
	if k.Kind == input.KindEnter {
		f.state = game.StateOver
	}
	f.version++
}

func (f *fakeGame) Tick() {
	f.ticks++
	f.version++
}

// View draws "p<id> v<version>", so a frame shows who it was made for.
func (f *fakeGame) View(p game.PlayerID) *render.Canvas {
	c := render.NewCanvas(20, 1)
	c.Text(0, 0, fmt.Sprintf("p%d v%d", p, f.version), render.Default)
	return c
}

// fakeSink collects frames. Send never blocks, like a real Session.
type fakeSink struct{ frames chan []byte }

func newSink() *fakeSink { return &fakeSink{frames: make(chan []byte, 64)} }

func (s *fakeSink) Send(frame []byte) bool {
	select {
	case s.frames <- frame:
		return true
	default:
		return false
	}
}

// dropSink models a client too slow to read: every frame is dropped.
type dropSink struct{}

func (dropSink) Send([]byte) bool { return false }

// recv waits for the next frame on s and returns its text.
func recv(t *testing.T, s *fakeSink) string {
	t.Helper()
	select {
	case f := <-s.frames:
		return string(f)
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for a frame")
		return ""
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// start runs a Room on g and stops it when the test ends.
func start(t *testing.T, g game.Game, tick <-chan time.Time) *Room {
	t.Helper()
	r := New(g, tick, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(testTimeout):
			t.Error("Run did not return after cancel")
		}
	})
	return r
}

func keyRune(c rune) input.Key { return input.Key{Kind: input.KindRune, Rune: c} }

func TestJoin_SendsFrameToJoiner(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	s := newSink()

	if err := r.Join(1, "p1", s); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got := recv(t, s); !strings.Contains(got, "p1 v1") {
		t.Errorf("frame = %q, want it to show p1 v1", got)
	}
	if len(g.joined) != 1 || g.joined[0] != 1 {
		t.Errorf("game saw joins %v, want [1]", g.joined)
	}
}

func TestJoin_PassesTheNameToTheGame(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)

	if err := r.Join(7, "alice", newSink()); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got := g.names[7]; got != "alice" {
		t.Errorf("game saw name %q for player 7, want %q", got, "alice")
	}
}

func TestJoin_RejectedReturnsTheGameError(t *testing.T) {
	t.Parallel()

	g := &fakeGame{joinErr: game.ErrFull}
	r := start(t, g, nil)
	s := newSink()

	if err := r.Join(1, "p1", s); !errors.Is(err, game.ErrFull) {
		t.Fatalf("Join error = %v, want ErrFull", err)
	}
	if n := len(s.frames); n != 0 {
		t.Errorf("rejected player received %d frames, want 0", n)
	}

	// A rejected player is not a member, so its input must not reach the game.
	r.Input(1, keyRune('5'))
	g.joinErr = nil // safe: the room is idle until the next event
	other := newSink()
	if err := r.Join(2, "p2", other); err != nil {
		t.Fatalf("Join(2): %v", err)
	}
	recv(t, other)
	if len(g.inputs) != 0 {
		t.Errorf("game saw inputs %v from a rejected player", g.inputs)
	}
}

func TestInput_ForwardedAndEveryoneGetsAFrame(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	a, b := newSink(), newSink()
	if err := r.Join(1, "p1", a); err != nil {
		t.Fatal(err)
	}
	if err := r.Join(2, "p2", b); err != nil {
		t.Fatal(err)
	}
	recv(t, a) // join frame
	recv(t, a) // frame caused by player 2 joining
	recv(t, b)

	r.Input(1, keyRune('5'))

	fa, fb := recv(t, a), recv(t, b)
	if want := []inputRec{{1, keyRune('5')}}; len(g.inputs) != 1 || g.inputs[0] != want[0] {
		t.Errorf("game saw inputs %v, want %v", g.inputs, want)
	}

	// Each player got their own view, at the same version.
	if !strings.Contains(fa, "p1 v3") {
		t.Errorf("player 1 frame = %q, want it to contain %q", fa, "p1 v3")
	}
	if !strings.Contains(fb, "p2 v3") {
		t.Errorf("player 2 frame = %q, want it to contain %q", fb, "p2 v3")
	}
}

func TestInput_FromNonMemberIsIgnored(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	r.Input(99, keyRune('x')) // stranger: dropped, no frame
	r.Input(1, keyRune('y'))  // member: forwarded
	recv(t, s)

	if len(g.inputs) != 1 || g.inputs[0].p != 1 {
		t.Errorf("game saw inputs %v, want only player 1's", g.inputs)
	}
}

func TestLeave(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	leaver, stayer := newSink(), newSink()
	if err := r.Join(1, "p1", leaver); err != nil {
		t.Fatal(err)
	}
	if err := r.Join(2, "p2", stayer); err != nil {
		t.Fatal(err)
	}
	recv(t, leaver)
	recv(t, leaver)
	recv(t, stayer)

	r.Leave(1)
	if got := recv(t, stayer); !strings.Contains(got, "p2 ") {
		t.Errorf("remaining player frame = %q, want a p2 view", got)
	}
	if len(g.left) != 1 || g.left[0] != 1 {
		t.Errorf("game saw leaves %v, want [1]", g.left)
	}

	// A later event must not reach the player who left.
	r.Input(2, keyRune('z'))
	recv(t, stayer)
	if n := len(leaver.frames); n != 0 {
		t.Errorf("player who left received %d more frames, want 0", n)
	}
}

func TestLeave_UnknownPlayerIsANoop(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	r.Leave(99)
	r.Input(1, keyRune('a'))
	recv(t, s)

	if len(g.left) != 0 {
		t.Errorf("game saw leaves %v for a player who never joined", g.left)
	}
}

// TestLeave_ReturnsOnlyAfterTheRoomProcessedIt: the lobby sends the menu right
// after Leave returns, so if Leave returned early the room could still send a
// stale game frame on top of the menu. Reading the game's records straight
// after Leave, with no frame received in between, proves the wait (and the
// race detector would flag it if Leave did not wait).
func TestLeave_ReturnsOnlyAfterTheRoomProcessedIt(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}

	r.Leave(1)
	if len(g.left) != 1 || g.left[0] != 1 {
		t.Errorf("game saw leaves %v right after Leave returned, want [1]", g.left)
	}

	r.Leave(99) // unknown player: must return, not hang
}

func TestOver_ClosesWhenTheGameEndsAndOnlyOnce(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	select {
	case <-r.Over():
		t.Fatal("Over() closed before the game ended")
	default:
	}

	r.Input(1, input.Key{Kind: input.KindEnter}) // ends the fake game
	recv(t, s)
	select {
	case <-r.Over():
	case <-time.After(testTimeout):
		t.Fatal("Over() not closed after the game ended")
	}

	// More events after the end must not close the channel a second time.
	r.Input(1, keyRune('x'))
	recv(t, s)
	r.Leave(1)
}

func TestTick_DrivesTheGameOncePerTick(t *testing.T) {
	t.Parallel()

	g := &fakeGame{tickEvery: time.Millisecond}
	tick := make(chan time.Time) // unbuffered: a send returns once the room has taken the tick
	r := start(t, g, tick)
	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)

	for want := 1; want <= 3; want++ {
		tick <- time.Time{}
		recv(t, s)
		if g.ticks != want {
			t.Fatalf("after %d ticks the game saw %d", want, g.ticks)
		}
	}
}

func TestTick_IgnoredUnlessRunning(t *testing.T) {
	t.Parallel()

	g := &fakeGame{tickEvery: time.Millisecond}
	tick := make(chan time.Time)
	r := start(t, g, tick)

	// Nobody has joined, so the game is Waiting: a tick must not reach it.
	tick <- time.Time{}

	s := newSink()
	if err := r.Join(1, "p1", s); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if g.ticks != 0 {
		t.Errorf("game ticked %d times while waiting", g.ticks)
	}

	// Enter ends the fake game; further ticks must not reach it either.
	r.Input(1, input.Key{Kind: input.KindEnter})
	recv(t, s)
	tick <- time.Time{}
	r.Input(1, keyRune('x'))
	recv(t, s)
	if g.ticks != 0 {
		t.Errorf("game ticked %d times after it was over", g.ticks)
	}
}

func TestSlowPlayerDoesNotBlockTheRoom(t *testing.T) {
	t.Parallel()

	g := &fakeGame{}
	r := start(t, g, nil)
	if err := r.Join(1, "p1", dropSink{}); err != nil { // its Send always reports a drop
		t.Fatal(err)
	}
	healthy := newSink()
	if err := r.Join(2, "p2", healthy); err != nil {
		t.Fatal(err)
	}
	recv(t, healthy)

	r.Input(2, keyRune('a'))
	recv(t, healthy) // the room kept going despite the dropped frames
}

func TestRun_StopsOnCancelAndLaterCallsDoNotBlock(t *testing.T) {
	t.Parallel()

	r := New(&fakeGame{}, nil, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(stopped)
	}()

	cancel()
	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("Run did not return after cancel")
	}

	// The room is gone: none of these may block.
	finished := make(chan error, 1)
	go func() {
		r.Leave(1)
		r.Input(1, keyRune('a'))
		finished <- r.Join(1, "p1", newSink())
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("Join after close = %v, want ErrClosed", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("calls on a closed room blocked")
	}
}

func TestTickerFor(t *testing.T) {
	t.Parallel()

	t.Run("turn-based game has no ticker", func(t *testing.T) {
		t.Parallel()
		tick, stop := TickerFor(&fakeGame{tickEvery: 0})
		if tick != nil {
			t.Error("tick channel should be nil for a turn-based game")
		}
		stop() // must be safe to call
	})

	t.Run("real-time game gets a working ticker", func(t *testing.T) {
		t.Parallel()
		tick, stop := TickerFor(&fakeGame{tickEvery: time.Millisecond})
		defer stop()
		if tick == nil {
			t.Fatal("tick channel is nil for a real-time game")
		}
		select {
		case <-tick:
		case <-time.After(testTimeout):
			t.Fatal("ticker never ticked")
		}
	})
}
