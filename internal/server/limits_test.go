package server

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- connection cap --------------------------------------------------------

func TestRun_ConnectionCapTurnsExtraClientsAway(t *testing.T) {
	t.Parallel()

	h := &echoHandler{}
	srv, addr, _, _ := startServer(t, h, WithMaxConns(2))

	a, b := dial(t, addr), dial(t, addr)
	readGreeting(t, a)
	readGreeting(t, b)

	// The third gets a message and is disconnected, and never reaches the
	// handler.
	c := dial(t, addr)
	msg, err := io.ReadAll(c) // returns when the server closes the connection
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(msg), "full") {
		t.Errorf("rejected client was told %q, want a message saying the server is full", msg)
	}
	if got := srv.Rejected(); got != 1 {
		t.Errorf("Rejected() = %d, want 1", got)
	}
	if got := h.connects.Load(); got != 2 {
		t.Errorf("handler saw %d connections, want 2", got)
	}

	// Once someone leaves, the place can be taken. The slot is freed when the
	// server has finished with the first connection, which happens shortly
	// after it closes, so poll (with a deadline) until a newcomer gets in.
	a.Close()
	waitFor(t, testTimeout, func() bool {
		d := dial(t, addr)
		line, err := io.ReadAll(io.LimitReader(d, int64(len(testGreeting))))
		d.Close()
		return err == nil && strings.Contains(string(line), "Arena")
	})
}

// A server with no cap accepts as many as come (the 100-client test relies on
// this).
func TestRun_NoCapByDefault(t *testing.T) {
	t.Parallel()

	srv, addr, _, _ := startServer(t, &echoHandler{})
	for i := 0; i < 5; i++ {
		readGreeting(t, dial(t, addr))
	}
	if got := srv.Rejected(); got != 0 {
		t.Errorf("Rejected() = %d, want 0", got)
	}
}

// ---- idle timeout ---------------------------------------------------------------

func TestRun_IdleClientIsDisconnectedWithAMessage(t *testing.T) {
	t.Parallel()

	h := &echoHandler{}
	_, addr, _, _ := startServer(t, h, WithIdleTimeout(50*time.Millisecond))

	conn := dial(t, addr)
	out, err := io.ReadAll(conn) // the client says nothing; returns when the server hangs up
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(out), "idle") {
		t.Errorf("client was sent %q, want a message about being idle", out)
	}
	waitFor(t, testTimeout, func() bool { return h.disconnects.Load() == 1 })
}

// Pressing keys is not being idle: a player who keeps typing stays connected for
// far longer than the timeout.
func TestRun_ActiveClientIsNotDisconnected(t *testing.T) {
	t.Parallel()

	const idle = 400 * time.Millisecond
	_, addr, _, _ := startServer(t, &echoHandler{}, WithIdleTimeout(idle))

	conn := dial(t, addr)
	readGreeting(t, conn)

	// Six keys, each a quarter of the timeout after the last: 1.5 timeouts in
	// all, and every one is echoed back.
	tick := time.NewTicker(idle / 4)
	defer tick.Stop()
	for i := 0; i < 6; i++ {
		<-tick.C
		if err := echoOnce(conn, "k"); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
}

// ---- key budget ---------------------------------------------------------------------

// fakeNow is a clock a test moves by hand. The server reads it from a session
// goroutine, so it is locked.
type fakeNow struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeNow) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeNow) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func TestServe_KeyBudgetDropsTheExcessAndRefills(t *testing.T) {
	t.Parallel()

	clock := &fakeNow{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	h := newKeyHandler()
	srv := New(nil, h, discardLogger(), WithKeyRate(10, 5), withClock(clock.now))
	client, _ := pipeSession(t, srv)

	// Eight keys in one read against a bucket of five: three are dropped.
	if _, err := io.WriteString(client, "abcdefgh"); err != nil {
		t.Fatal(err)
	}
	// A second later the bucket is full again (10 a second, capped at 5).
	// This key follows the batch above, so once it arrives the batch has
	// certainly been handled.
	waitFor(t, testTimeout, func() bool { return srv.Throttled() == 3 })
	clock.advance(time.Second)
	if _, err := io.WriteString(client, "Z"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, testTimeout, func() bool { return len(h.got(1)) == 6 })

	var got string
	for _, k := range h.got(1) {
		got += string(k.Rune)
	}
	if got != "abcdeZ" {
		t.Errorf("handler got %q, want %q (the first five and the one after the refill)", got, "abcdeZ")
	}
	if n := srv.Throttled(); n != 3 {
		t.Errorf("Throttled() = %d, want 3", n)
	}
}

// Each session has its own budget: one flooding client cannot use up another's.
func TestServe_KeyBudgetIsPerSession(t *testing.T) {
	t.Parallel()

	clock := &fakeNow{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	h := newKeyHandler()
	srv := New(nil, h, discardLogger(), WithKeyRate(1, 2), withClock(clock.now))
	flooder, _ := pipeSession(t, srv) // session 1
	polite, _ := pipeSession(t, srv)  // session 2

	if _, err := io.WriteString(flooder, "xxxxxxxxxx"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, testTimeout, func() bool { return srv.Throttled() == 8 })

	if _, err := io.WriteString(polite, "ab"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, testTimeout, func() bool { return len(h.got(2)) == 2 })
}

// End to end over TCP: a client that floods is throttled, and another client is
// served as usual meanwhile.
func TestRun_FloodingClientIsThrottledAndOthersAreFine(t *testing.T) {
	t.Parallel()

	srv, addr, _, _ := startServer(t, &echoHandler{}, WithKeyRate(100, 200))

	flooder := dial(t, addr)
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)   // before go
	go func() { // owned by this test; stops when stop is called or the write fails
		defer wg.Done()
		chunk := []byte(strings.Repeat("x", 4096))
		for ctx.Err() == nil {
			if _, err := flooder.Write(chunk); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { stop(); flooder.Close(); wg.Wait() })

	waitFor(t, testTimeout, func() bool { return srv.Throttled() > 1000 })

	other := dial(t, addr)
	readGreeting(t, other)
	if err := echoOnce(other, "hi"); err != nil { // two keys: within the burst
		t.Errorf("another client while one floods: %v", err)
	}
}
