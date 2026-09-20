package lobby

import (
	"github.com/n9e6y/gocade/internal/render"
)

// screenW is the width of the lobby screens: wide enough for the longest
// line, and for a 12-character nickname in a greeting.
const screenW = 50

// resultHint is written under a game's final screen. The game's last frame
// ends with the cursor on the bottom row of its canvas, so the CRLF starts a
// fresh line just below it. Enter, not "any key", because players are often
// still pressing game keys when a game ends and must get to read the result.
const resultHint = "\r\nPress Enter to return to the menu."

// nicknameFrame is the screen shown before a player has a name. The terminal
// has echo turned off, so the server draws what has been typed, plus a
// cursor. notice is a message to show under the prompt, or empty.
func nicknameFrame(typed, notice string) []byte {
	c := render.NewCanvas(screenW, 8)
	c.Text(2, 0, "ARENA", render.Yellow)
	c.Text(2, 2, "Choose a nickname (up to 12 characters)", render.Default)
	c.Text(2, 4, "> "+typed+"_", render.Cyan)
	c.Text(2, 6, notice, render.Yellow)
	return c.Frame()
}

// menuFrame is the game menu. Games are numbered in registry order, which is
// what the digit keys mean. notice is a message shown under the menu, or
// empty.
func menuFrame(name string, games []Entry, notice string) []byte {
	c := render.NewCanvas(screenW, 8+len(games))
	c.Text(2, 0, "ARENA", render.Yellow)
	c.Text(2, 2, "Hello, "+name+"!", render.Default)
	c.Text(2, 4, "Pick a game:", render.Default)
	for i, g := range games {
		c.Text(4, 5+i, string(rune('1'+i))+") "+g.Title, render.Cyan)
	}
	c.Text(4, 5+len(games), "q) Quit", render.Default)
	c.Text(2, 7+len(games), notice, render.Yellow)
	return c.Frame()
}
