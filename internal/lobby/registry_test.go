package lobby

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/n9e6y/gocade/internal/game"
)

func stubFactory() game.Game { return nil }

// registration is one Register call, so a test can describe a sequence of them.
type registration struct {
	name, title string
	f           Factory
}

func TestRegistry_Register(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		steps     []registration
		wantErr   error // the error the LAST step must return; nil means all succeed
		wantNames []string
	}{
		{
			name:      "one game",
			steps:     []registration{{"tictactoe", "Tic-Tac-Toe", stubFactory}},
			wantNames: []string{"tictactoe"},
		},
		{
			name: "order of registration is kept",
			steps: []registration{
				{"tron", "Tron", stubFactory},
				{"tictactoe", "Tic-Tac-Toe", stubFactory},
				{"snake", "Snake", stubFactory},
			},
			wantNames: []string{"tron", "tictactoe", "snake"},
		},
		{
			name: "duplicate name rejected",
			steps: []registration{
				{"tictactoe", "Tic-Tac-Toe", stubFactory},
				{"tictactoe", "Again", stubFactory},
			},
			wantErr:   ErrDuplicateGame,
			wantNames: []string{"tictactoe"},
		},
		{
			name:    "empty name rejected",
			steps:   []registration{{"", "Nameless", stubFactory}},
			wantErr: ErrInvalidGame,
		},
		{
			name:    "empty title rejected",
			steps:   []registration{{"tictactoe", "", stubFactory}},
			wantErr: ErrInvalidGame,
		},
		{
			name:    "nil factory rejected",
			steps:   []registration{{"tictactoe", "Tic-Tac-Toe", nil}},
			wantErr: ErrInvalidGame,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := NewRegistry()
			var err error
			for _, s := range tt.steps {
				err = reg.Register(s.name, s.title, s.f)
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("last Register error = %v, want %v", err, tt.wantErr)
			}

			var got []string
			for _, e := range reg.Entries() {
				got = append(got, e.Name)
			}
			if !slices.Equal(got, tt.wantNames) {
				t.Errorf("registered names = %v, want %v", got, tt.wantNames)
			}
		})
	}
}

func TestRegistry_Lookup(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	if err := reg.Register("tictactoe", "Tic-Tac-Toe", stubFactory); err != nil {
		t.Fatal(err)
	}

	e, ok := reg.Lookup("tictactoe")
	if !ok || e.Title != "Tic-Tac-Toe" {
		t.Errorf("Lookup(tictactoe) = (%+v, %v), want the Tic-Tac-Toe entry", e, ok)
	}
	if _, ok := reg.Lookup("missing"); ok {
		t.Error("Lookup(missing) found an entry")
	}
}

// TestRegistry_EntriesIsACopy: callers must not be able to reorder or edit
// the registry through the slice it returns.
func TestRegistry_EntriesIsACopy(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	_ = reg.Register("a", "A", stubFactory)
	_ = reg.Register("b", "B", stubFactory)

	got := reg.Entries()
	got[0], got[1] = got[1], got[0]

	if first := reg.Entries()[0].Name; first != "a" {
		t.Errorf("registry order changed through Entries(): first = %q", first)
	}
}

// The menu picks games with the digit keys 1-9, so a tenth game could never
// be chosen; the registry refuses it instead of failing silently.
func TestRegistry_AtMostNineGames(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	for i := 1; i <= MaxGames; i++ {
		if err := reg.Register(fmt.Sprintf("g%d", i), "Game", stubFactory); err != nil {
			t.Fatalf("game %d: %v", i, err)
		}
	}
	if err := reg.Register("one-too-many", "Game", stubFactory); !errors.Is(err, ErrInvalidGame) {
		t.Errorf("game %d: err = %v, want ErrInvalidGame", MaxGames+1, err)
	}
	if got := len(reg.Entries()); got != MaxGames {
		t.Errorf("registry holds %d games, want %d", got, MaxGames)
	}
}
