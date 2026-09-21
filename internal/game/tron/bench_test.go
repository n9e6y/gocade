package tron

import (
	"testing"
	"time"

	"github.com/n9e6y/gocade/internal/game"
)

// playingGame returns a game past its countdown with the given players.
func playingGame(b *testing.B, cfg Config, players int) *Game {
	b.Helper()
	g := NewWithConfig(cfg)
	for i := 0; i < players; i++ {
		if err := g.Join(pid(i), "bench"); err != nil {
			b.Fatalf("Join: %v", err)
		}
	}
	for g.phase == phaseCountdown {
		g.Tick()
	}
	return g
}

// BenchmarkTronTick measures one tick of two heads moving. The board is very
// wide, so the heads take about a thousand ticks to meet; when they do, a
// fresh game replaces the finished one (that cost is spread over those
// ticks).
func BenchmarkTronTick(b *testing.B) {
	cfg := Config{Width: 4000, Height: 12, TicksPerCount: 1, Tick: time.Millisecond}
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

// BenchmarkTronView measures what a room does for one player after each tick:
// draw the view and encode it as a frame. It uses the default board with four
// heads a few steps into a round.
func BenchmarkTronView(b *testing.B) {
	g := playingGame(b, DefaultConfig(), 4)
	for i := 0; i < 5; i++ {
		g.Tick()
	}
	if g.State() != game.StateRunning {
		b.Fatalf("State() = %v, want Running", g.State())
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.View(pid(0)).Frame()
	}
}
