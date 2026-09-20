package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/input"
)

// testTimeout bounds every blocking step in these tests. Tests never wait
// longer than this, and never sleep to "let things settle".
const testTimeout = 5 * time.Second

const testGreeting = "Arena test server\r\n"

// echoHandler stands in for the real game handler: it greets on connect,
// echoes printable keys back, and closes the session on q or Ctrl-C. It also
// counts calls, so tests can check the Handler contract.
type echoHandler struct {
	connects    atomic.Int64
	disconnects atomic.Int64
}

func (h *echoHandler) OnConnect(s *Session) {
	h.connects.Add(1)
	s.Send([]byte(testGreeting))
}

func (h *echoHandler) OnKeys(s *Session, keys []input.Key) {
	var out []byte
	for _, k := range keys {
		if k.IsQuit() {
			s.Close()
			return
		}
		if k.Kind == input.KindRune {
			out = append(out, byte(k.Rune))
		}
	}
	if len(out) > 0 {
		s.Send(out)
	}
}

func (h *echoHandler) OnDisconnect(s *Session) { h.disconnects.Add(1) }

// keyHandler records the keys each session produces.
type keyHandler struct {
	mu   sync.Mutex // guards keys; OnKeys runs on many session goroutines
	keys map[uint64][]input.Key
}

func newKeyHandler() *keyHandler { return &keyHandler{keys: make(map[uint64][]input.Key)} }

func (h *keyHandler) OnConnect(*Session)    {}
func (h *keyHandler) OnDisconnect(*Session) {}
func (h *keyHandler) OnKeys(s *Session, keys []input.Key) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.keys[s.ID()] = append(h.keys[s.ID()], keys...)
}

func (h *keyHandler) got(id uint64) []input.Key {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.keys[id])
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitFor polls cond until it is true or d has passed. Polling with a
// deadline is the sanctioned alternative to a fixed sleep.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

// startServer runs a Server on 127.0.0.1:0. cancel stops it; done yields
// Run's result. The server is also stopped when the test ends.
func startServer(t *testing.T, h Handler) (srv *Server, addr string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv = New(ln, h, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	finished := make(chan struct{}) // closed after Run returns; separate from ch so tests may consume ch
	go func() {
		ch <- srv.Run(ctx)
		close(finished)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(testTimeout):
			t.Error("Run did not return after cancel")
		}
	})
	return srv, ln.Addr().String(), cancel, ch
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, testTimeout)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := conn.SetDeadline(time.Now().Add(testTimeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readGreeting consumes the one-line greeting sent on connect.
func readGreeting(t *testing.T, conn net.Conn) {
	t.Helper()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read greeting: %v", err)
	}
	if !strings.Contains(line, "Arena") {
		t.Fatalf("greeting = %q, want it to mention Arena", line)
	}
}

// echoOnce writes payload and expects the same bytes back.
func echoOnce(conn net.Conn, payload string) error {
	if _, err := io.WriteString(conn, payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return fmt.Errorf("read echo: %w", err)
	}
	if string(got) != payload {
		return fmt.Errorf("echo = %q, want %q", got, payload)
	}
	return nil
}

// pipeSession runs srv.serve on one end of a net.Pipe and returns the
// client end plus a channel closed when serve returns. net.Pipe is
// synchronous: a write blocks until the other side reads.
func pipeSession(t *testing.T, srv *Server) (client net.Conn, served <-chan struct{}) {
	t.Helper()
	c, s := net.Pipe()
	if err := c.SetDeadline(time.Now().Add(testTimeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.serve(context.Background(), s)
	}()
	return c, done
}

func waitServed(t *testing.T, served <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-served:
	case <-time.After(testTimeout):
		t.Fatalf("serve did not return %s", what)
	}
}

func TestServe_GreetsAndEchoes(t *testing.T) {
	t.Parallel()

	srv := New(nil, &echoHandler{}, discardLogger())
	client, served := pipeSession(t, srv)

	readGreeting(t, client)
	if err := echoOnce(client, "hi"); err != nil {
		t.Fatal(err)
	}

	client.Close()
	waitServed(t, served, "after the client closed")
}

func TestServe_HandlerClosingTheSessionDisconnects(t *testing.T) {
	t.Parallel()

	srv := New(nil, &echoHandler{}, discardLogger())
	client, served := pipeSession(t, srv)

	readGreeting(t, client)
	if _, err := io.WriteString(client, "q"); err != nil {
		t.Fatalf("write q: %v", err)
	}

	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after q: err = %v, want io.EOF", err)
	}
	waitServed(t, served, "after q")
}

func TestServe_HandlerLifecycleCalledOncePerSession(t *testing.T) {
	t.Parallel()

	h := &echoHandler{}
	srv := New(nil, h, discardLogger())
	client, served := pipeSession(t, srv)
	readGreeting(t, client)

	if got := h.connects.Load(); got != 1 {
		t.Errorf("OnConnect calls = %d, want 1", got)
	}
	if got := h.disconnects.Load(); got != 0 {
		t.Errorf("OnDisconnect calls before disconnect = %d, want 0", got)
	}

	client.Close()
	waitServed(t, served, "after the client closed")

	// serve calls OnDisconnect before it returns, so no polling is needed.
	if got := h.disconnects.Load(); got != 1 {
		t.Errorf("OnDisconnect calls = %d, want exactly 1", got)
	}
}

