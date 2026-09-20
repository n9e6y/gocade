package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update rewrites the golden files: go test ./internal/render -update
// Review the diff (and cat the files in a terminal) before committing.
var update = flag.Bool("update", false, "rewrite golden files in testdata")

func TestNewCanvas_Dimensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{name: "normal", w: 3, h: 2, wantW: 3, wantH: 2},
		{name: "one cell", w: 1, h: 1, wantW: 1, wantH: 1},
		{name: "zero width", w: 0, h: 5, wantW: 0, wantH: 0},
		{name: "negative width", w: -1, h: 4, wantW: 0, wantH: 0},
		{name: "negative height", w: 5, h: -2, wantW: 0, wantH: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewCanvas(tt.w, tt.h)
			if c.Width() != tt.wantW || c.Height() != tt.wantH {
				t.Errorf("size = %dx%d, want %dx%d", c.Width(), c.Height(), tt.wantW, tt.wantH)
			}
		})
	}
}

func TestCanvas_SetAndCell(t *testing.T) {
	t.Parallel()

	x := Cell{Rune: 'x', Fg: Red}

	tests := []struct {
		name   string
		w, h   int
		px, py int
		stored bool // whether the cell should land on the canvas
	}{
		{name: "top left", w: 3, h: 2, px: 0, py: 0, stored: true},
		{name: "bottom right", w: 3, h: 2, px: 2, py: 1, stored: true},
		{name: "right edge", w: 3, h: 2, px: 3, py: 0},
		{name: "bottom edge", w: 3, h: 2, px: 0, py: 2},
		{name: "negative x", w: 3, h: 2, px: -1, py: 0},
		{name: "negative y", w: 3, h: 2, px: 0, py: -1},
		{name: "empty canvas", w: 0, h: 0, px: 0, py: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewCanvas(tt.w, tt.h)
			c.Set(tt.px, tt.py, x) // must never panic, even out of bounds

			want := Cell{}
			if tt.stored {
				want = x
			}
			if got := c.Cell(tt.px, tt.py); got != want {
				t.Errorf("Cell(%d,%d) = %+v, want %+v", tt.px, tt.py, got, want)
			}

			// An ignored Set must not have landed anywhere else either.
			stored := 0
			for cy := 0; cy < c.Height(); cy++ {
				for cx := 0; cx < c.Width(); cx++ {
					if c.Cell(cx, cy) != (Cell{}) {
						stored++
					}
				}
			}
			wantStored := 0
			if tt.stored {
				wantStored = 1
			}
			if stored != wantStored {
				t.Errorf("%d cells set, want %d", stored, wantStored)
			}
		})
	}
}

func TestCanvas_Text(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		x, y   int
		s      string
		wantAt map[[2]int]rune // cells that must hold these runes
		w, h   int
	}{
		{name: "fits", x: 1, y: 0, s: "abc", w: 6, h: 1,
			wantAt: map[[2]int]rune{{1, 0}: 'a', {2, 0}: 'b', {3, 0}: 'c', {0, 0}: 0, {4, 0}: 0}},
		{name: "clipped at right edge", x: 3, y: 0, s: "abcd", w: 5, h: 1,
			wantAt: map[[2]int]rune{{3, 0}: 'a', {4, 0}: 'b'}},
		{name: "starts left of canvas", x: -2, y: 0, s: "abcd", w: 5, h: 1,
			wantAt: map[[2]int]rune{{0, 0}: 'c', {1, 0}: 'd', {2, 0}: 0}},
		{name: "row out of bounds", x: 0, y: 3, s: "abc", w: 5, h: 2,
			wantAt: map[[2]int]rune{{0, 0}: 0, {0, 1}: 0}},
		{name: "empty string", x: 0, y: 0, s: "", w: 3, h: 1,
			wantAt: map[[2]int]rune{{0, 0}: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewCanvas(tt.w, tt.h)
			c.Text(tt.x, tt.y, tt.s, Cyan)

			for pos, want := range tt.wantAt {
				cell := c.Cell(pos[0], pos[1])
				if cell.Rune != want {
					t.Errorf("Cell(%d,%d).Rune = %q, want %q", pos[0], pos[1], cell.Rune, want)
				}
				if want != 0 && cell.Fg != Cyan {
					t.Errorf("Cell(%d,%d).Fg = %v, want Cyan", pos[0], pos[1], cell.Fg)
				}
			}
		})
	}
}

