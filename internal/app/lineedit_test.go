package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// feedKeys sends a space-separated key sequence to the editor, using the
// config spellings (esc, enter, space, ctrl+r, backspace, left, ...).
func feedKeys(t *testing.T, e *lineEditor, spec string) {
	t.Helper()
	for _, tok := range strings.Fields(spec) {
		stroke, err := parseStroke(tok)
		if err != nil {
			t.Fatal(err)
		}
		if stroke.key == tcell.KeyRune {
			e.key(editKey{tcell.KeyRune, stroke.ch})
		} else {
			e.key(editKey{key: stroke.key})
		}
	}
}

func TestLineEditorVimCommands(t *testing.T) {
	cases := []struct {
		text, keys, want string
		cur              int
	}{
		// motions
		{"hello world", "w", "hello world", 6},
		{"foo.bar baz", "w", "foo.bar baz", 3},
		{"foo.bar baz", "W", "foo.bar baz", 8},
		{"foo.bar baz", "e", "foo.bar baz", 2},
		{"foo.bar baz", "E", "foo.bar baz", 6},
		{"foo bar", "$ b", "foo bar", 4},
		{"hello", "3 l", "hello", 3},
		{"hello", "9 l", "hello", 4},
		{"abcdefghijklmn", "1 0 l", "abcdefghijklmn", 10},
		{"  abc", "$ ^", "  abc", 2},
		{"abc  ", "g _", "abc  ", 2},
		{"abcdef", "4 |", "abcdef", 3},
		{"a,b,c", "f , ;", "a,b,c", 3},
		{"a,b,c,d", "t , ; x", "a,,c,d", 2},
		{"a,b,c", "$ F , , x", "a,bc", 3},
		// deleting
		{"hello world", "d w", "world", 0},
		{"hello world", "$ x", "hello worl", 9},
		{"hello world", "d d", "", 0},
		{"abc", "x x x", "", 0},
		{"abc", "3 x", "", 0},
		{"abc", "5 x", "", 0},
		{"abc", "$ X", "ac", 1},
		{"hello world", "D", "", 0},
		{"hello world", "l l D", "he", 1},
		{"a b c d", "d 2 w", "c d", 0},
		{"a b c d", "2 d w", "c d", 0},
		{"ab cd", "w d e", "ab ", 2},
		{"abc", "$ d l", "ab", 1},
		{"abc", "d h", "abc", 0},
		{"f(abc)", "l l d t )", "f()", 2},
		// text objects
		{"one two", "d i w", " two", 0},
		{"one two", "d a w", "two", 0},
		{"one two", "w d a w", "one", 2},
		{"f(a, b)", "l l d i (", "f()", 2},
		{"f(a, b)", "l l d a (", "f", 0},
		{"f(g(x), y)", "4 l d i (", "f(g(), y)", 4},
		{"x = [1, 2]", "$ d i ]", "x = []", 5},
		{`say "hi there" ok`, `c i " y o esc`, `say "yo" ok`, 6},
		// changing and inserting
		{"hello world", "c w x esc", "x world", 0},
		{"a  b", "l c w X esc", "aXb", 1},
		{"hello", "s Z esc", "Zello", 0},
		{"hello", "S Z esc", "Z", 0},
		{"hello", "A space w o r l d esc", "hello world", 10},
		{"hello", "I x esc", "xhello", 0},
		{"", "A x esc", "x", 0},
		{"hello", "R x y esc", "xyllo", 1},
		{"hello", "R x y backspace esc", "xello", 0},
		{"foo bar", "A ctrl+w esc", "foo ", 3},
		{"foo bar", "A ctrl+u esc", "", 0},
		{"abc", "l r z", "azc", 1},
		{"ab", "3 r x", "ab", 0},
		{"abc", "~ ~", "ABc", 2},
		{"abc", "2 ~", "ABc", 2},
		// registers
		{"abc def", "y w $ p", "abc defabc ", 10},
		{"abc def", "y w 0 P", "abc abc def", 3},
		{"abc", "d d p", "abc", 2},
		{"ab", "y y $ p", "abab", 3},
		// undo, redo, repeat
		{"abc", "x u", "abc", 0},
		{"abc", "x u ctrl+r", "bc", 0},
		{"a a a", "x . .", " a", 0},
		{"x", "i a b esc .", "aabbx", 2},
		// visual
		{"hello world", "v e d", " world", 0},
		{"hello world", "w v e y 0 P", "worldhello world", 4},
		{"hello world", "v i w d", " world", 0},
		{"abcdef", "l v l l o d", "aef", 1},
		{"abc", "v l U", "ABc", 0},
		{"abcd", "v l r x", "xxcd", 1},
		{"abc def", "y i w w v e p", "abc abc", 6},
		{"abc", "v esc x", "bc", 0},
	}
	for _, tc := range cases {
		lineRegister, lineLastChange = nil, nil
		e := newLineEditor(tc.text)
		feedKeys(t, e, tc.keys)
		if got := string(e.text); got != tc.want || e.cur != tc.cur {
			t.Errorf("%q + %q = %q cur %d, want %q cur %d", tc.text, tc.keys, got, e.cur, tc.want, tc.cur)
		}
		if e.mode != editNormal {
			t.Errorf("%q + %q: mode %v, want normal", tc.text, tc.keys, e.mode)
		}
		if e.done {
			t.Errorf("%q + %q: editor closed", tc.text, tc.keys)
		}
	}
}

