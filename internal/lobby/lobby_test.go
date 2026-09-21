package lobby

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/game/tictactoe"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/render"
)

// testTimeout bounds every wait. Tests never sleep to let the lobby "catch
// up": they wait for the text they expect, or for a Stats round trip.
const testTimeout = 5 * time.Second

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- fake game -------------------------------------------------------

// fakeGame is a tiny game for matchmaking tests. Its screen says which room
// it is and who is in it ("room 2: p3,p4"), so a test can see from a
// player's screen exactly who they were matched with.
type fakeGame struct {
	n       int // creation order, starting at 1
	max     int
	startAt int // if > 0, the game starts once this many have joined, before it is full (like Tron)
	ids     []game.PlayerID
	names   []string
	state   game.State
}

func (g *fakeGame) Name() string { return "fake" }
func (g *fakeGame) Seats() (min, max int) {
	if g.startAt > 0 {
		return g.startAt, g.max
	}
	return g.max, g.max
}
func (g *fakeGame) TickEvery() time.Duration { return 0 }
func (g *fakeGame) Tick()                    {}
func (g *fakeGame) State() game.State        { return g.state }
func (g *fakeGame) Outcome() game.Outcome    { return game.Outcome{Draw: true} }

func (g *fakeGame) Join(p game.PlayerID, name string) error {
	switch {
	case g.state == game.StateOver:
		return game.ErrOver
	case g.state == game.StateRunning && g.startAt > 0:
		return game.ErrStarted // started before it was full: closed to newcomers
	case len(g.ids) == g.max:
		return game.ErrFull
	}
	g.ids = append(g.ids, p)
	g.names = append(g.names, name)
	if len(g.ids) == g.max || (g.startAt > 0 && len(g.ids) >= g.startAt) {
		g.state = game.StateRunning
	}
	return nil
}

func (g *fakeGame) Leave(p game.PlayerID) {
	for i, id := range g.ids {
		if id != p {
			continue
		}
		if g.state == game.StateRunning {
			g.state = game.StateOver // a mid-game leave ends it
			return
		}
		g.ids = append(g.ids[:i], g.ids[i+1:]...)
		g.names = append(g.names[:i], g.names[i+1:]...)
		return
	}
}

func (g *fakeGame) Input(game.PlayerID, input.Key) {}

func (g *fakeGame) View(game.PlayerID) *render.Canvas {
	c := render.NewCanvas(40, 2)
	c.Text(0, 0, fmt.Sprintf("room %d: %s", g.n, strings.Join(g.names, ",")), render.Default)
	return c
}

