package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// testTimeout bounds every blocking step in these tests. Tests never wait
// longer than this, and never sleep to "let things settle".
const testTimeout = 5 * time.Second

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitFor polls cond until it is true or d has passed.
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
func startServer(t *testing.T) (srv *Server, addr string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv = New(ln, discardLogger())

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

// TestServe_GreetsAndEchoes uses net.Pipe, which is synchronous: a write
// blocks until the other side reads, so the client reads concurrently with
// the server's writer goroutine.
func TestServe_GreetsAndEchoes(t *testing.T) {
	t.Parallel()

	client, serverSide := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(testTimeout))

	srv := New(nil, discardLogger())
	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.serve(context.Background(), serverSide)
	}()

	readGreeting(t, client)
	if err := echoOnce(client, "hi"); err != nil {
		t.Fatal(err)
	}

	client.Close()
	select {
	case <-served:
	case <-time.After(testTimeout):
		t.Fatal("serve did not return after client closed")
	}
}

func TestServe_QuitDisconnects(t *testing.T) {
	t.Parallel()

	client, serverSide := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(testTimeout))

	srv := New(nil, discardLogger())
	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.serve(context.Background(), serverSide)
	}()

	readGreeting(t, client)
	if _, err := io.WriteString(client, "q"); err != nil {
		t.Fatalf("write q: %v", err)
	}

	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after q: err = %v, want io.EOF", err)
	}
	select {
	case <-served:
	case <-time.After(testTimeout):
		t.Fatal("serve did not return after q")
	}
}

func TestRun_100ConcurrentClients(t *testing.T) {
	t.Parallel()

	_, addr, _, _ := startServer(t)

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

	srv, addr, _, _ := startServer(t)

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

	_, addr, cancel, done := startServer(t)

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

	for i, c := range conns {
		if _, err := c.Read(make([]byte, 1)); err != io.EOF {
			t.Errorf("client %d: read err = %v, want io.EOF", i, err)
		}
	}

	waitFor(t, testTimeout, func() bool { return runtime.NumGoroutine() <= baseline })
}
