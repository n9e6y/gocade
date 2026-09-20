package lobby

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/server"
)

// startArena runs the real stack (server, lobby, Tic-Tac-Toe) on
// 127.0.0.1:0 and returns its address. It is stopped when the test ends.
func startArena(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	log := discardLogger()

	lob := New(ticTacToeRegistry(t), log)
	srv := server.New(ln, lob, log)

	// Two contexts, so the server can stop (and say goodbye to every session)
	// while the lobby is still there to hear about it, as in cmd/arena.
	srvCtx, stopServer := context.WithCancel(context.Background())
	lobCtx, stopLobby := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); lob.Run(lobCtx) }()
	go func() { defer wg.Done(); srv.Run(srvCtx) }()

	t.Cleanup(func() {
		stopServer()
		stopLobby()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(testTimeout):
			t.Error("server and lobby did not stop after cancel")
		}
	})
	return ln.Addr().String()
}

// client is a fake player on a real TCP connection: it presses keys and
// reads what the server draws.
type client struct {
	t    *testing.T
	conn net.Conn
	buf  []byte // received, escape codes stripped, not yet consumed by expect
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, testTimeout)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := conn.SetDeadline(time.Now().Add(testTimeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &client{t: t, conn: conn}
}

// join connects, chooses a nickname and waits for the menu.
func join(t *testing.T, addr, name string) *client {
	t.Helper()
	c := dial(t, addr)
	c.expect("Choose a nickname")
	c.press(name + "\r") // a terminal in raw mode sends CR for Enter
	c.expect("q) Quit")
	return c
}

func (c *client) press(keys string) {
	c.t.Helper()
	if _, err := io.WriteString(c.conn, keys); err != nil {
		c.t.Fatalf("press %q: %v", keys, err)
	}
}

// expect reads until text has appeared, then discards everything up to the
// end of it, so an old screen cannot satisfy a later expectation.
func (c *client) expect(text string) {
	c.t.Helper()
	tmp := make([]byte, 4096)
	for {
		if i := bytes.Index(c.buf, []byte(text)); i >= 0 {
			c.buf = c.buf[i+len(text):]
			return
		}
		n, err := c.conn.Read(tmp)
		c.buf = ansi.ReplaceAll(append(c.buf, tmp[:n]...), nil)
		if err != nil {
			c.t.Fatalf("waiting for %q: %v (screen so far: %q)", text, err, c.buf)
		}
	}
}

// TestArena_FourPlayersTwoGamesAtOnce is the stage's headline scenario: four
// terminals, two games running side by side, each finishing independently,
// and the winners and losers going back to the menu to play again.
func TestArena_FourPlayersTwoGamesAtOnce(t *testing.T) {
	t.Parallel()

	addr := startArena(t)

	// Players arrive one at a time, so pairs form in arrival order:
	// ann + ben in game 1, cat + dan in game 2.
	ann := join(t, addr, "ann")
	ann.press("1")
	ann.expect("Waiting for an opponent")
	ben := join(t, addr, "ben")
	ben.press("1")
	ann.expect("Opponent: ben (O)")

	cat := join(t, addr, "cat")
	cat.press("1")
	cat.expect("Waiting for an opponent")
	dan := join(t, addr, "dan")
	dan.press("1")
	cat.expect("Opponent: dan (O)")

	// Both games are now running. Moves alternate between the two boards, so
	// they really are in progress at the same time.
	//   game 1: ann (X) takes the top row       -> ann wins
	//   game 2: dan (O) takes the middle row    -> dan wins
	//
	// Keys from different connections can reach the server in either order,
	// so before each move we wait until the mover has been told it is their
	// turn. X's opening screen already says "Your turn"; consume that first.
	ann.expect("Your turn")
	cat.expect("Your turn")

	ann.press("1")
	cat.press("9")
	ben.expect("Your turn")
	dan.expect("Your turn")

	ben.press("4")
	dan.press("4")
	ann.expect("Your turn")
	cat.expect("Your turn")

	ann.press("2")
	cat.press("8")
	ben.expect("Your turn")
	dan.expect("Your turn")

	ben.press("5")
	dan.press("5")
	ann.expect("Your turn")
	cat.expect("Your turn")

	ann.press("3") // completes the top row: game 1 is over
	cat.press("1")
	dan.expect("Your turn")
	dan.press("6") // completes the middle row: game 2 is over

	ann.expect("You win!")
	ben.expect("You lose")
	dan.expect("You win!")
	cat.expect("You lose")

	// Game 1's players go back to the menu and start another game while game
	// 2's players are still looking at their result screen.
	ann.expect("Press Enter to return to the menu.")
	ben.expect("Press Enter to return to the menu.")
	ann.press("\r")
	ben.press("\r")
	ann.expect("q) Quit")
	ben.expect("q) Quit")

	ben.press("1")
	ben.expect("Waiting for an opponent")
	ann.press("1")
	ann.expect("Opponent: ben (X)") // ben arrived first, so ben is X this time
	ben.expect("Your turn")
}

// TestArena_KillingAClientMidGameForfeitsIt is the disconnect scenario over
// real TCP: one player's connection just dies.
func TestArena_KillingAClientMidGameForfeitsIt(t *testing.T) {
	t.Parallel()

	addr := startArena(t)
	ann := join(t, addr, "ann")
	ann.press("1")
	ann.expect("Waiting for an opponent")
	ben := join(t, addr, "ben")
	ben.press("1")
	ann.expect("Opponent: ben (O)")

	ben.conn.Close() // like killing nc

	ann.expect("Opponent left, you win by forfeit")
	ann.expect("Press Enter to return to the menu.")
	ann.press("\r")
	ann.expect("q) Quit")

	// The server is still healthy: a new pair can play.
	cat := join(t, addr, "cat")
	cat.press("1")
	ann.press("1")
	cat.expect("Opponent: ann")
}

// TestArena_TerminalKeysWork: a raw terminal sends CR for Enter and DEL for
// Backspace, and arrow keys as escape sequences. None of that may upset the
// nickname prompt.
func TestArena_TerminalKeysWork(t *testing.T) {
	t.Parallel()

	addr := startArena(t)
	c := dial(t, addr)
	c.expect("Choose a nickname")

	c.press("bobx\x7f") // Backspace is DEL
	c.press("\x1b[A")   // an arrow key must not end up in the name
	c.expect("> bob_")
	c.press("\r")
	c.expect("Hello, bob!")
}
