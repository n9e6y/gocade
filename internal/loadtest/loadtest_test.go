package loadtest_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/game/tictactoe"
	"github.com/n9e6y/gocade/internal/game/tron"
	"github.com/n9e6y/gocade/internal/loadtest"
	"github.com/n9e6y/gocade/internal/lobby"
	"github.com/n9e6y/gocade/internal/server"
)

const testTimeout = 10 * time.Second

// A Tron that is quicker than the real one (a 40 ms tick and a short countdown),
// so that in a few seconds every room goes through countdown, play, result and
// a new round.
const testTick = 40 * time.Millisecond

func fastTron() tron.Config {
	return tron.Config{Width: 40, Height: 20, TicksPerCount: 3, Tick: testTick}
}

// arena is the real stack in this process: server, lobby, and the games as
// registered by cmd/arena.
type arena struct {
	addr string
	srv  *server.Server
	lob  *lobby.Lobby
	stop func()
}

func startArena(t *testing.T) *arena {
	t.Helper()

	reg := lobby.NewRegistry()
	games := []struct {
		name, title string
		f           lobby.Factory
	}{
		{"tictactoe", "Tic-Tac-Toe", func() game.Game { return tictactoe.New() }},
		{"tron", "Tron", func() game.Game { return tron.NewWithConfig(fastTron()) }},
	}
	for _, g := range games {
		if err := reg.Register(g.name, g.title, g.f, lobby.WithBots()); err != nil {
			t.Fatal(err)
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lob := lobby.New(reg, log)
	srv := server.New(ln, lob, log)

	// As in cmd/arena: the server stops first, then the lobby.
	srvCtx, stopServer := context.WithCancel(context.Background())
	lobCtx, stopLobby := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); lob.Run(lobCtx) }()
	go func() { defer wg.Done(); srv.Run(srvCtx) }()

	a := &arena{addr: ln.Addr().String(), srv: srv, lob: lob}
	var once sync.Once
	a.stop = func() {
		once.Do(func() {
			stopServer()
			stopLobby()
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(testTimeout):
				t.Error("server and lobby did not stop: a goroutine is stuck")
			}
		})
	}
	t.Cleanup(a.stop)
	return a
}

// waitUntilEmpty waits (with a deadline) for the lobby to have no players and no
// rooms: every client has gone, and every room they were in has been closed.
func waitUntilEmpty(t *testing.T, l *lobby.Lobby) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for {
		st := l.Stats()
		if st.Players == 0 && st.Rooms == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Stats() = %+v after every client left, want nothing left: something leaked", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRun_50Rooms is the load test: 50 rooms of Tron running at once against the
// real server, for a few seconds, under whatever the test was started with
// (CI and "make test" use -race). It checks that nothing breaks, nothing is left
// behind, and that frames keep arriving at about the pace of the tick.
//
// It is skipped with -short because it runs for real seconds.
func TestRun_50Rooms(t *testing.T) {
	if testing.Short() {
		t.Skip("load test skipped in -short mode")
	}

	for _, tt := range []struct {
		name string
		bots bool
		pick string
	}{
		{"pairs of clients", false, "2"},
		{"clients against bots", true, "22"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := startArena(t)

			rep, err := loadtest.Run(context.Background(), loadtest.Config{
				Addr:     a.addr,
				Rooms:    50,
				Bots:     tt.bots,
				Pick:     tt.pick,
				Duration: 3 * time.Second,
				KeyEvery: 50 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("Run() = %v", err)
			}
			t.Logf("\n%s", rep)
			t.Logf("server side: %d frames dropped, %d keys throttled, %d connections refused",
				a.srv.Dropped(), a.srv.Throttled(), a.srv.Rejected())

			wantClients := 100
			if tt.bots {
				wantClients = 50
			}
			if rep.Clients != wantClients {
				t.Errorf("Clients = %d, want %d", rep.Clients, wantClients)
			}
			if rep.DialFailures != 0 || rep.Disconnects != 0 {
				t.Errorf("dial failures = %d, unexpected disconnects = %d, want 0 and 0", rep.DialFailures, rep.Disconnects)
			}
			if rep.MinFrames == 0 {
				t.Error("some client never received a frame")
			}
			// A loose bound: the tick is 40 ms, and -race on a busy machine is slow.
			// The real numbers are in the log above.
			if rep.Gaps.Count == 0 || rep.Gaps.P50 > 5*testTick {
				t.Errorf("median frame gap = %v over %d samples, want at most %v", rep.Gaps.P50, rep.Gaps.Count, 5*testTick)
			}

			// Every client has disconnected; the server must notice and clean up
			// every player and room, and then stop with no goroutine stuck.
			waitUntilEmpty(t, a.lob)
			a.stop()
		})
	}
}

func TestRun_RejectsBadConfig(t *testing.T) {
	t.Parallel()

	good := loadtest.Config{Addr: "127.0.0.1:1", Rooms: 1, Duration: time.Second}
	tests := []struct {
		name   string
		mutate func(*loadtest.Config)
	}{
		{"no address", func(c *loadtest.Config) { c.Addr = "" }},
		{"no rooms", func(c *loadtest.Config) { c.Rooms = 0 }},
		{"no duration", func(c *loadtest.Config) { c.Duration = 0 }},
	}
	for _, tt := range tests {
		cfg := good
		tt.mutate(&cfg)
		if _, err := loadtest.Run(context.Background(), cfg); err == nil {
			t.Errorf("%s: Run() succeeded, want an error", tt.name)
		}
	}
}

// Clients that cannot connect are reported, not hidden, and Run still returns
// promptly.
func TestRun_CountsDialFailures(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here any more

	rep, err := loadtest.Run(context.Background(), loadtest.Config{Addr: addr, Rooms: 3, Duration: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if rep.DialFailures != rep.Clients || rep.Clients != 6 {
		t.Errorf("clients = %d, dial failures = %d, want all 6 to fail", rep.Clients, rep.DialFailures)
	}
}
