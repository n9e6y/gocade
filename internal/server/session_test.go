package server

import (
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

func TestSessionSend_DropsWhenFull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		buf          int
		sends        int
		wantAccepted int
		wantDropped  uint64
	}{
		{name: "room for all", buf: 3, sends: 3, wantAccepted: 3, wantDropped: 0},
		{name: "one slot", buf: 1, sends: 3, wantAccepted: 1, wantDropped: 2},
		{name: "overflow", buf: 3, sends: 5, wantAccepted: 3, wantDropped: 2},
		{name: "unbuffered never accepts without a reader", buf: 0, sends: 2, wantAccepted: 0, wantDropped: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var dropped atomic.Uint64
			// No writer goroutine is running, so nothing drains out.
			s := newSession(1, nil, tt.buf, &dropped)

			accepted := 0
			for i := 0; i < tt.sends; i++ {
				if s.Send([]byte("frame")) {
					accepted++
				}
			}

			if accepted != tt.wantAccepted {
				t.Errorf("accepted = %d, want %d", accepted, tt.wantAccepted)
			}
			if got := dropped.Load(); got != tt.wantDropped {
				t.Errorf("dropped = %d, want %d", got, tt.wantDropped)
			}
		})
	}
}