// fakeRegistry offers four fake games, which are menu choices 1 to 4: "A" and
// "B" (2 seats), "T" (3 seats, starts when full) and "S" (3 seats, but it
// starts as soon as 2 have joined, so a third player is turned away).
func fakeRegistry(t *testing.T) *Registry {
	t.Helper()
	var created atomic.Int64
	factory := func(max, startAt int) Factory {
		return func() game.Game {
			return &fakeGame{n: int(created.Add(1)), max: max, startAt: startAt}
		}
	}
	reg := NewRegistry()
	for _, e := range []struct {
		name    string
		title   string
		max     int
		startAt int
	}{{"a", "Game A", 2, 0}, {"b", "Game B", 2, 0}, {"t", "Game T", 3, 0}, {"s", "Game S", 3, 2}} {
		if err := reg.Register(e.name, e.title, factory(e.max, e.startAt)); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// ticTacToeRegistry offers the real game as menu choice 1, without a bot, so
// picking it joins a room straight away. These tests are about matchmaking
// between people; bot_test.go covers the bot.
func ticTacToeRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.Register("tictactoe", "Tic-Tac-Toe", func() game.Game { return noBot{tictactoe.New()} })
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// ---- fake connection and player ---------------------------------------

// fakeConn stands in for a server.Session: it collects the frames the lobby
// sends and records whether the lobby asked to close it.
type fakeConn struct {
	id     uint64
	frames chan []byte
	closed chan struct{}
	once   sync.Once
}

var nextConnID atomic.Uint64

func newFakeConn() *fakeConn {
	return &fakeConn{
		id:     nextConnID.Add(1),
		frames: make(chan []byte, 256),
		closed: make(chan struct{}),
	}
}

func (c *fakeConn) ID() uint64 { return c.id }

// Send never blocks, like a real Session: a full buffer drops the frame.
func (c *fakeConn) Send(frame []byte) bool {
	select {
	case c.frames <- frame:
		return true
	default:
		return false
	}
}

func (c *fakeConn) Close() { c.once.Do(func() { close(c.closed) }) }

// ansi matches a complete terminal escape sequence such as a color change.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// testPlayer is a fake person at a terminal.
type testPlayer struct {
	t   *testing.T
	l   *Lobby
	c   *fakeConn
	buf []byte // received, escape codes stripped, not yet consumed by expect
}

func newPlayer(t *testing.T, l *Lobby) *testPlayer {
	t.Helper()
	p := &testPlayer{t: t, l: l, c: newFakeConn()}
	l.connect(p.c)
	return p
}

// keysOf turns a script into key presses: '\n' is Enter, '\b' Backspace,
// '\x03' Ctrl-C, anything else a typed character.
func keysOf(script string) []input.Key {
	var keys []input.Key
	for _, c := range script {
		switch c {
		case '\n':
			keys = append(keys, input.Key{Kind: input.KindEnter})
		case '\b':
			keys = append(keys, input.Key{Kind: input.KindBackspace})
		case '\x03':
			keys = append(keys, input.Key{Kind: input.KindCtrlC})
		default:
			keys = append(keys, input.Key{Kind: input.KindRune, Rune: c})
		}
	}
	return keys
}

func (p *testPlayer) press(script string) {
	p.t.Helper()
	p.l.onKeys(p.c, keysOf(script))
}

// expect waits until text has been drawn, then discards everything up to the
// end of it, so an old screen cannot satisfy a later expectation.
func (p *testPlayer) expect(text string) {
	p.t.Helper()
	deadline := time.After(testTimeout)
	for {
		if i := bytes.Index(p.buf, []byte(text)); i >= 0 {
			p.buf = p.buf[i+len(text):]
			return
		}
		select {
		case f := <-p.c.frames:
			p.buf = ansi.ReplaceAll(append(p.buf, f...), nil)
		case <-deadline:
			p.t.Fatalf("timed out waiting for %q; screen so far: %q", text, p.buf)
		}
	}
}

func (p *testPlayer) expectClosed() {
	p.t.Helper()
	select {
	case <-p.c.closed:
	case <-time.After(testTimeout):
		p.t.Fatal("the lobby did not close the connection")
	}
}

// disconnect is what the server does when a client goes away.
func (p *testPlayer) disconnect() { p.l.disconnect(p.c) }

// named connects a player and gets them past the nickname screen.
func named(t *testing.T, l *Lobby, name string) *testPlayer {
	t.Helper()
	p := newPlayer(t, l)
	p.press(name + "\n")
	p.expect("q) Quit") // the last line of the menu
	return p
}

// startLobby runs a Lobby and stops it when the test ends.
func startLobby(t *testing.T, reg *Registry, opts ...Option) *Lobby {
	t.Helper()
	l := New(reg, discardLogger(), opts...)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(stopped)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Error("Lobby.Run did not return after cancel")
		}
	})
	return l
}

// ---- nickname and menu -------------------------------------------------

func TestNickname_PromptEchoesWhatIsTyped(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	p := newPlayer(t, l)

	p.expect("Choose a nickname")
	p.press("bo")
	p.expect("> bo_")
	p.press("x\b") // typing then deleting redraws without the x
	p.expect("> bo_")
}

func TestNickname_EmptyNameIsRefused(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	p := newPlayer(t, l)

	p.press("\n")
	p.expect("cannot be empty")
	p.press("   \n")
	p.expect("cannot be empty")
}

func TestNickname_LettersUsedByGamesAreStillLetters(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	p := newPlayer(t, l)

	p.press("quinn\n") // starts with q, contains w/a/s/d letters elsewhere
	p.expect("Hello, quinn!")
}

func TestMenu_ListsGamesInRegistrationOrder(t *testing.T) {
	t.Parallel()

	l := startLobby(t, fakeRegistry(t))
	p := newPlayer(t, l)
	p.press("bob\n")

	p.expect("1) Game A")
	p.expect("2) Game B")
	p.expect("3) Game T")
	p.expect("q) Quit")
}