func TestLineEditorModesAndClosing(t *testing.T) {
	e := newLineEditor("abc")
	feedKeys(t, e, "x enter")
	if !e.done || !e.applied || string(e.text) != "bc" {
		t.Errorf("Enter must apply: done=%v applied=%v %q", e.done, e.applied, e.text)
	}
	e = newLineEditor("abc")
	feedKeys(t, e, "x esc")
	if !e.done || e.applied {
		t.Errorf("Esc in normal mode must cancel: done=%v applied=%v", e.done, e.applied)
	}
	e = newLineEditor("abc")
	feedKeys(t, e, "d esc x")
	if e.done || string(e.text) != "bc" {
		t.Errorf("Esc with a pending operator only cancels the operator: done=%v %q", e.done, e.text)
	}
	e = newLineEditor("abc")
	feedKeys(t, e, "i")
	if e.mode != editInsert {
		t.Error("i must enter insert mode")
	}
	feedKeys(t, e, "z enter")
	if !e.done || !e.applied || string(e.text) != "zabc" {
		t.Errorf("Enter in insert mode applies: %q", e.text)
	}
	e = newLineEditor("abc")
	feedKeys(t, e, "R")
	if e.mode != editReplace {
		t.Error("R must enter replace mode")
	}
	feedKeys(t, e, "v")
	e = newLineEditor("abc")
	feedKeys(t, e, "v")
	if e.mode != editVisual {
		t.Error("v must enter visual mode")
	}
	feedKeys(t, e, "l enter")
	if !e.done || !e.applied {
		t.Error("Enter in visual mode applies")
	}
	// The register and the last change outlive the cell.
	lineRegister, lineLastChange = nil, nil
	e = newLineEditor("one two")
	feedKeys(t, e, "y i w")
	e2 := newLineEditor("abc")
	feedKeys(t, e2, "p")
	if got := string(e2.text); got != "aonebc" {
		t.Errorf("the register must carry over: %q", got)
	}
	feedKeys(t, e, "x") // the last change is now "x", and the register "o"
	e3 := newLineEditor("abc")
	feedKeys(t, e3, ". p")
	if got := string(e3.text); got != "bac" || e3.cur != 1 { // the replayed x deletes "a" into the register, as in vim
		t.Errorf(". must carry over: %q cur %d", got, e3.cur)
	}
	if got, _ := findChar([]rune("abc"), 0, 'f', 'z', 1, false); got != 0 {
		t.Error("findChar miss")
	}
	if lo, hi, ok := textObject([]rune("abc"), 0, '(', false); ok || lo != 0 || hi != 0 {
		t.Error("no bracket pair must fail")
	}
	if _, _, ok := textObject(nil, 0, 'w', false); ok {
		t.Error("empty text has no objects")
	}
}
