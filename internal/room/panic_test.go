package room

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/render"
)

// panicGame is a game with a bug: it panics in the operation named by on.
type panicGame struct {
	fakeGame
	on string // "join", "input", "tick", "view" or "advise"
}

func (g *panicGame) boom(op string) {
	if g.on == op {
		panic("boom in " + op)
	}
}

func (g *panicGame) Join(p game.PlayerID, name string) error {
	g.boom("join")
	return g.fakeGame.Join(p, name)
}

func (g *panicGame) Input(p game.PlayerID, k input.Key) {
	g.boom("input")
	g.fakeGame.Input(p, k)
}

func (g *panicGame) Tick() {
	g.boom("tick")
	g.fakeGame.Tick()
}

func (g *panicGame) View(p game.PlayerID) *render.Canvas {
	g.boom("view")
	return g.fakeGame.View(p)
}

func (g *panicGame) Advise(game.PlayerID) (input.Key, bool) {
	g.boom("advise")
	return keyRune('z'), true
}

// syncBuffer is a log destination that is safe to write from the room
// goroutine and read from the test.
type syncBuffer struct {
	mu  sync.Mutex
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

func TestPanic_TheRoomStopsAndSaysSo(t *testing.T) {
	t.Parallel()

	// trigger drives the room into the panic from the outside. Where the
	// panic happens while the caller is waiting for an answer (during Join or
	// AddBot), that call comes back with ErrClosed instead of hanging.
	tests := []struct {
		name    string
		on      string
		trigger func(t *testing.T, r *Room, tick chan time.Time)
	}{
		{"in Join", "join", func(t *testing.T, r *Room, _ chan time.Time) {
			if err := r.Join(1, "p1", newSink()); !errors.Is(err, ErrClosed) {
				t.Errorf("Join() = %v, want ErrClosed", err)
			}
		}},
		{"in Input", "input", func(t *testing.T, r *Room, _ chan time.Time) {
			if err := r.Join(1, "p1", newSink()); err != nil {
				t.Fatal(err)
			}
			r.Input(1, keyRune('a'))
		}},
		{"in Tick", "tick", func(t *testing.T, r *Room, tick chan time.Time) {
			if err := r.Join(1, "p1", newSink()); err != nil {
				t.Fatal(err)
			}
			tick <- time.Time{}
		}},
		{"in View", "view", func(t *testing.T, r *Room, _ chan time.Time) {
			// Drawing the first frame happens during Join.
			if err := r.Join(1, "p1", newSink()); !errors.Is(err, ErrClosed) {
				t.Errorf("Join() = %v, want ErrClosed", err)
			}
		}},
		{"in Advise", "advise", func(t *testing.T, r *Room, _ chan time.Time) {
			if err := r.AddBot(botID, "Bot"); !errors.Is(err, ErrClosed) {
				t.Errorf("AddBot() = %v, want ErrClosed", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := &panicGame{on: tt.on}
			g.tickEvery = time.Millisecond
			tick := make(chan time.Time)
			var logs syncBuffer
			r := New(g, tick, slog.New(slog.NewTextHandler(&logs, nil)))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopped := make(chan struct{})
			go func() { r.Run(ctx); close(stopped) }()

			tt.trigger(t, r, tick)

			// The room ends by itself, without anyone cancelling it.
			select {
			case <-r.Done():
			case <-time.After(testTimeout):
				t.Fatal("the room did not stop after the panic")
			}
			select {
			case <-stopped:
			case <-time.After(testTimeout):
				t.Fatal("Run did not return after the panic")
			}
			if !r.Crashed() {
				t.Error("Crashed() = false after a panic")
			}

			// The panic and where it came from are in the log, at Error level.
			out := logs.String()
			for _, want := range []string{"level=ERROR", "room panicked", "boom in " + tt.on, "stack="} {
				if !strings.Contains(out, want) {
					t.Errorf("log does not contain %q:\n%s", want, out)
				}
			}

			// Calls into a dead room return instead of hanging.
			done := make(chan struct{})
			go func() {
				defer close(done)
				if err := r.Join(2, "p2", newSink()); !errors.Is(err, ErrClosed) {
					t.Errorf("Join() after the panic = %v, want ErrClosed", err)
				}
				r.Input(2, keyRune('x'))
				r.Leave(2)
			}()
			select {
			case <-done:
			case <-time.After(testTimeout):
				t.Fatal("a call into the crashed room blocked")
			}
		})
	}
}

// A room that is simply stopped is not a crash.
func TestPanic_NormalStopIsNotACrash(t *testing.T) {
	t.Parallel()

	r := New(&fakeGame{}, nil, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { r.Run(ctx); close(stopped) }()
	cancel()

	select {
	case <-r.Done():
	case <-time.After(testTimeout):
		t.Fatal("the room did not stop")
	}
	<-stopped
	if r.Crashed() {
		t.Error("Crashed() = true after a normal stop")
	}
}
