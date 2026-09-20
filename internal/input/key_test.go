package input

import "testing"

func TestKey_Direction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  Key
		want Dir
		ok   bool
	}{
		{name: "up arrow", key: k(KindUp), want: DirUp, ok: true},
		{name: "down arrow", key: k(KindDown), want: DirDown, ok: true},
		{name: "left arrow", key: k(KindLeft), want: DirLeft, ok: true},
		{name: "right arrow", key: k(KindRight), want: DirRight, ok: true},
		{name: "w", key: r('w'), want: DirUp, ok: true},
		{name: "a", key: r('a'), want: DirLeft, ok: true},
		{name: "s", key: r('s'), want: DirDown, ok: true},
		{name: "d", key: r('d'), want: DirRight, ok: true},
		{name: "caps W", key: r('W'), want: DirUp, ok: true},
		{name: "caps D", key: r('D'), want: DirRight, ok: true},
		{name: "other letter", key: r('x'), ok: false},
		{name: "digit", key: r('5'), ok: false},
		{name: "enter", key: k(KindEnter), ok: false},
		{name: "zero key", key: Key{}, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.key.Direction()
			if ok != tt.ok || got != tt.want {
				t.Errorf("Direction() = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestKey_Digit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  Key
		want int
		ok   bool
	}{
		{name: "zero", key: r('0'), want: 0, ok: true},
		{name: "five", key: r('5'), want: 5, ok: true},
		{name: "nine", key: r('9'), want: 9, ok: true},
		{name: "letter", key: r('a'), ok: false},
		{name: "just before zero", key: r('/'), ok: false},
		{name: "just after nine", key: r(':'), ok: false},
		{name: "enter", key: k(KindEnter), ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.key.Digit()
			if ok != tt.ok || got != tt.want {
				t.Errorf("Digit() = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestKey_IsQuit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  Key
		want bool
	}{
		{name: "q", key: r('q'), want: true},
		{name: "Q", key: r('Q'), want: true},
		{name: "ctrl-c", key: k(KindCtrlC), want: true},
		{name: "other letter", key: r('w'), want: false},
		{name: "enter", key: k(KindEnter), want: false},
		{name: "escape arrow", key: k(KindUp), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.key.IsQuit(); got != tt.want {
				t.Errorf("IsQuit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKind_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind Kind
		want string
	}{
		{KindRune, "Rune"},
		{KindUp, "Up"},
		{KindDown, "Down"},
		{KindLeft, "Left"},
		{KindRight, "Right"},
		{KindEnter, "Enter"},
		{KindBackspace, "Backspace"},
		{KindCtrlC, "CtrlC"},
		{Kind(0), "Kind(0)"},
		{Kind(99), "Kind(99)"},
	}

	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
