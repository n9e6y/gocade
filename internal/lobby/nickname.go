package lobby

import (
	"strings"

	"github.com/n9e6y/gocade/internal/input"
)

// maxNameLen is the longest nickname, in characters. It keeps names inside
// the game screens.
const maxNameLen = 12

// nameEditor is the state of a nickname being typed. The terminal has echo
// turned off, so the server draws the text itself; Text is what to draw.
//
// It is pure state: keys in, text out. The decoder hands it letters as plain
// runes, so w, a, s, d and q are ordinary characters here.
type nameEditor struct {
	text []byte // ASCII only: the decoder never produces anything else
}

// Feed applies one key and reports whether the name is now finished, which
// happens when Enter is pressed with a non-blank name. Other keys that mean
// nothing to a text field (arrows, Ctrl-C) are ignored.
func (e *nameEditor) Feed(k input.Key) (done bool) {
	switch k.Kind {
	case input.KindRune:
		if len(e.text) < maxNameLen {
			e.text = append(e.text, byte(k.Rune))
		}
	case input.KindBackspace:
		if len(e.text) > 0 {
			e.text = e.text[:len(e.text)-1]
		}
	case input.KindEnter:
		return e.Name() != ""
	}
	return false
}

// Text is what has been typed so far, exactly as typed.
func (e *nameEditor) Text() string {
	return string(e.text)
}

// Name is the finished nickname: the text without surrounding spaces.
func (e *nameEditor) Name() string {
	return strings.TrimSpace(string(e.text))
}
