package app

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// action identifies something the table can do in response to a key.
type action string

const (
	actMoveLeft     action = "move_left"
	actMoveRight    action = "move_right"
	actMoveDown     action = "move_down"
	actMoveUp       action = "move_up"
	actNextColumn   action = "next_column"
	actPrevColumn   action = "prev_column"
	actFirstRow     action = "first_row"
	actLastRow      action = "last_row"
	actFirstColumn  action = "first_column"
	actLastColumn   action = "last_column"
	actHalfPageDown action = "half_page_down"
	actHalfPageUp   action = "half_page_up"
	actPageDown     action = "page_down"
	actPageUp       action = "page_up"
	actSearch       action = "search"
	actNextMatch    action = "next_match"
	actPrevMatch    action = "prev_match"
	actFilter       action = "filter"
	actRemoveFilter action = "remove_filter"
	actSortAsc      action = "sort_asc"
	actSortDesc     action = "sort_desc"
	actToggleType   action = "toggle_type"
	actToggleWidth  action = "toggle_width"
	actStats        action = "stats"
	actHelp         action = "help"
	actQuit         action = "quit"
	actCancel       action = "cancel"
	actYank         action = "yank"
	actYankRow      action = "yank_row"
	actVisual       action = "visual"
	actVisualRow    action = "visual_row"
	actVisualSwap   action = "visual_swap"
	actDelete       action = "delete"
	actCut          action = "cut"
	actClear        action = "clear"
	actUndo         action = "undo"
	actWrite        action = "write"
)

// actionInfo describes an action for the help dialog and the config file.
type actionInfo struct {
	act     action
	section string
	help    string
	motion  bool // motions accept a count prefix and mark the cursor as moved
}

// actionCatalog lists every action in help order with its default keys.
var actionCatalog = []struct {
	actionInfo
	keys []string
}{
	{actionInfo{actMoveLeft, "Movement", "Move left (wraps to the last column)", true}, []string{"h", "left"}},
	{actionInfo{actMoveRight, "Movement", "Move right (wraps to the first column)", true}, []string{"l", "right"}},
	{actionInfo{actMoveDown, "Movement", "Move down", true}, []string{"j", "down"}},
	{actionInfo{actMoveUp, "Movement", "Move up", true}, []string{"k", "up"}},
	{actionInfo{actNextColumn, "Movement", "Next column", true}, []string{"w"}},
	{actionInfo{actPrevColumn, "Movement", "Previous column", true}, []string{"b"}},
	{actionInfo{actFirstRow, "Movement", "First row; with a count, row N", true}, []string{"g g", "home"}},
	{actionInfo{actLastRow, "Movement", "Last row; with a count, row N", true}, []string{"G", "end"}},
	{actionInfo{actFirstColumn, "Movement", "First column", true}, []string{"0"}},
	{actionInfo{actLastColumn, "Movement", "Last column", true}, []string{"$"}},
	{actionInfo{actHalfPageDown, "Movement", "Half a page down; with a count, N rows", true}, []string{"ctrl+d"}},
	{actionInfo{actHalfPageUp, "Movement", "Half a page up; with a count, N rows", true}, []string{"ctrl+u"}},
	{actionInfo{actPageDown, "Movement", "A page down; with a count, N pages", true}, []string{"pgdn", "ctrl+f"}},
	{actionInfo{actPageUp, "Movement", "A page up; with a count, N pages", true}, []string{"pgup", "ctrl+b"}},
	{actionInfo{actSearch, "Search", "Search (plain text or regex)", false}, []string{"/"}},
	{actionInfo{actNextMatch, "Search", "Next match; with a count, N matches ahead", true}, []string{"n"}},
	{actionInfo{actPrevMatch, "Search", "Previous match; with a count, N matches back", true}, []string{"N"}},
	{actionInfo{actCancel, "Search", "Clear search highlighting", false}, []string{"esc"}},
	{actionInfo{actFilter, "Filter", "Filter rows by the current column", false}, []string{"f"}},
	{actionInfo{actRemoveFilter, "Filter", "Remove the filter on the current column", false}, []string{"r"}},
	{actionInfo{actSortAsc, "Sort and types", "Sort ascending by the current column", false}, []string{"s"}},
	{actionInfo{actSortDesc, "Sort and types", "Sort descending by the current column", false}, []string{"S"}},
	{actionInfo{actToggleType, "Sort and types", "Toggle the column type (String, Number, Date)", false}, []string{"t"}},
	{actionInfo{actYank, "Yank", "Copy the current cell to the clipboard", false}, []string{"y"}},
	{actionInfo{actYankRow, "Yank", "Copy the current row to the clipboard (tab-separated)", false}, []string{"Y"}},
	{actionInfo{actVisual, "Visual", "Select a block of cells; move to extend, y to copy, Esc or q to cancel", false}, []string{"v", "ctrl+v"}},
	{actionInfo{actVisualRow, "Visual", "Select whole rows", false}, []string{"V"}},
	{actionInfo{actVisualSwap, "Visual", "Swap the anchor and the cursor of the selection", false}, []string{"o"}},
	{actionInfo{actDelete, "Edit", "Remove: dd the row, d+motion the rows (dj, dG) or columns (dl, d$) it spans; in visual mode the selected rows (V) or columns (v)", false}, []string{"d"}},
	{actionInfo{actCut, "Edit", "Remove and copy to the clipboard: the row, or the visual selection", false}, []string{"X"}},
	{actionInfo{actClear, "Edit", "Clear the cell; with a count, N cells to the right; in visual mode the selected cells", false}, []string{"x"}},
	{actionInfo{actUndo, "Edit", "Undo the last edit; with a count, N edits", false}, []string{"u"}},
	{actionInfo{actWrite, "Edit", "Write the table back to the file (the original is kept in the backup directory)", false}, []string{"W"}},
	{actionInfo{actToggleWidth, "View", "Toggle the 50 character width limit on the current column", false}, []string{"_"}},
	{actionInfo{actStats, "View", "Statistics for the current column", false}, []string{"i"}},
	{actionInfo{actHelp, "View", "Show this help", false}, []string{"?"}},
	{actionInfo{actQuit, "View", "Quit", false}, []string{"q"}},
}