// TestCanvas_Frame_Exact spells out the exact bytes of tiny frames, so the
// escape-code rules are readable in one place.
func TestCanvas_Frame_Exact(t *testing.T) {
	t.Parallel()

	const head = "\x1b[?25l\x1b[H" // hide cursor, cursor home
	const tail = "\x1b[J"          // clear whatever an older, larger frame left below

	tests := []struct {
		name  string
		build func() *Canvas
		want  string
	}{
		{
			name:  "empty canvas",
			build: func() *Canvas { return NewCanvas(0, 0) },
			want:  head + tail,
		},
		{
			name:  "blank cells render as spaces",
			build: func() *Canvas { return NewCanvas(2, 1) },
			want:  head + "  " + tail,
		},
		{
			name: "style is emitted only when it changes",
			build: func() *Canvas {
				c := NewCanvas(3, 1)
				c.Set(0, 0, Cell{Rune: 'a', Fg: Red})
				c.Set(1, 0, Cell{Rune: 'b', Fg: Red})
				c.Set(2, 0, Cell{Rune: 'c'})
				return c
			},
			want: head + "\x1b[0;31mab" + "\x1b[0mc" + tail,
		},
		{
			name: "foreground and background",
			build: func() *Canvas {
				c := NewCanvas(1, 1)
				c.Set(0, 0, Cell{Rune: '@', Fg: Black, Bg: White})
				return c
			},
			// Black is ANSI 30 and White is background 47. The row ends
			// styled, so it is reset before the frame ends.
			want: head + "\x1b[0;30;47m@" + "\x1b[0m" + tail,
		},
		{
			name: "rows are separated by CRLF and styles reset at row end",
			build: func() *Canvas {
				c := NewCanvas(1, 2)
				c.Set(0, 0, Cell{Rune: 'x', Fg: Green})
				c.Set(0, 1, Cell{Rune: 'y', Fg: Green})
				return c
			},
			want: head + "\x1b[0;32mx\x1b[0m\r\n" + "\x1b[0;32my\x1b[0m" + tail,
		},
		{
			name: "control runes are drawn as spaces so players cannot inject escapes",
			build: func() *Canvas {
				c := NewCanvas(3, 1)
				c.Set(0, 0, Cell{Rune: '\x1b'})
				c.Set(1, 0, Cell{Rune: '\n'})
				c.Set(2, 0, Cell{Rune: '\x7f'})
				return c
			},
			want: head + "   " + tail,
		},
		{
			name: "unknown color falls back to default",
			build: func() *Canvas {
				c := NewCanvas(1, 1)
				c.Set(0, 0, Cell{Rune: 'z', Fg: Color(200)})
				return c
			},
			want: head + "z" + tail,
		},
		{
			name: "multi-byte runes are written as UTF-8",
			build: func() *Canvas {
				c := NewCanvas(1, 1)
				c.Set(0, 0, Cell{Rune: '█'})
				return c
			},
			want: head + "█" + tail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := string(tt.build().Frame()); got != tt.want {
				t.Errorf("Frame() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestCanvas_Frame_Golden locks down the output of realistic frames. View a
// golden file in a terminal with `cat internal/render/testdata/x.golden`.
func TestCanvas_Frame_Golden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() *Canvas
	}{
		{name: "empty_3x2", build: func() *Canvas { return NewCanvas(3, 2) }},
		{name: "colors", build: buildColors},
		{name: "tron_highlight", build: buildTronHighlight},
		{name: "text_label", build: buildTextLabel},
		{name: "style_runs", build: buildStyleRuns},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.build().Frame()
			path := filepath.Join("testdata", tt.name+".golden")

			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatalf("mkdir testdata: %v", err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("frame differs from %s\ngot:  %q\nwant: %q\nrun with -update if the change is intended", path, got, want)
			}
		})
	}
}

// buildColors draws every foreground color once.
func buildColors() *Canvas {
	names := "KRGYBMCW" // Black Red Green Yellow Blue Magenta Cyan White
	c := NewCanvas(len(names)+1, 2)
	for i, ch := range names {
		c.Set(i, 0, Cell{Rune: ch, Fg: Color(i + 1)})
		c.Set(i, 1, Cell{Rune: ' ', Bg: Color(i + 1)})
	}
	c.Set(len(names), 0, Cell{Rune: '.'})
	return c
}

// buildTronHighlight is a small Tron board: two trails and the viewer's own
// head shown with a background so it stands out.
func buildTronHighlight() *Canvas {
	c := NewCanvas(8, 4)
	for x := 0; x < 8; x++ {
		c.Set(x, 0, Cell{Rune: '#', Fg: White}) // wall
		c.Set(x, 3, Cell{Rune: '#', Fg: White})
	}
	for x := 1; x <= 3; x++ {
		c.Set(x, 1, Cell{Rune: '█', Fg: Red})
	}
	c.Set(4, 1, Cell{Rune: '@', Fg: Black, Bg: Red}) // own head
	for x := 4; x <= 6; x++ {
		c.Set(x, 2, Cell{Rune: '█', Fg: Cyan})
	}
	c.Set(6, 2, Cell{Rune: 'O', Fg: Cyan}) // opponent head
	return c
}

func buildTextLabel() *Canvas {
	c := NewCanvas(14, 2)
	c.Text(0, 0, "Score: 3", Cyan)
	c.Text(0, 1, "Your turn", Yellow)
	return c
}

// buildStyleRuns checks that neighbours with the same style share one escape.
func buildStyleRuns() *Canvas {
	c := NewCanvas(8, 1)
	for x := 0; x < 3; x++ {
		c.Set(x, 0, Cell{Rune: 'r', Fg: Red})
	}
	for x := 3; x < 5; x++ {
		c.Set(x, 0, Cell{Rune: 'g', Fg: Green})
	}
	c.Set(5, 0, Cell{Rune: '-'})
	c.Set(6, 0, Cell{Rune: 'b', Fg: Blue, Bg: Yellow})
	c.Set(7, 0, Cell{Rune: 'b', Fg: Blue, Bg: Yellow})
	return c
}
