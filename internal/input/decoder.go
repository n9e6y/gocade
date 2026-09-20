package input

// state is where the decoder is inside an escape sequence.
type state uint8

const (
	stateGround state = iota // not inside a sequence
	stateEsc                 // saw ESC
	stateCSI                 // saw ESC [
	stateSS3                 // saw ESC O
)

const esc = 0x1b

// Decoder turns bytes into keys. It is a small state machine, and it is
// stateful because TCP may split an escape sequence (the three bytes of an
// arrow key) across two reads: the state remembers where we stopped.
//
// The zero Decoder is ready to use. A Decoder must not be shared between
// goroutines; each session owns its own.
//
// Two limits follow from the decoder having no clock and no buffer:
//   - A lone ESC byte waits for the next byte, because it cannot be told
//     apart from the start of a split sequence.
//   - Only ASCII is decoded. Other bytes (including UTF-8) are ignored.
type Decoder struct {
	st state
}

// Feed consumes p and returns the keys completed by it, in order. It returns
// nil when p completes no key. Bytes that belong to an unfinished sequence
// are remembered for the next call.
func (d *Decoder) Feed(p []byte) []Key {
	var keys []Key
	for _, b := range p {
		keys = d.step(b, keys)
	}
	return keys
}

// step advances the state machine by one byte, appending any finished key.
func (d *Decoder) step(b byte, keys []Key) []Key {
	switch d.st {
	case stateEsc:
		switch b {
		case '[':
			d.st = stateCSI
			return keys
		case 'O':
			d.st = stateSS3
			return keys
		}
		// Not a sequence we know: forget the ESC and treat b normally.
		d.st = stateGround
		return d.ground(b, keys)

	case stateCSI:
		// Parameters and intermediates (0x20-0x3F) are skipped, not stored,
		// so a hostile client cannot make us buffer without limit.
		if b >= 0x20 && b <= 0x3f {
			return keys
		}
		d.st = stateGround
		if b >= 0x40 && b <= 0x7e { // final byte
			if key, ok := arrow(b); ok {
				return append(keys, key)
			}
			return keys // some other sequence, such as ESC [ 3 ~: ignore it
		}
		return d.ground(b, keys) // malformed: abort, treat b normally

	case stateSS3:
		d.st = stateGround
		if key, ok := arrow(b); ok {
			return append(keys, key)
		}
		return d.ground(b, keys)
	}

	return d.ground(b, keys)
}

// ground handles a byte that is not inside a sequence.
func (d *Decoder) ground(b byte, keys []Key) []Key {
	switch {
	case b == esc:
		d.st = stateEsc
	case b == '\r' || b == '\n':
		keys = append(keys, Key{Kind: KindEnter})
	case b == 0x7f || b == 0x08:
		keys = append(keys, Key{Kind: KindBackspace})
	case b == 0x03:
		keys = append(keys, Key{Kind: KindCtrlC})
	case b >= 0x20 && b <= 0x7e:
		keys = append(keys, Key{Kind: KindRune, Rune: rune(b)})
	}
	return keys
}

// arrow maps the final byte of an arrow-key sequence to its key.
func arrow(final byte) (Key, bool) {
	switch final {
	case 'A':
		return Key{Kind: KindUp}, true
	case 'B':
		return Key{Kind: KindDown}, true
	case 'C':
		return Key{Kind: KindRight}, true
	case 'D':
		return Key{Kind: KindLeft}, true
	}
	return Key{}, false
}
