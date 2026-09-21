package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer that is safe to write from the server's
// goroutines while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex // guards buf: slog writes from server goroutines, the test reads
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRun_ServesTheLobbyAndStopsOnCancel wires everything together the way
// main does: it starts run, plays the part of a client, then cancels the
// context (which is what Ctrl-C does) and expects a clean stop.
func TestRun_ServesTheLobbyAndStopsOnCancel(t *testing.T) {
	const testTimeout = 5 * time.Second

	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, io.Discard, "dev", config{addr: "127.0.0.1:0"}, log) }()

	// run logs the address it is listening on; use that to find the port.
	listening := regexp.MustCompile(`msg=listening addr=(\S+)`)
	var addr string
	deadline := time.Now().Add(testTimeout)
	for addr == "" {
		if m := listening.FindStringSubmatch(logs.String()); m != nil {
			addr = m[1]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never logged its address; logs:\n%s", logs.String())
		}
		time.Sleep(time.Millisecond)
	}

	conn, err := net.DialTimeout("tcp", addr, testTimeout)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(testTimeout))

	// readUntil reads from the server until the screen has shown text.
	var got []byte
	tmp := make([]byte, 4096)
	readUntil := func(text string) {
		t.Helper()
		for !strings.Contains(string(got), text) {
			n, err := conn.Read(tmp)
			got = append(got, tmp[:n]...)
			if err != nil {
				t.Fatalf("waiting for %q: %v (got %q)", text, err, got)
			}
		}
	}

	// A new player is greeted by the lobby and asked for a nickname...
	readUntil("Choose a nickname")
	if _, err := conn.Write([]byte("bob\r")); err != nil {
		t.Fatalf("send nickname: %v", err)
	}

	// ...and then offered every game the server registers.
	readUntil("1) Tic-Tac-Toe")
	readUntil("2) Tron")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v after cancel, want nil", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("run did not return after cancel")
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		addr    string
		want    string
		wantErr bool
	}{
		{name: "default version", version: "dev", addr: "127.0.0.1:0", want: "arena dev\n"},
		{name: "injected version", version: "v0.0.1", addr: "127.0.0.1:0", want: "arena v0.0.1\n"},
		{name: "bad address", version: "dev", addr: "not-an-address", want: "arena dev\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A context that is already cancelled makes a healthy server
			// start, find it should stop, and return at once.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			var out bytes.Buffer
			log := slog.New(slog.NewTextHandler(io.Discard, nil))

			err := run(ctx, &out, tt.version, config{addr: tt.addr}, log)
			if (err != nil) != tt.wantErr {
				t.Fatalf("run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("run() output = %q, want %q", got, tt.want)
			}
		})
	}
}

// startRun starts run with cfg and returns the address it listens on. The
// server is stopped, and must stop cleanly, when the test ends.
func startRun(t *testing.T, cfg config) string {
	t.Helper()
	const testTimeout = 5 * time.Second

	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, io.Discard, "dev", cfg, log) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run returned %v after cancel", err)
			}
		case <-time.After(testTimeout):
			t.Error("run did not return after cancel")
		}
	})

	listening := regexp.MustCompile(`msg=listening addr=(\S+)`)
	deadline := time.Now().Add(testTimeout)
	for {
		if m := listening.FindStringSubmatch(logs.String()); m != nil {
			return m[1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never logged its address; logs:\n%s", logs.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRun_AppliesTheLimits checks that the flags reach the server: with room for
// one connection, the second is turned away.
func TestRun_AppliesTheLimits(t *testing.T) {
	t.Parallel()

	addr := startRun(t, config{addr: "127.0.0.1:0", maxConns: 1})

	first, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	first.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	if _, err := first.Read(buf); err != nil { // the nickname prompt: the server has accepted us
		t.Fatalf("first client: %v", err)
	}

	second, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.SetDeadline(time.Now().Add(5 * time.Second))
	msg, err := io.ReadAll(second)
	if err != nil || !strings.Contains(string(msg), "full") {
		t.Errorf("second client got %q, %v; want a message saying the server is full", msg, err)
	}
}