func TestMenu_QuitClosesTheConnection(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	p := named(t, l, "bob")

	p.press("q")
	p.expectClosed()
}

func TestCtrlC_ClosesFromAnyScreen(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))

	atNickname := newPlayer(t, l)
	atNickname.press("\x03")
	atNickname.expectClosed()

	inRoom := named(t, l, "bob")
	inRoom.press("1")
	inRoom.expect("Waiting for an opponent")
	inRoom.press("\x03")
	inRoom.expectClosed()
}

// ---- matchmaking -------------------------------------------------------

// act is one player pressing keys. Menu choices are 1 = Game A, 2 = Game B,
// 3 = Game T (three seats), and q leaves a room.
type act struct {
	player int // 1-based
	keys   string
}

func TestMatchmaking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		players   int
		acts      []act
		want      map[int]string // player -> text their screen must show
		wantRooms int
	}{
		{
			name:    "two players share a room",
			players: 2,
			acts:    []act{{1, "1"}, {2, "1"}},
			want:    map[int]string{1: "room 1: p1,p2", 2: "room 1: p1,p2"},
			// the room stays open (the game is running), so 1 room
			wantRooms: 1,
		},
		{
			name:      "the third player waits in a new room",
			players:   3,
			acts:      []act{{1, "1"}, {2, "1"}, {3, "1"}},
			want:      map[int]string{1: "room 1: p1,p2", 2: "room 1: p1,p2", 3: "room 2: p3"},
			wantRooms: 2,
		},
		{
			name:    "four players make two rooms",
			players: 4,
			acts:    []act{{1, "1"}, {2, "1"}, {3, "1"}, {4, "1"}},
			want: map[int]string{
				1: "room 1: p1,p2", 2: "room 1: p1,p2",
				3: "room 2: p3,p4", 4: "room 2: p3,p4",
			},
			wantRooms: 2,
		},
		{
			name:    "different games never mix",
			players: 3,
			acts:    []act{{1, "1"}, {2, "2"}, {3, "1"}},
			want:    map[int]string{1: "room 1: p1,p3", 3: "room 1: p1,p3", 2: "room 2: p2"},
			// room 2 (game B) has p2 waiting alone
			wantRooms: 2,
		},
		{
			name:    "a three-seat room fills in join order",
			players: 4,
			acts:    []act{{1, "3"}, {2, "3"}, {3, "3"}, {4, "3"}},
			want: map[int]string{
				1: "room 1: p1,p2,p3", 2: "room 1: p1,p2,p3", 3: "room 1: p1,p2,p3",
				4: "room 2: p4",
			},
			wantRooms: 2,
		},
		{
			name:    "a lone waiting player who leaves closes the room",
			players: 2,
			acts:    []act{{1, "1"}, {1, "q"}, {2, "1"}},
			// p1's room 1 was closed when p1 left, so p2 gets a fresh room 2
			want:      map[int]string{2: "room 2: p2"},
			wantRooms: 1,
		},
		{
			name:    "a player who left can rejoin someone else's room",
			players: 2,
			acts:    []act{{1, "1"}, {1, "q"}, {2, "1"}, {1, "1"}},
			want:    map[int]string{1: "room 2: p2,p1", 2: "room 2: p2,p1"},
			// both players are in the running game in room 2
			wantRooms: 1,
		},
		{
			name:    "a room that has already started is skipped for a new one",
			players: 3,
			acts:    []act{{1, "4"}, {2, "4"}, {3, "4"}},
			// game S starts at two players even though it has three seats, so p3
			// is turned away by room 1 and gets a room of their own
			want:      map[int]string{1: "room 1: p1,p2", 2: "room 1: p1,p2", 3: "room 2: p3"},
			wantRooms: 2,
		},
		{
			name:    "a started room is never offered again",
			players: 5,
			acts:    []act{{1, "4"}, {2, "4"}, {3, "4"}, {4, "4"}, {5, "4"}},
			want: map[int]string{
				1: "room 1: p1,p2", 2: "room 1: p1,p2",
				3: "room 2: p3,p4", 4: "room 2: p3,p4",
				5: "room 3: p5",
			},
			wantRooms: 3,
		},
		{
			name:      "an out-of-range digit does nothing",
			players:   1,
			acts:      []act{{1, "9"}, {1, "0"}},
			want:      map[int]string{},
			wantRooms: 0,
		},
		{
			name:    "a player who leaves a waiting three-seat room leaves the others waiting",
			players: 3,
			acts:    []act{{1, "3"}, {2, "3"}, {1, "q"}, {3, "3"}},
			// room 1 keeps p2, loses p1, then takes p3
			want:      map[int]string{2: "room 1: p2,p3", 3: "room 1: p2,p3"},
			wantRooms: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := startLobby(t, fakeRegistry(t))
			players := make([]*testPlayer, tt.players+1) // index 0 unused
			for i := 1; i <= tt.players; i++ {
				players[i] = named(t, l, fmt.Sprintf("p%d", i))
			}

			for _, a := range tt.acts {
				players[a.player].press(a.keys)
				// Stats is answered by the lobby goroutine after every event
				// posted before it, so once it returns this act is complete.
				l.Stats()
			}

			for i, text := range tt.want {
				players[i].expect(text)
			}
			if got := l.Stats().Rooms; got != tt.wantRooms {
				t.Errorf("open rooms = %d, want %d", got, tt.wantRooms)
			}
		})
	}
}

