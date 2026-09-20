package game

import "testing"

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
