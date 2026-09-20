package input

import (
	"slices"
	"testing"
)

// r is shorthand for a printable-character key.
func r(c rune) Key { return Key{Kind: KindRune, Rune: c} }

// k is shorthand for a special key such as an arrow.
func k(kind Kind) Key { return Key{Kind: kind} }

func TestDecoder_Keys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []Key
	}{
		{name: "letter", in: "h", want: []Key{r('h')}},
		{name: "wasd are plain runes", in: "wasd", want: []Key{r('w'), r('a'), r('s'), r('d')}},
		{name: "q is a plain rune", in: "q", want: []Key{r('q')}},
		{name: "digits", in: "0159", want: []Key{r('0'), r('1'), r('5'), r('9')}},
		{name: "space", in: " ", want: []Key{r(' ')}},
		{name: "enter CR", in: "\r", want: []Key{k(KindEnter)}},
		{name: "enter LF", in: "\n", want: []Key{k(KindEnter)}},
		{name: "backspace DEL", in: "\x7f", want: []Key{k(KindBackspace)}},
		{name: "backspace BS", in: "\x08", want: []Key{k(KindBackspace)}},
		{name: "ctrl-c", in: "\x03", want: []Key{k(KindCtrlC)}},
		{name: "arrow up", in: "\x1b[A", want: []Key{k(KindUp)}},
		{name: "arrow down", in: "\x1b[B", want: []Key{k(KindDown)}},
		{name: "arrow right", in: "\x1b[C", want: []Key{k(KindRight)}},
		{name: "arrow left", in: "\x1b[D", want: []Key{k(KindLeft)}},
		{name: "ss3 up", in: "\x1bOA", want: []Key{k(KindUp)}},
		{name: "ss3 down", in: "\x1bOB", want: []Key{k(KindDown)}},
		{name: "ss3 right", in: "\x1bOC", want: []Key{k(KindRight)}},
		{name: "ss3 left", in: "\x1bOD", want: []Key{k(KindLeft)}},
		{name: "mixed", in: "a\x1b[Bb\r", want: []Key{r('a'), k(KindDown), r('b'), k(KindEnter)}},
		{name: "empty input", in: "", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var d Decoder
			got := d.Feed([]byte(tt.in))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Feed(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecoder_EveryPrintableByteIsARune(t *testing.T) {
	t.Parallel()

	for b := byte(0x20); b <= 0x7e; b++ {
		var d Decoder
		got := d.Feed([]byte{b})
		want := []Key{r(rune(b))}
		if !slices.Equal(got, want) {
			t.Errorf("Feed(%#x) = %v, want %v", b, got, want)
		}
	}
}

// TestDecoder_SplitSequences checks the reason the decoder is stateful: TCP
// may deliver an escape sequence in pieces, and the result must not depend on
// where the pieces break.
func TestDecoder_SplitSequences(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"\x1b[A",
		"\x1bOB",
		"\x1b[1;5C",
		"a\x1b[Db",
		"\x1b\x1b[A",
		"\x1b[3~x",
		"\x1bx",
	}

	for _, in := range inputs {
		var whole Decoder
		want := whole.Feed([]byte(in))

		// Every two-way split.
		for i := 0; i <= len(in); i++ {
			var d Decoder
			got := append(d.Feed([]byte(in[:i])), d.Feed([]byte(in[i:]))...)
			if !slices.Equal(got, want) {
				t.Errorf("input %q split at %d: got %v, want %v", in, i, got, want)
			}
		}

		// One byte at a time.
		var d Decoder
		var got []Key
		for i := 0; i < len(in); i++ {
			got = append(got, d.Feed([]byte{in[i]})...)
		}
		if !slices.Equal(got, want) {
			t.Errorf("input %q byte by byte: got %v, want %v", in, got, want)
		}
	}
}

func TestDecoder_Malformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []Key
	}{
		{name: "double ESC drops the first", in: "\x1b\x1b[A", want: []Key{k(KindUp)}},
		{name: "ESC then letter", in: "\x1bx", want: []Key{r('x')}},
		{name: "CSI with parameters still yields the arrow", in: "\x1b[1;5A", want: []Key{k(KindUp)}},
		{name: "CSI with unknown final is ignored", in: "\x1b[3~", want: nil},
		{name: "CSI aborted by control byte", in: "\x1b[\x01", want: nil},
		{name: "CSI aborted by Enter, which still counts", in: "\x1b[\r", want: []Key{k(KindEnter)}},
		{name: "CSI aborted by ESC starts a new sequence", in: "\x1b[\x1b[B", want: []Key{k(KindDown)}},
		{name: "SS3 with unknown final", in: "\x1bOx", want: []Key{r('x')}},
		{name: "NUL is ignored", in: "\x00", want: nil},
		{name: "non-ASCII bytes are ignored", in: "\xc3\xa9\xff", want: nil},
		{name: "lone trailing ESC produces nothing yet", in: "a\x1b", want: []Key{r('a')}},
		{name: "unknown control bytes are ignored", in: "\x01\x02\x04\x1f", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var d Decoder
			got := d.Feed([]byte(tt.in))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Feed(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestDecoder_LoneESCWaitsForNextByte documents the trade-off: without a
// clock the decoder cannot tell a bare ESC from the start of a split
// sequence, so it waits.
func TestDecoder_LoneESCWaitsForNextByte(t *testing.T) {
	t.Parallel()

	var d Decoder
	if got := d.Feed([]byte{0x1b}); got != nil {
		t.Fatalf("Feed(ESC) = %v, want nil", got)
	}
	got := d.Feed([]byte("[A"))
	if want := []Key{k(KindUp)}; !slices.Equal(got, want) {
		t.Errorf("continuation = %v, want %v", got, want)
	}
}

// FuzzDecoder feeds arbitrary bytes, split at an arbitrary point, and checks
// properties that must hold for every input. Run it with:
//
//	go test -fuzz=FuzzDecoder -fuzztime=30s ./internal/input
func FuzzDecoder(f *testing.F) {
	seeds := []string{
		"", "hello", "\x1b[A", "\x1bOD", "\x1b[1;5C", "\x1b[3~", "\x1b\x1b[", "\x1b[\x01",
		"\r\n", "\x7f\x08\x03", "\xff\xfe", "a\x1b[Bb", "\x1b",
	}
	for _, s := range seeds {
		f.Add([]byte(s), 0)
		f.Add([]byte(s), len(s)/2)
	}

	f.Fuzz(func(t *testing.T, data []byte, split int) {
		split = int(uint(split) % uint(len(data)+1))

		var whole Decoder
		want := whole.Feed(data)

		var d Decoder
		got := append(d.Feed(data[:split]), d.Feed(data[split:])...)

		if !slices.Equal(got, want) {
			t.Fatalf("split at %d changed the result: got %v, want %v", split, got, want)
		}

		for _, key := range want {
			switch key.Kind {
			case KindRune:
				if key.Rune < 0x20 || key.Rune > 0x7e {
					t.Fatalf("rune key %q is not printable ASCII", key.Rune)
				}
			case KindUp, KindDown, KindLeft, KindRight, KindEnter, KindBackspace, KindCtrlC:
				if key.Rune != 0 {
					t.Fatalf("special key %v carries rune %q", key.Kind, key.Rune)
				}
			default:
				t.Fatalf("invalid kind %d", key.Kind)
			}
		}
	})
}
