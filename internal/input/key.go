// Package input decodes raw terminal bytes into key events.
//
// It is pure: no I/O, no goroutines, no clock. Bytes go in through
// Decoder.Feed and keys come out.
package input

import "fmt"

// Kind says what sort of key a Key is.
type Kind uint8

// The kinds of key the decoder produces. The zero Kind is deliberately not
// valid, so a zero Key is recognizably "no key".
const (
	KindRune      Kind = iota + 1 // a printable ASCII character; Key.Rune is set
	KindUp                        // arrow up
	KindDown                      // arrow down
	KindLeft                      // arrow left
	KindRight                     // arrow right
	KindEnter                     // CR or LF
	KindBackspace                 // DEL or BS
	KindCtrlC                     // 0x03
)

var kindNames = map[Kind]string{
	KindRune:      "Rune",
	KindUp:        "Up",
	KindDown:      "Down",
	KindLeft:      "Left",
	KindRight:     "Right",
	KindEnter:     "Enter",
	KindBackspace: "Backspace",
	KindCtrlC:     "CtrlC",
}

// String returns the kind's name, for logs and test failures.
func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Key is one decoded key press. It is comparable, so tests can use ==.
//
// Letters are always KindRune: the nickname prompt needs w, a, s, d and q as
// ordinary characters. Games ask what a key means through Direction, Digit
// and IsQuit instead.
type Key struct {
	Kind Kind
	Rune rune // set only when Kind is KindRune
}

// Dir is a movement direction.
type Dir uint8

// The four directions. The zero Dir is deliberately not valid.
const (
	DirUp Dir = iota + 1
	DirDown
	DirLeft
	DirRight
)

// Key returns the arrow key for d, the inverse of Key.Direction. It returns the
// zero Key for a Dir that is not one of the four directions.
func (d Dir) Key() Key {
	switch d {
	case DirUp:
		return Key{Kind: KindUp}
	case DirDown:
		return Key{Kind: KindDown}
	case DirLeft:
		return Key{Kind: KindLeft}
	case DirRight:
		return Key{Kind: KindRight}
	}
	return Key{}
}

// Direction reports the direction this key means: an arrow key, or w, a, s,
// d in either case.
func (k Key) Direction() (Dir, bool) {
	switch k.Kind {
	case KindUp:
		return DirUp, true
	case KindDown:
		return DirDown, true
	case KindLeft:
		return DirLeft, true
	case KindRight:
		return DirRight, true
	case KindRune:
		switch k.Rune {
		case 'w', 'W':
			return DirUp, true
		case 's', 'S':
			return DirDown, true
		case 'a', 'A':
			return DirLeft, true
		case 'd', 'D':
			return DirRight, true
		}
	}
	return 0, false
}

// Digit reports the number this key means, if it is '0' through '9'.
func (k Key) Digit() (int, bool) {
	if k.Kind == KindRune && k.Rune >= '0' && k.Rune <= '9' {
		return int(k.Rune - '0'), true
	}
	return 0, false
}

// IsQuit reports whether the key asks to quit: q, Q or Ctrl-C.
func (k Key) IsQuit() bool {
	return k.Kind == KindCtrlC || (k.Kind == KindRune && (k.Rune == 'q' || k.Rune == 'Q'))
}
