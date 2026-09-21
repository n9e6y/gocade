package tictactoe

import (
	"testing"

	"github.com/n9e6y/gocade/internal/game"
)

var _ game.Advisor = (*Game)(nil)

func TestAdvise(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		moves    []move // played first, alternating from X
		who      game.PlayerID
		wantCell int // 0 means "no advice"
	}{
		{
			name: "takes a win in one",
			// X: 1 2 9, O: 4 5. O to move; 6 wins (blocking X at 3 would lose the chance).
			moves:    []move{{px, 1}, {po, 4}, {px, 2}, {po, 5}, {px, 9}},
			who:      po,
			wantCell: 6,
		},
		{
			name: "blocks a win in one",
			// X: 1 2, O: 5. X threatens 3.
			moves:    []move{{px, 1}, {po, 5}, {px, 2}},
			who:      po,
			wantCell: 3,
		},
		{
			name: "answers a corner with the center",
			// Any other reply to a corner loses, so 5 is the only move.
			moves:    []move{{px, 1}},
			who:      po,
			wantCell: 5,
		},
		{
			name: "answers opposite corners with an edge, not a corner",
			// X: 1 9, O: 5. A corner reply lets X build a fork; the lowest edge is 2.
			moves:    []move{{px, 1}, {po, 5}, {px, 9}},
			who:      po,
			wantCell: 2,
		},
		{
			name:     "opens in the first cell when every move draws",
			moves:    nil,
			who:      px,
			wantCell: 1,
		},
		{
			name:     "not your turn gives no advice",
			moves:    []move{{px, 1}},
			who:      px,
			wantCell: 0,
		},
		{
			name:     "a player who is not seated gets no advice",
			moves:    nil,
			who:      game.PlayerID(99),
			wantCell: 0,
		},
		{
			name:     "a finished game gives no advice",
			moves:    []move{{px, 1}, {po, 4}, {px, 2}, {po, 5}, {px, 3}},
			who:      po,
			wantCell: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := newStarted(t)
			play(g, tt.moves...)

			k, ok := g.Advise(tt.who)
			if tt.wantCell == 0 {
				if ok {
					t.Fatalf("Advise() = %v, true; want no advice", k)
				}
				return
			}
			n, isDigit := k.Digit()
			if !ok || !isDigit || n != tt.wantCell {
				t.Errorf("Advise() = %v (digit %d, ok %v), want cell %d", k, n, ok, tt.wantCell)
			}
		})
	}
}

func TestAdvise_WaitingGameGivesNoAdvice(t *testing.T) {
	t.Parallel()

	g := New()
	if err := g.Join(px, nameX); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if k, ok := g.Advise(px); ok {
		t.Errorf("Advise() = %v, true; want no advice while waiting for an opponent", k)
	}
}

// TestAdvise_NeverLoses proves the bot is unbeatable: whatever the human does
// at every one of their turns, the bot never loses. The human's moves are
// enumerated exhaustively; the bot's are whatever Advise says.
func TestAdvise_NeverLoses(t *testing.T) {
	t.Parallel()

	for _, botSeat := range []game.PlayerID{px, po} {
		t.Run(map[game.PlayerID]string{px: "bot plays X", po: "bot plays O"}[botSeat], func(t *testing.T) {
			t.Parallel()

			games := 0
			var explore func(g Game)
			explore = func(g Game) { // g is a copy, so each branch is independent
				if g.State() == game.StateOver {
					games++
					if out := g.Outcome(); !out.Draw && out.Winner != botSeat {
						t.Fatalf("the bot lost:\n%s", screen(&g, botSeat))
					}
					return
				}

				mover := g.seats[g.turn]
				if mover == botSeat {
					k, ok := g.Advise(botSeat)
					if !ok {
						t.Fatalf("no advice on the bot's turn:\n%s", screen(&g, botSeat))
					}
					g.Input(botSeat, k)
					explore(g)
					return
				}
				for cell := 1; cell <= 9; cell++ {
					if g.cells[cell-1] != empty {
						continue
					}
					next := g
					next.Input(mover, digit(cell))
					explore(next)
				}
			}

			explore(*newStarted(t))
			if games == 0 {
				t.Fatal("no games were played")
			}
			t.Logf("checked %d complete games", games)
		})
	}
}

// BenchmarkAdvise measures the bot's reply to a human's opening move: eight
// empty cells, the most the bot ever has to search (the human always moves
// first, so the bot never faces an empty board).
func BenchmarkAdvise(b *testing.B) {
	g := New()
	if err := g.Join(px, nameX); err != nil {
		b.Fatalf("Join: %v", err)
	}
	if err := g.Join(po, nameO); err != nil {
		b.Fatalf("Join: %v", err)
	}
	g.Input(px, digit(1))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Advise(po)
	}
}
