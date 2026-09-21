package server

import (
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
)

func TestSession_IDAndClose(t *testing.T) {
	t.Parallel()

	var dropped atomic.Uint64
	s := newSession(7, nil, 1, &dropped)
	if s.ID() != 7 {
		t.Errorf("ID() = %d, want 7", s.ID())
	}

	s.Close() // no cancel func yet: must not panic

	calls := 0
	s.cancel = func() { calls++ }
	s.Close()
	s.Close() // idempotent from the caller's point of view: cancel may be called repeatedly
	if calls != 2 {
		t.Errorf("cancel called %d times, want 2 (context cancel funcs are safe to repeat)", calls)
	}
}

// When the buffer is full, Send makes room by discarding the oldest queued
// frame, so the newest frame (which is a complete picture of the game, and may
// be the final result) is always the one that survives.
func TestSessionSend_WhenFullTheOldestFrameIsDiscarded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		buf          int
		sends        int
		wantAccepted int
		wantDropped  uint64
		wantQueue    []string // what is left in the buffer, oldest first
	}{
		{name: "room for all", buf: 3, sends: 3, wantAccepted: 3, wantDropped: 0, wantQueue: []string{"f1", "f2", "f3"}},
		{name: "one slot keeps only the newest", buf: 1, sends: 3, wantAccepted: 3, wantDropped: 2, wantQueue: []string{"f3"}},
		{name: "overflow keeps the newest few", buf: 3, sends: 5, wantAccepted: 5, wantDropped: 2, wantQueue: []string{"f3", "f4", "f5"}},
		{name: "unbuffered never accepts without a reader", buf: 0, sends: 2, wantAccepted: 0, wantDropped: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var dropped atomic.Uint64
			// No writer goroutine is running, so nothing drains out.
			s := newSession(1, nil, tt.buf, &dropped)

			accepted := 0
			for i := 1; i <= tt.sends; i++ {
				if s.Send([]byte(fmt.Sprintf("f%d", i))) {
					accepted++
				}
			}

			if accepted != tt.wantAccepted {
				t.Errorf("accepted = %d, want %d", accepted, tt.wantAccepted)
			}
			if got := dropped.Load(); got != tt.wantDropped {
				t.Errorf("dropped = %d, want %d", got, tt.wantDropped)
			}
			var queue []string
			for len(s.out) > 0 {
				queue = append(queue, string(<-s.out))
			}
			if !slices.Equal(queue, tt.wantQueue) {
				t.Errorf("queue = %v, want %v", queue, tt.wantQueue)
			}
		})
	}
}