// TestServe_DecodesSplitEscapeSequences: an arrow key arriving in two reads
// must reach the handler as one Up key.
func TestServe_DecodesSplitEscapeSequences(t *testing.T) {
	t.Parallel()

	h := newKeyHandler()
	srv := New(nil, h, discardLogger())
	client, served := pipeSession(t, srv)

	if _, err := io.WriteString(client, "\x1b["); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(client, "A"); err != nil {
		t.Fatal(err)
	}
	client.Close()
	waitServed(t, served, "after the client closed")

	want := []input.Key{{Kind: input.KindUp}}
	if got := h.got(1); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

// TestServe_EachSessionHasItsOwnDecoder: half of an escape sequence on one
// connection must not combine with the rest of it from another.
func TestServe_EachSessionHasItsOwnDecoder(t *testing.T) {
	t.Parallel()

	h := newKeyHandler()
	srv := New(nil, h, discardLogger())
	a, servedA := pipeSession(t, srv) // session 1... or 2: ids are assigned in serve order
	b, servedB := pipeSession(t, srv)

	// Session A starts a sequence and stops. Session B sends the tail of one:
	// alone, "[A" is just two ordinary characters.
	if _, err := io.WriteString(a, "\x1b"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(b, "[A"); err != nil {
		t.Fatal(err)
	}
	a.Close()
	b.Close()
	waitServed(t, servedA, "for A")
	waitServed(t, servedB, "for B")

	var runeKeys int
	for id := uint64(1); id <= 2; id++ {
		for _, k := range h.got(id) {
			if k.Kind == input.KindUp {
				t.Errorf("session %d produced Up: decoder state leaked between sessions", id)
			}
			if k.Kind == input.KindRune {
				runeKeys++
			}
		}
	}
	if runeKeys != 2 {
		t.Errorf("got %d rune keys, want 2 ('[' and 'A' from session B)", runeKeys)
	}
}

func TestRun_100ConcurrentClients(t *testing.T) {
	t.Parallel()

	h := &echoHandler{}
	_, addr, _, _ := startServer(t, h)

	const clients = 100
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- runClient(addr, fmt.Sprintf("client-%d", i))
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	// Every session that connected must be reported as disconnected, once.
	waitFor(t, testTimeout, func() bool { return h.disconnects.Load() == clients })
	if got := h.connects.Load(); got != clients {
		t.Errorf("OnConnect calls = %d, want %d", got, clients)
	}
}

// runClient is a whole client session: greeting, one echo, quit. It returns
// errors instead of calling t.Fatal because it runs off the test goroutine.
func runClient(addr, payload string) error {
	conn, err := net.DialTimeout("tcp", addr, testTimeout)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(testTimeout)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}

	r := bufio.NewReader(conn)
	if _, err := r.ReadString('\n'); err != nil {
		return fmt.Errorf("read greeting: %w", err)
	}
	if _, err := io.WriteString(conn, payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(r, got); err != nil {
		return fmt.Errorf("read echo: %w", err)
	}
	if string(got) != payload {
		return fmt.Errorf("echo = %q, want %q", got, payload)
	}
	if _, err := io.WriteString(conn, "q"); err != nil {
		return fmt.Errorf("write q: %w", err)
	}
	if _, err := r.ReadByte(); err != io.EOF {
		return fmt.Errorf("after q: err = %v, want io.EOF", err)
	}
	return nil
}

// TestRun_SlowClientDoesNotBlockOthers: client A floods the server but never
// reads, so the echoes back to A pile up until A's out buffer overflows and
// frames are dropped. Client B must still get prompt service.
func TestRun_SlowClientDoesNotBlockOthers(t *testing.T) {
	t.Parallel()

	srv, addr, _, _ := startServer(t, &echoHandler{})

	a := dial(t, addr)
	if tc, ok := a.(*net.TCPConn); ok {
		tc.SetReadBuffer(1024) // shrink A's receive window so its socket fills fast
	}

	chunk := []byte(strings.Repeat("x", 256))
	deadline := time.Now().Add(testTimeout)
	for srv.Dropped() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no frames were dropped for the slow client")
		}
		// The server's reader never blocks on a slow writer, so these
		// writes always complete.
		if _, err := a.Write(chunk); err != nil {
			t.Fatalf("flood write: %v", err)
		}
	}

	b := dial(t, addr)
	readGreeting(t, b)
	if err := echoOnce(b, "still-alive"); err != nil {
		t.Fatalf("client B blocked by slow client A: %v", err)
	}
}

// TestRun_GracefulShutdown is deliberately not parallel: it counts goroutines,
// and parallel tests only start after every serial test has finished.
func TestRun_GracefulShutdown(t *testing.T) {
	baseline := runtime.NumGoroutine()

	h := &echoHandler{}
	_, addr, cancel, done := startServer(t, h)

	const clients = 5
	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
		readGreeting(t, conns[i])
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after cancel", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Run did not return after cancel")
	}

	// Run returns only after every serve goroutine has, and serve reports the
	// disconnect before it returns, so this holds without polling.
	if got := h.disconnects.Load(); got != clients {
		t.Errorf("OnDisconnect calls after shutdown = %d, want %d", got, clients)
	}

	for i, c := range conns {
		if _, err := c.Read(make([]byte, 1)); err != io.EOF {
			t.Errorf("client %d: read err = %v, want io.EOF", i, err)
		}
	}

	waitFor(t, testTimeout, func() bool { return runtime.NumGoroutine() <= baseline })
}
