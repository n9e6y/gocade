package render

import "testing"

// BenchmarkRenderFrame measures one full-frame redraw of a 70x24 canvas, the
// size of a Tron view: colored blocks, a highlighted cell and some text. Every
// player gets one of these per tick.
func BenchmarkRenderFrame(b *testing.B) {
	c := NewCanvas(70, 24)
	colors := []Color{Red, Cyan, Green, Yellow}
	for y := 2; y < 22; y++ {
		for x := 1; x < 41; x++ {
			if (x+y)%3 == 0 { // a scatter of trail cells, so styles change often
				c.Set(x, y, Cell{Rune: '█', Fg: colors[(x*y)%len(colors)]})
			}
		}
	}
	c.Set(10, 10, Cell{Rune: '@', Fg: Black, Bg: Red})
	c.Text(0, 0, "█ ann (you)  █ bob", Red)
	c.Text(0, 23, "Steer with the arrow keys or W A S D", Default)

	b.ReportAllocs()
	b.SetBytes(int64(len(c.Frame())))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Frame()
	}
}