// actionByName resolves a config key such as "move_left".
func actionByName(name string) (actionInfo, bool) {
	for _, entry := range actionCatalog {
		if string(entry.act) == name {
			return entry.actionInfo, true
		}
	}
	return actionInfo{}, false
}

// keyStroke is one normalised key press: a printable rune (optionally with
// Alt) or a special key such as Escape or Ctrl-d.
type keyStroke struct {
	key tcell.Key // tcell.KeyRune for printable characters
	ch  rune      // the character when key is KeyRune
	alt bool
}

// specialKeys maps config names to tcell keys.
var specialKeys = map[string]tcell.Key{
	"esc": tcell.KeyEscape, "escape": tcell.KeyEscape,
	"enter": tcell.KeyEnter, "return": tcell.KeyEnter,
	"tab": tcell.KeyTab, "backspace": tcell.KeyBackspace2, "delete": tcell.KeyDelete, "insert": tcell.KeyInsert,
	"left": tcell.KeyLeft, "right": tcell.KeyRight, "up": tcell.KeyUp, "down": tcell.KeyDown,
	"home": tcell.KeyHome, "end": tcell.KeyEnd,
	"pgup": tcell.KeyPgUp, "pageup": tcell.KeyPgUp, "pgdn": tcell.KeyPgDn, "pagedown": tcell.KeyPgDn,
	"f1": tcell.KeyF1, "f2": tcell.KeyF2, "f3": tcell.KeyF3, "f4": tcell.KeyF4, "f5": tcell.KeyF5, "f6": tcell.KeyF6,
	"f7": tcell.KeyF7, "f8": tcell.KeyF8, "f9": tcell.KeyF9, "f10": tcell.KeyF10, "f11": tcell.KeyF11, "f12": tcell.KeyF12,
}

// specialNames is the display name of each special key.
var specialNames = map[tcell.Key]string{
	tcell.KeyEscape: "Esc", tcell.KeyEnter: "Enter", tcell.KeyTab: "Tab", tcell.KeyBackspace2: "Backspace",
	tcell.KeyDelete: "Delete", tcell.KeyInsert: "Insert", tcell.KeyLeft: "Left", tcell.KeyRight: "Right",
	tcell.KeyUp: "Up", tcell.KeyDown: "Down", tcell.KeyHome: "Home", tcell.KeyEnd: "End",
	tcell.KeyPgUp: "PgUp", tcell.KeyPgDn: "PgDn",
}

// strokeFromEvent normalises a tcell key event.
func strokeFromEvent(ev *tcell.EventKey) keyStroke {
	switch ev.Key() {
	case tcell.KeyRune:
		return keyStroke{key: tcell.KeyRune, ch: ev.Rune(), alt: ev.Modifiers()&tcell.ModAlt != 0}
	case tcell.KeyBackspace:
		return keyStroke{key: tcell.KeyBackspace2}
	}
	return keyStroke{key: ev.Key()}
}

// parseStroke reads one key from its config spelling: a single character
// ("h", "G", "$"), a special name ("esc", "left", "space", "f5") or a
// modifier form ("ctrl+d", "alt+x", "shift+v").
func parseStroke(spec string) (keyStroke, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return keyStroke{}, fmt.Errorf("empty key")
	}
	if runes := []rune(spec); len(runes) == 1 {
		return keyStroke{key: tcell.KeyRune, ch: runes[0]}, nil
	}
	lower := strings.ToLower(spec)
	if lower == "space" {
		return keyStroke{key: tcell.KeyRune, ch: ' '}, nil
	}
	if k, ok := specialKeys[lower]; ok {
		return keyStroke{key: k}, nil
	}
	if mod, rest, ok := strings.Cut(lower, "+"); ok {
		r := []rune(rest)
		switch {
		case mod == "ctrl" && len(r) == 1 && r[0] >= 'a' && r[0] <= 'z':
			return keyStroke{key: tcell.Key(int(tcell.KeyCtrlA) + int(r[0]-'a'))}, nil
		case mod == "alt" && len(r) == 1:
			return keyStroke{key: tcell.KeyRune, ch: []rune(spec[strings.Index(spec, "+")+1:])[0], alt: true}, nil
		case mod == "shift" && len(r) == 1:
			return keyStroke{key: tcell.KeyRune, ch: unicode.ToUpper(r[0])}, nil
		}
	}
	return keyStroke{}, fmt.Errorf("unknown key %q", spec)
}

