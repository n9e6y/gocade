package game

import (
	"errors"
	"testing"
)

func TestState_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state State
		want  string
	}{
		{StateWaiting, "Waiting"},
		{StateRunning, "Running"},
		{StateOver, "Over"},
		{State(9), "State(9)"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", uint8(tt.state), got, tt.want)
		}
	}
}

// The lobby tells "try another room" errors apart from real failures by
// matching on these, so they must stay distinct from one another.
func TestJoinErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []error{ErrFull, ErrAlreadyJoined, ErrOver, ErrStarted}
	for i, a := range all {
		if a == nil || a.Error() == "" {
			t.Errorf("error %d has no message", i)
		}
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%q matches %q", a, b)
			}
		}
	}
}
