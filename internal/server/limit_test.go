package server

import (
	"testing"
	"time"
)

func TestKeyBudget(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Each step asks to send n keys at time t0+at and expects want to be let
	// through. The budget is 10 keys a second with room for a burst of 5.
	type step struct {
		at   time.Duration
		n    int
		want int
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{"a burst up to the size of the bucket", []step{
			{0, 3, 3},
			{0, 10, 2}, // only 2 left of the 5
			{0, 1, 0},
		}},
		{"tokens come back with time", []step{
			{0, 5, 5},
			{0, 1, 0},
			{500 * time.Millisecond, 10, 5}, // 0.5 s at 10/s is 5 keys
		}},
		{"the bucket never holds more than a burst", []step{
			{0, 5, 5},
			{time.Hour, 100, 5},
		}},
		{"part of a token is remembered", []step{
			{0, 5, 5},
			{50 * time.Millisecond, 5, 0},  // half a key
			{100 * time.Millisecond, 5, 1}, // now one whole key
		}},
		{"asking for nothing costs nothing", []step{
			{0, 0, 0},
			{0, 5, 5},
		}},
		{"a clock that goes backwards gives nothing back and does no harm", []step{
			{time.Second, 5, 5},
			{0, 5, 0},
			{time.Second, 5, 0},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := newKeyBudget(10, 5)
			for i, s := range tt.steps {
				if got := b.allow(t0.Add(s.at), s.n); got != s.want {
					t.Fatalf("step %d: allow(+%v, %d) = %d, want %d", i, s.at, s.n, got, s.want)
				}
			}
		})
	}
}
