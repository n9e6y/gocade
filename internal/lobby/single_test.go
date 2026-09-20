package lobby

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game/tictactoe"
	"github.com/n9e6y/gocade/internal/room"
	"github.com/n9e6y/gocade/internal/server"
)

// testTimeout bounds every read and wait. Tests never sleep to let the
// server "catch up": they read until the expected text arrives.
const testTimeout = 5 * time.Second

// startArena runs the real stack (server, single room, Tic-Tac-Toe) on
// 127.0.0.1:0 and returns its address. It is stopped when the test ends.
func startArena(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	g := tictactoe.New()
	tick, stopTicker := room.TickerFor(g)
	lob := NewSingleRoom(g, tick, log)
	srv := server.New(ln, lob, log)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); lob.Run(ctx) }()
	go func() { defer wg.Done(); srv.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(testTimeout):
			t.Error("server and room did not stop after cancel")
		}
		stopTicker()
	})
	return ln.Addr().String()
}

// client is a fake player: it presses keys and reads what the server draws.
type client struct {
	t    *testing.T
	conn net.Conn
	buf  []byte // received but not yet consumed by expect
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

// press sends keystrokes, as if typed.
func (c *client) press(keys string) {
	c.t.Helper()
	if _, err := io.WriteString(c.conn, keys); err != nil {
		c.t.Fatalf("press %q: %v", keys, err)
	}
}

// ansi matches a complete terminal escape sequence such as a color change.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// expect reads until text has appeared, then discards everything up to the
// end of it. Discarding is what stops an old frame from satisfying a later
// expectation: each call only sees output that arrived after the last one.
//
// Escape sequences are stripped first, leaving the text a person would see
// on screen. A sequence cut in half by a read stays in the buffer and is
// stripped once the rest arrives.
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
			c.t.Fatalf("waiting for %q: %v (received so far: %q)", text, err, c.buf)
		}
	}
}

// expectClosed reads until the server closes the connection.
func (c *client) expectClosed() {
	c.t.Helper()
	tmp := make([]byte, 4096)
	for {
		if _, err := c.conn.Read(tmp); err != nil {
			if err != io.EOF {
				c.t.Fatalf("waiting for close: %v", err)
			}
			return
		}
	}
}

// twoPlayers connects X and then O, and waits until both are seated. X is
// connected first and seen to be waiting before O connects, so the seat
// order is certain.
func twoPlayers(t *testing.T, addr string) (x, o *client) {
	t.Helper()
	x = dial(t, addr)
	x.expect("Waiting for an opponent")
	o = dial(t, addr)
	o.expect("Opponent's turn")
	x.expect("Your turn")
	return x, o
}

func TestSingleRoom_TwoPlayersPlayAFullGame(t *testing.T) {
	t.Parallel()

	x, o := twoPlayers(t, startArena(t))

	// X takes the top row while O takes the middle row's first two cells.
	x.press("1")
	o.expect("Your turn")
	o.press("4")
	x.expect("Your turn")
	x.press("2")
	o.expect("Your turn")
	o.press("5")
	x.expect("Your turn")
	x.press("3")

	x.expect("You win!")
	o.expect("You lose")
}

func TestSingleRoom_IllegalMovesAreRejectedWithAMessage(t *testing.T) {
	t.Parallel()

	x, o := twoPlayers(t, startArena(t))

	o.press("5") // O moves before X
	o.expect("Not your turn")

	x.press("5")
	o.expect("Your turn")
	o.press("5") // occupied
	o.expect("Cell 5 is taken")

	// O may still move after a rejected move. X's screen has shown "Your turn"
	// several times by now, so wait for O's mark on the board instead.
	o.press("1")
	x.expect(" O | 2 | 3 ")
}

func TestSingleRoom_ThirdPlayerIsTurnedAwayAndTheGameGoesOn(t *testing.T) {
	t.Parallel()

	addr := startArena(t)
	x, o := twoPlayers(t, addr)

	c := dial(t, addr)
	c.expect("Cannot join")
	c.press("5") // a bystander's keys must not reach the game
	c.press("q")
	c.expectClosed()

	// The bystander leaving must not have forfeited anything.
	x.press("5")
	o.expect(" 4 | X | 6 ") // the board is drawn before the status line
	o.expect("Your turn")
}

func TestSingleRoom_DisconnectMidGameForfeits(t *testing.T) {
	t.Parallel()

	x, o := twoPlayers(t, startArena(t))
	x.press("5")
	o.expect("Your turn")

	o.conn.Close() // the client dies without saying goodbye

	x.expect("Opponent left, you win by forfeit")
}

func TestSingleRoom_QuitKeyLeavesAndForfeits(t *testing.T) {
	t.Parallel()

	x, o := twoPlayers(t, startArena(t))

	x.press("q")
	x.expectClosed()
	o.expect("Opponent left, you win by forfeit")
}
