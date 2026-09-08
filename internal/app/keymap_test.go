package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestParseStroke(t *testing.T) {
	tests := []struct {
		spec string
		want keyStroke
		str  string
	}{
		{"h", keyStroke{key: tcell.KeyRune, ch: 'h'}, "h"},
		{"G", keyStroke{key: tcell.KeyRune, ch: 'G'}, "G"},
		{"$", keyStroke{key: tcell.KeyRune, ch: '$'}, "$"},
		{"space", keyStroke{key: tcell.KeyRune, ch: ' '}, "Space"},
		{"ctrl+d", keyStroke{key: tcell.KeyCtrlD}, "Ctrl-d"},
		{"Ctrl+U", keyStroke{key: tcell.KeyCtrlU}, "Ctrl-u"},
		{"shift+v", keyStroke{key: tcell.KeyRune, ch: 'V'}, "V"},
		{"alt+x", keyStroke{key: tcell.KeyRune, ch: 'x', alt: true}, "Alt-x"},
		{"esc", keyStroke{key: tcell.KeyEscape}, "Esc"},
		{"Left", keyStroke{key: tcell.KeyLeft}, "Left"},
		{"pgdn", keyStroke{key: tcell.KeyPgDn}, "PgDn"},
		{"f5", keyStroke{key: tcell.KeyF5}, "F5"},
	}
	for _, tt := range tests {
		got, err := parseStroke(tt.spec)
		if err != nil {
			t.Errorf("parseStroke(%q): %v", tt.spec, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseStroke(%q) = %+v, want %+v", tt.spec, got, tt.want)
		}
		if got.String() != tt.str {
			t.Errorf("%q renders as %q, want %q", tt.spec, got.String(), tt.str)
		}
	}
	for _, bad := range []string{"", "bogus", "ctrl+", "ctrl+1", "hyper+x"} {
		if _, err := parseStroke(bad); err == nil {
			t.Errorf("parseStroke(%q) should fail", bad)
		}
	}
	chord, err := parseChord(" g  g ")
	if err != nil || len(chord) != 2 || chordString(chord) != "g g" {
		t.Errorf("parseChord: %v %v", chord, err)
	}
}

func TestStrokeFromEvent(t *testing.T) {
	if got := strokeFromEvent(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone)); got != (keyStroke{key: tcell.KeyRune, ch: 'h'}) {
		t.Errorf("rune event: %+v", got)
	}
	if got := strokeFromEvent(tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl)); got != (keyStroke{key: tcell.KeyCtrlD}) {
		t.Errorf("ctrl event: %+v", got)
	}
	if got := strokeFromEvent(tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone)); got.key != tcell.KeyBackspace2 {
		t.Errorf("backspace should normalise to KeyBackspace2, got %+v", got)
	}
	if got := strokeFromEvent(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt)); !got.alt {
		t.Errorf("alt modifier lost: %+v", got)
	}
}

func TestDefaultKeymapResolvesAndValidates(t *testing.T) {
	km := defaultKeymap()
	if err := km.validate(); err != nil {
		t.Fatalf("default keymap must be conflict-free: %v", err)
	}
	g := keyStroke{key: tcell.KeyRune, ch: 'g'}
	if act, prefix := km.resolve([]keyStroke{g}); act != "" || !prefix {
		t.Errorf("single g must be a prefix only, got act=%q prefix=%v", act, prefix)
	}
	if act, _ := km.resolve([]keyStroke{g, g}); act != actFirstRow {
		t.Errorf("gg = %q, want %q", act, actFirstRow)
	}
	if act, _ := km.resolve([]keyStroke{{key: tcell.KeyCtrlD}}); act != actHalfPageDown {
		t.Errorf("ctrl-d = %q", act)
	}
	if act, _ := km.resolve([]keyStroke{{key: tcell.KeyLeft}}); act != actMoveLeft {
		t.Errorf("Left = %q", act)
	}
	if act, prefix := km.resolve([]keyStroke{{key: tcell.KeyRune, ch: 'Z'}}); act != "" || prefix {
		t.Errorf("unbound key must resolve to nothing, got %q %v", act, prefix)
	}
	if got := km.keysFor(actMoveLeft); got != "h, Left" {
		t.Errorf("keysFor(move_left) = %q", got)
	}
	for _, entry := range actionCatalog {
		if _, ok := actionByName(string(entry.act)); !ok {
			t.Errorf("actionByName(%q) failed", entry.act)
		}
	}
}

func TestKeymapValidateReportsConflicts(t *testing.T) {
	km := defaultKeymap()
	h, _ := parseChord("h")
	km.set(actQuit, [][]keyStroke{h})
	err := km.validate()
	if err == nil || !strings.Contains(err.Error(), "move_left") || !strings.Contains(err.Error(), "quit") {
		t.Errorf("duplicate binding must name both actions, got %v", err)
	}

	km = defaultKeymap()
	g, _ := parseChord("g")
	km.set(actQuit, [][]keyStroke{g})
	if err := km.validate(); err != nil {
		t.Errorf("a binding that is a prefix of another is allowed (the chord timeout resolves it), got %v", err)
	}
	if act, prefix := km.resolve(g); act != actQuit || !prefix {
		t.Errorf("g must resolve to quit and still be a prefix of gg: %q %v", act, prefix)
	}

	km = defaultKeymap()
	km.set(actQuit, nil)
	if got := km.keysFor(actQuit); got != "" {
		t.Errorf("unbound action should render empty, got %q", got)
	}
	if act, _ := km.resolve([]keyStroke{{key: tcell.KeyRune, ch: 'q'}}); act != "" {
		t.Errorf("q should be unbound after set(nil), got %q", act)
	}
}

func TestKeymapRejectsDigitBindings(t *testing.T) {
	km := defaultKeymap()
	five, _ := parseChord("5")
	km.set(actQuit, [][]keyStroke{five})
	if err := km.validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("binding a digit must be rejected, got %v", err)
	}
	km = defaultKeymap()
	gzero, _ := parseChord("g 0")
	km.set(actQuit, [][]keyStroke{gzero})
	if err := km.validate(); err != nil {
		t.Errorf("0 is not a count digit and may be bound, got %v", err)
	}
}
