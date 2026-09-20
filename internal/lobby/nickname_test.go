package lobby

import (
	"testing"

	"github.com/n9e6y/gocade/internal/input"
)

// typeKeys feeds a script to the editor: '\n' is Enter, '\b' is Backspace,
// anything else is a typed character. It returns whether the last key
// finished the name.
func typeKeys(e *nameEditor, script string) (done bool) {
	for _, c := range script {
		var k input.Key
		switch c {
		case '\n':
			k = input.Key{Kind: input.KindEnter}
		case '\b':
			k = input.Key{Kind: input.KindBackspace}
		default:
			k = input.Key{Kind: input.KindRune, Rune: c}
		}
		done = e.Feed(k)
	}
	return done
}

func TestNameEditor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		script   string
		wantText string // what the screen echoes after the script
		wantDone bool
		wantName string // only checked when done
	}{
		{name: "plain name", script: "bob\n", wantText: "bob", wantDone: true, wantName: "bob"},
		{name: "not done until enter", script: "bob", wantText: "bob"},
		{name: "movement and quit letters are just letters", script: "wasdq\n", wantText: "wasdq", wantDone: true, wantName: "wasdq"},
		{name: "a name that starts with q", script: "quinn\n", wantText: "quinn", wantDone: true, wantName: "quinn"},
		{name: "digits and punctuation allowed", script: "p1_x-2\n", wantText: "p1_x-2", wantDone: true, wantName: "p1_x-2"},
		{name: "backspace removes the last character", script: "bobx\b", wantText: "bob"},
		{name: "backspace then retype", script: "bxx\b\bob\n", wantText: "bob", wantDone: true, wantName: "bob"},
		{name: "backspace on empty does nothing", script: "\b\b", wantText: ""},
		{name: "enter on empty is refused", script: "\n", wantText: ""},
		{name: "enter on spaces only is refused", script: "   \n", wantText: "   "},
		{name: "surrounding spaces are trimmed from the name", script: "  bob  \n", wantText: "  bob  ", wantDone: true, wantName: "bob"},
		{name: "inner space is kept", script: "big bob\n", wantText: "big bob", wantDone: true, wantName: "big bob"},
		{name: "typing stops at the maximum length", script: "abcdefghijklmnop", wantText: "abcdefghijkl"},
		{name: "backspace works at the maximum length", script: "abcdefghijklmnop\b", wantText: "abcdefghijk"},
		{name: "after a refused enter typing continues", script: "\nbob\n", wantText: "bob", wantDone: true, wantName: "bob"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var e nameEditor
			done := typeKeys(&e, tt.script)

			if got := e.Text(); got != tt.wantText {
				t.Errorf("Text() = %q, want %q", got, tt.wantText)
			}
			if done != tt.wantDone {
				t.Errorf("done = %v, want %v", done, tt.wantDone)
			}
			if tt.wantDone {
				if got := e.Name(); got != tt.wantName {
					t.Errorf("Name() = %q, want %q", got, tt.wantName)
				}
			}
		})
	}
}

func TestNameEditor_IgnoresOtherKeys(t *testing.T) {
	t.Parallel()

	var e nameEditor
	typeKeys(&e, "bob")

	for _, kind := range []input.Kind{input.KindUp, input.KindDown, input.KindLeft, input.KindRight, input.KindCtrlC} {
		if e.Feed(input.Key{Kind: kind}) {
			t.Errorf("%v finished the name", kind)
		}
	}
	if got := e.Text(); got != "bob" {
		t.Errorf("Text() = %q after non-text keys, want %q", got, "bob")
	}
}