// ---- full flows with the real game -------------------------------------

func TestFlow_PlayAFullGameThenPlayAgain(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	bob := named(t, l, "bob")
	alice := named(t, l, "alice")

	bob.press("1")
	bob.expect("Waiting for an opponent")
	alice.press("1")
	alice.expect("Opponent: bob (X)")
	bob.expect("Opponent: alice (O)")

	// bob takes the top row; alice takes the middle row's first two cells.
	bob.press("1")
	alice.press("4")
	bob.press("2")
	alice.press("5")
	bob.press("3")
	bob.expect("You win!")
	alice.expect("You lose")

	// The result stays on screen until the player asks for the menu.
	bob.expect("Press Enter to return to the menu.")
	alice.expect("Press Enter to return to the menu.")
	bob.press("\n")
	alice.press("\n")
	bob.expect("q) Quit")
	alice.expect("q) Quit")
	if got := l.Stats().Rooms; got != 0 {
		t.Errorf("finished game left %d rooms open, want 0", got)
	}

	// And they can play again without restarting anything.
	alice.press("1")
	alice.expect("Waiting for an opponent")
	bob.press("1")
	bob.expect("Opponent: alice (X)") // alice arrived first, so alice is X this time
}

func TestFlow_QLeavesAWaitingRoom(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	bob := named(t, l, "bob")
	alice := named(t, l, "alice")

	bob.press("1")
	bob.expect("Waiting for an opponent")
	bob.press("q")
	bob.expect("q) Quit") // back at the menu, not disconnected
	select {
	case <-bob.c.closed:
		t.Fatal("q in a room closed the connection; it should return to the menu")
	default:
	}

	// bob is gone from the room, so alice must not be matched with him.
	alice.press("1")
	alice.expect("Waiting for an opponent")
	if got := l.Stats().Rooms; got != 1 {
		t.Errorf("open rooms = %d, want 1", got)
	}
}

func TestFlow_QMidGameForfeitsAndBothReturnToTheMenu(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	bob := named(t, l, "bob")
	alice := named(t, l, "alice")
	bob.press("1")
	alice.press("1")
	bob.expect("Opponent: alice (O)")

	alice.press("q")
	alice.expect("q) Quit") // the leaver goes straight to the menu

	bob.expect("Opponent left, you win by forfeit")
	bob.expect("Press Enter to return to the menu.")
	bob.press("\n")
	bob.expect("q) Quit")

	if got := l.Stats().Rooms; got != 0 {
		t.Errorf("open rooms = %d, want 0", got)
	}
}

func TestFlow_DisconnectMidGameForfeits(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	bob := named(t, l, "bob")
	alice := named(t, l, "alice")
	bob.press("1")
	alice.press("1")
	alice.expect("Opponent: bob (X)")

	bob.disconnect() // the nc process was killed

	alice.expect("Opponent left, you win by forfeit")
	alice.expect("Press Enter to return to the menu.")
	alice.press("\n")
	alice.expect("q) Quit")

	st := l.Stats()
	if st.Players != 1 || st.Rooms != 0 {
		t.Errorf("Stats() = %+v, want 1 player and 0 rooms", st)
	}
}

func TestFlow_DisconnectAtEveryStageLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		prep func(t *testing.T, l *Lobby) *testPlayer
	}{
		{"at the nickname prompt", func(t *testing.T, l *Lobby) *testPlayer { return newPlayer(t, l) }},
		{"at the menu", func(t *testing.T, l *Lobby) *testPlayer { return named(t, l, "bob") }},
		{"while waiting in a room", func(t *testing.T, l *Lobby) *testPlayer {
			p := named(t, l, "bob")
			p.press("1")
			p.expect("Waiting for an opponent")
			return p
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := startLobby(t, ticTacToeRegistry(t))
			p := tt.prep(t, l)
			if got := l.Stats().Players; got != 1 {
				t.Fatalf("Players = %d before the disconnect, want 1", got)
			}

			p.disconnect()
			p.disconnect() // a repeated notice must be harmless

			if st := l.Stats(); st.Players != 0 || st.Rooms != 0 {
				t.Errorf("Stats() = %+v after the disconnect, want nothing left", st)
			}
		})
	}
}

func TestFlow_KeysAfterDisconnectAreIgnored(t *testing.T) {
	t.Parallel()

	l := startLobby(t, ticTacToeRegistry(t))
	p := named(t, l, "bob")
	p.disconnect()

	p.press("1") // a late key from a session that is already gone
	if st := l.Stats(); st.Players != 0 || st.Rooms != 0 {
		t.Errorf("Stats() = %+v, want nothing left", st)
	}
}

// ---- stress ------------------------------------------------------------

// TestStress_ManyPlayersComingAndGoing throws 50 players at the lobby at
// once, each pressing random keys (picking games, moving, leaving rooms) and
// then vanishing at a random moment. Run under -race, it checks that nothing
// crashes or deadlocks, that no player or room is left behind, and that
// every goroutine the lobby started has gone when it stops.
//
// Not parallel: it counts goroutines, and parallel tests only start after
// every serial test has finished.
func TestStress_ManyPlayersComingAndGoing(t *testing.T) {
	baseline := runtime.NumGoroutine()

	l := New(ticTacToeRegistry(t), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(stopped)
	}()

	const players = 50
	var wg sync.WaitGroup
	for i := 0; i < players; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(i))) // deterministic per player
			c := newFakeConn()
			l.connect(c)
			l.onKeys(c, keysOf(fmt.Sprintf("n%d\n", i)))

			const alphabet = "1234567890q\n"
			for step := rng.Intn(40); step > 0; step-- {
				l.onKeys(c, keysOf(string(alphabet[rng.Intn(len(alphabet))])))
			}
			l.disconnect(c)
		}()
	}
	wg.Wait()

	// Stats is answered after every event posted before it, so all the
	// disconnects above have been handled by now.
	if st := l.Stats(); st.Players != 0 || st.Rooms != 0 {
		t.Errorf("Stats() = %+v after everyone left, want nothing left", st)
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("Lobby.Run did not return after cancel")
	}

	deadline := time.Now().Add(testTimeout)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want at most %d: the lobby leaked some", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestShutdown_StopsRoomsThatAreStillPlaying: cancelling the lobby while
// games are running must stop every room, not just return.
func TestShutdown_StopsRoomsThatAreStillPlaying(t *testing.T) {
	baseline := runtime.NumGoroutine()

	l := New(ticTacToeRegistry(t), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(stopped)
	}()

	a, b := named(t, l, "a"), named(t, l, "b")
	a.press("1")
	b.press("1")
	b.expect("Opponent: a (X)")
	c := named(t, l, "c")
	c.press("1")
	c.expect("Waiting for an opponent")

	cancel()
	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("Lobby.Run did not return after cancel")
	}

	deadline := time.Now().Add(testTimeout)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want at most %d: rooms were not stopped", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(time.Millisecond)
	}

	// After shutdown, calls into the lobby must return instead of blocking.
	late := make(chan struct{})
	go func() {
		l.connect(newFakeConn())
		l.Stats()
		close(late)
	}()
	select {
	case <-late:
	case <-time.After(testTimeout):
		t.Fatal("calls on a stopped lobby blocked")
	}
}
