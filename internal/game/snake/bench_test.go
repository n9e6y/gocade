package snake

import (
	"testing"

	"github.com/n9e6y/gocade/internal/game"
)

// playingGame returns a game past its countdown with the given players.
func playingGame(b *testing.B, cfg Config, players int) *Game {
	b.Helper()
	g := NewWithConfig(cfg)
	for i := 0; i < players; i++ {
		if err := g.Join(game.PlayerID(i+1), "bench"); err != nil {
			b.Fatalf("Join: %v", err)
		}
	}
	for g.phase == phaseCountdown {
		g.Tick()
	}
	return g
}

// BenchmarkSnakeTick measures one tick of two heads moving and eating on the
// default board: a fresh game replaces one that has ended (that cost is
// spread over the many ticks between endings).
func BenchmarkSnakeTick(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Seed = 1
	g := playingGame(b, cfg, 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if g.State() == game.StateOver {
			g = playingGame(b, cfg, 2)
		}
		g.Tick()
	}
}

// BenchmarkSnakeView measures what a room does for one player after each
// tick: draw the view and encode it as a frame. Four players, a few steps
// into a round so there is more than a single cell to draw per snake.
func BenchmarkSnakeView(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Seed = 1
	g := playingGame(b, cfg, 4)
	for i := 0; i < 5; i++ {
		g.Tick()
	}
	if g.State() != game.StateRunning {
		b.Fatalf("State() = %v, want Running", g.State())
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.View(game.PlayerID(1)).Frame()
	}
}

// BenchmarkAdvise measures one bot decision on the default board a few steps
// into a four-player round.
func BenchmarkAdvise(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Seed = 1
	g := playingGame(b, cfg, 4)
	for i := 0; i < 5; i++ {
		g.Tick()
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Advise(game.PlayerID(1))
	}
}