// parseChord reads a whitespace-separated key sequence such as "g g".
func parseChord(spec string) ([]keyStroke, error) {
	fields := strings.Fields(spec)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty key sequence")
	}
	chord := make([]keyStroke, 0, len(fields))
	for _, f := range fields {
		k, err := parseStroke(f)
		if err != nil {
			return nil, err
		}
		chord = append(chord, k)
	}
	return chord, nil
}

// String renders a key the way the help dialog shows it.
func (k keyStroke) String() string {
	switch {
	case k.key == tcell.KeyRune && k.ch == ' ':
		return "Space"
	case k.key == tcell.KeyRune && k.alt:
		return "Alt-" + string(k.ch)
	case k.key == tcell.KeyRune:
		return string(k.ch)
	case k.key >= tcell.KeyCtrlA && k.key <= tcell.KeyCtrlZ:
		return "Ctrl-" + string(rune('a'+int(k.key-tcell.KeyCtrlA)))
	}
	if name, ok := specialNames[k.key]; ok {
		return name
	}
	if k.key >= tcell.KeyF1 && k.key <= tcell.KeyF12 {
		return fmt.Sprintf("F%d", int(k.key-tcell.KeyF1)+1)
	}
	return fmt.Sprintf("key(%d)", k.key)
}

// chordString renders a key sequence for display.
func chordString(chord []keyStroke) string {
	parts := make([]string, len(chord))
	for i, k := range chord {
		parts[i] = k.String()
	}
	return strings.Join(parts, " ")
}

// keymap binds key sequences to actions.
type keymap struct {
	bindings map[action][][]keyStroke
}

// defaultKeymap returns the built-in bindings.
func defaultKeymap() *keymap {
	km := &keymap{bindings: make(map[action][][]keyStroke)}
	for _, entry := range actionCatalog {
		for _, spec := range entry.keys {
			chord, err := parseChord(spec)
			if err != nil {
				panic("default keymap: " + err.Error())
			}
			km.bindings[entry.act] = append(km.bindings[entry.act], chord)
		}
	}
	return km
}

// set replaces the bindings of an action. An empty list unbinds it.
func (km *keymap) set(act action, chords [][]keyStroke) {
	if len(chords) == 0 {
		delete(km.bindings, act)
		return
	}
	km.bindings[act] = chords
}

// resolve looks a key sequence up. act is set on an exact match; prefix
// reports that a longer binding starts with the sequence, so the caller
// should wait for more keys.
func (km *keymap) resolve(seq []keyStroke) (act action, prefix bool) {
	for a, chords := range km.bindings {
		for _, chord := range chords {
			switch {
			case len(chord) == len(seq) && chordEqual(chord, seq):
				act = a
			case len(chord) > len(seq) && chordEqual(chord[:len(seq)], seq):
				prefix = true
			}
		}
	}
	return act, prefix
}

func chordEqual(a, b []keyStroke) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validate reports a key sequence bound to two actions, one that is a prefix
// of another (neither could be resolved unambiguously), or a binding on a
// digit 1-9, which the count prefix consumes before the keymap sees it.
func (km *keymap) validate() error {
	type owner struct {
		act   action
		chord []keyStroke
	}
	var all []owner
	for a, chords := range km.bindings {
		for _, c := range chords {
			for _, k := range c {
				if k.key == tcell.KeyRune && k.ch >= '1' && k.ch <= '9' {
					return fmt.Errorf("key %q (%s): digits 1-9 are reserved for count prefixes", chordString(c), a)
				}
			}
			all = append(all, owner{a, c})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].act < all[j].act })
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			x, y := all[i], all[j]
			if len(x.chord) > len(y.chord) {
				x, y = y, x
			}
			if chordEqual(x.chord, y.chord[:len(x.chord)]) {
				if len(x.chord) == len(y.chord) {
					return fmt.Errorf("key %q is bound to both %s and %s", chordString(x.chord), x.act, y.act)
				}
				return fmt.Errorf("key %q (%s) is a prefix of %q (%s)", chordString(x.chord), x.act, chordString(y.chord), y.act)
			}
		}
	}
	return nil
}

// keysFor renders the bindings of an action for the help dialog.
func (km *keymap) keysFor(act action) string {
	chords := km.bindings[act]
	parts := make([]string, len(chords))
	for i, c := range chords {
		parts[i] = chordString(c)
	}
	return strings.Join(parts, ", ")
}
