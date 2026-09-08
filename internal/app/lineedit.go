package app

import (
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// lineEditor edits one line of text, the value of a cell, the way vim edits a
// line: a normal sub-mode with motions, operators, text objects, counts,
// registers, undo and dot-repeat, an insert and a replace sub-mode, and a
// charwise visual sub-mode. Enter applies the text, Esc in normal mode
// cancels (the convention of vim's command line and of sc-im). The logic is
// pure: keys go in, text and cursor come out, so it can be driven by tests.

// editMode is the sub-mode of the line editor.
type editMode int

const (
	editNormal editMode = iota
	editInsert
	editReplace
	editVisual
)

// editKey is one key press: a printable rune (key == tcell.KeyRune) or a
// special key such as Escape, Enter, Backspace or Ctrl-r.
type editKey struct {
	key tcell.Key
	ch  rune
}

// editKeyOf normalises a tcell event into an editKey.
func editKeyOf(ev *tcell.EventKey) editKey {
	switch ev.Key() {
	case tcell.KeyRune:
		return editKey{tcell.KeyRune, ev.Rune()}
	case tcell.KeyBackspace:
		return editKey{key: tcell.KeyBackspace2}
	}
	return editKey{key: ev.Key()}
}

// Session-wide editor state shared by every cell: the unnamed register and
// the last change for ".", which in vim outlive the line being edited.
var (
	lineRegister   []rune
	lineLastChange []editKey
)

// editSnapshot is one undo state.
type editSnapshot struct {
	text []rune
	cur  int
}

type lineEditor struct {
	text []rune
	cur  int
	mode editMode

	count   int  // count being typed; 0 when none
	op      rune // pending operator d, c or y; 0 when none
	opCount int  // count typed before the operator
	pending rune // command waiting for a character: r f F t T g, or i/a for a text object
	anchor  int  // visual mode anchor

	lastFind     rune // character of the last f/F/t/T, for ; and ,
	lastFindKind rune

	undo, redo  []editSnapshot
	replaceBase []rune // text when R started; Backspace restores from it

	cmdKeys    []editKey // keys of the command in progress, recorded for .
	cmdChanged bool      // the command in progress changed the text
	replaying  bool      // . is replaying lineLastChange

	done    bool // Enter or Esc closed the editor
	applied bool // closed with Enter: the text is to be stored
}

// newLineEditor starts editing text in normal mode with the cursor at the
// first character.
func newLineEditor(text string) *lineEditor {
	return &lineEditor{text: []rune(text)}
}

// startInsert switches to insert mode at index at (clamped to the text).
func (e *lineEditor) startInsert(at int) {
	e.snapshot()
	e.mode = editInsert
	e.cur = clampInt(at, 0, len(e.text))
	e.cmdChanged = true
}

// key handles one key press.
func (e *lineEditor) key(k editKey) {
	if !e.replaying {
		e.cmdKeys = append(e.cmdKeys, k)
	}
	switch e.mode {
	case editInsert, editReplace:
		e.insertKey(k)
	case editVisual:
		e.visualKey(k)
	default:
		e.normalKey(k)
	}
	e.clampCursor()
}

// clampCursor keeps the cursor on a character in normal and visual mode and
// lets it sit after the last character while inserting.
func (e *lineEditor) clampCursor() {
	limit := len(e.text)
	if e.mode == editNormal || e.mode == editVisual {
		limit = max(len(e.text)-1, 0)
	}
	e.cur = clampInt(e.cur, 0, limit)
}

// snapshot records the text before a change so u can restore it.
func (e *lineEditor) snapshot() {
	e.undo = append(e.undo, editSnapshot{append([]rune(nil), e.text...), e.cur})
	e.redo = nil
}

// restore swaps the current state with the top of from, moving it to onto.
func (e *lineEditor) restore(from *[]editSnapshot, onto *[]editSnapshot) {
	if len(*from) == 0 {
		return
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*onto = append(*onto, editSnapshot{e.text, e.cur})
	e.text, e.cur = s.text, s.cur
}

// finishCommand ends the command in progress: a change is remembered for .
// and the pending state is cleared.
func (e *lineEditor) finishCommand() {
	if e.cmdChanged && !e.replaying && len(e.cmdKeys) > 0 {
		lineLastChange = append([]editKey(nil), e.cmdKeys...)
	}
	e.cmdKeys, e.cmdChanged = nil, false
	e.count, e.op, e.opCount, e.pending = 0, 0, 0, 0
}

// cancelPending drops a half-typed command (vim beeps).
func (e *lineEditor) cancelPending() {
	e.cmdKeys, e.cmdChanged = nil, false
	e.count, e.op, e.opCount, e.pending = 0, 0, 0, 0
}

// takeCount returns the typed count (0 when none) and the count to repeat
// with (at least 1), multiplied by the count typed before a pending operator.
func (e *lineEditor) takeCount() (raw, n int) {
	raw, n = e.count, max(e.count, 1)
	if e.opCount > 0 {
		n *= e.opCount
		if raw > 0 {
			raw *= e.opCount
		}
	}
	e.count = 0
	return raw, n
}

// normalKey handles a key in normal mode.
func (e *lineEditor) normalKey(k editKey) {
	switch k.key {
	case tcell.KeyEnter:
		e.done, e.applied = true, true
		return
	case tcell.KeyEscape:
		if e.op != 0 || e.pending != 0 || e.count != 0 {
			e.cancelPending()
			return
		}
		e.done = true
		return
	case tcell.KeyLeft:
		k = editKey{tcell.KeyRune, 'h'}
	case tcell.KeyRight:
		k = editKey{tcell.KeyRune, 'l'}
	case tcell.KeyHome:
		k = editKey{tcell.KeyRune, '0'}
	case tcell.KeyEnd:
		k = editKey{tcell.KeyRune, '$'}
	case tcell.KeyBackspace2:
		k = editKey{tcell.KeyRune, 'h'}
	case tcell.KeyDelete:
		k = editKey{tcell.KeyRune, 'x'}
	case tcell.KeyCtrlR:
		e.restore(&e.redo, &e.undo)
		e.finishCommand()
		return
	}
	if k.key != tcell.KeyRune {
		e.cancelPending()
		return
	}
	r := k.ch
	if e.pending != 0 {
		e.pendingChar(r)
		return
	}
	if r >= '1' && r <= '9' || (r == '0' && e.count > 0) {
		e.count = e.count*10 + int(r-'0')
		return
	}
	if e.op != 0 {
		e.operatorKey(r)
		return
	}
	raw, n := e.takeCount()
	switch r {
	case 'd', 'c', 'y':
		e.op, e.opCount = r, raw
	case 'r', 'f', 'F', 't', 'T', 'g':
		e.pending, e.count = r, raw
	case 'x':
		e.deleteRange(e.cur, e.cur+n, false)
		e.finishCommand()
	case 'X':
		e.deleteRange(e.cur-n, e.cur, false)
		e.finishCommand()
	case 's':
		e.deleteRange(e.cur, e.cur+n, true)
	case 'S':
		e.deleteRange(0, len(e.text), true)
	case 'D':
		e.deleteRange(e.cur, len(e.text), false)
		e.finishCommand()
	case 'C':
		e.deleteRange(e.cur, len(e.text), true)
	case 'Y':
		lineRegister = append([]rune(nil), e.text...)
		e.finishCommand()
	case '~':
		e.snapshot()
		hi := min(e.cur+n, len(e.text))
		for i := e.cur; i < hi; i++ {
			e.text[i] = toggleCase(e.text[i])
		}
		e.cur, e.cmdChanged = hi, true
		e.finishCommand()
	case 'p', 'P':
		e.paste(r == 'p', n)
		e.finishCommand()
	case 'u':
		e.restore(&e.undo, &e.redo)
		e.finishCommand()
	case '.':
		e.repeatLastChange()
	case 'i':
		e.startInsert(e.cur)
	case 'a':
		e.startInsert(e.cur + 1)
	case 'I':
		e.startInsert(firstNonBlank(e.text))
	case 'A':
		e.startInsert(len(e.text))
	case 'R':
		e.snapshot()
		e.mode, e.replaceBase, e.cmdChanged = editReplace, append([]rune(nil), e.text...), true
	case 'v':
		e.mode, e.anchor = editVisual, e.cur
		e.cancelPending()
	default:
		target, _, ok := e.motion(r, raw, n, false)
		if ok {
			e.cur = target
		}
		e.finishCommand()
	}
}

// operatorKey handles the key after d, c or y: the operator itself for the
// whole line, i/a for a text object, or a motion.
func (e *lineEditor) operatorKey(r rune) {
	switch r {
	case e.op:
		e.opRange(0, len(e.text))
		return
	case 'i', 'a':
		e.pending = r
		return
	case 'r', 'f', 'F', 't', 'T', 'g':
		e.pending = r
		return
	}
	raw, n := e.takeCount()
	target, inclusive, ok := e.motion(r, raw, n, true)
	if !ok {
		e.cancelPending()
		return
	}
	e.opMotion(target, inclusive)
}

// opMotion applies the pending operator from the cursor to a motion target.
func (e *lineEditor) opMotion(target int, inclusive bool) {
	lo, hi := min(e.cur, target), max(e.cur, target)
	if inclusive {
		hi++
	}
	e.opRange(lo, hi)
}

// opRange applies the pending operator to text[lo:hi).
func (e *lineEditor) opRange(lo, hi int) {
	lo, hi = clampInt(lo, 0, len(e.text)), clampInt(hi, 0, len(e.text))
	switch e.op {
	case 'y':
		lineRegister = append([]rune(nil), e.text[lo:hi]...)
		e.cur = lo
		e.finishCommand()
	case 'd':
		e.deleteRange(lo, hi, false)
		e.finishCommand()
	case 'c':
		e.deleteRange(lo, hi, true)
	}
}

// deleteRange removes text[lo:hi) into the register; with insert it then
// enters insert mode there (the c operator, s, S, C). It is a change even
// when the range is empty, as c on an empty line is.
func (e *lineEditor) deleteRange(lo, hi int, insert bool) {
	lo, hi = clampInt(lo, 0, len(e.text)), clampInt(hi, 0, len(e.text))
	if lo > hi {
		lo, hi = hi, lo
	}
	e.snapshot()
	if hi > lo {
		lineRegister = append([]rune(nil), e.text[lo:hi]...)
		e.text = append(e.text[:lo], e.text[hi:]...)
	}
	e.cur, e.cmdChanged = lo, true
	if insert {
		e.mode = editInsert
	}
}

// paste inserts the register n times after (p) or before (P) the cursor and
// leaves the cursor on the last pasted character.
func (e *lineEditor) paste(after bool, n int) {
	if len(lineRegister) == 0 {
		return
	}
	e.snapshot()
	at := e.cur
	if after && len(e.text) > 0 {
		at++
	}
	var ins []rune
	for i := 0; i < n; i++ {
		ins = append(ins, lineRegister...)
	}
	e.text = append(e.text[:at], append(ins, e.text[at:]...)...)
	e.cur, e.cmdChanged = at+len(ins)-1, true
}

// repeatLastChange replays the keys of the last change (.).
func (e *lineEditor) repeatLastChange() {
	keys := lineLastChange
	e.cancelPending()
	if len(keys) == 0 {
		return
	}
	e.replaying = true
	for _, k := range keys {
		e.key(k)
	}
	e.replaying = false
	e.cancelPending()
}

// pendingChar completes r, f, F, t, T, g and text objects with their argument.
func (e *lineEditor) pendingChar(r rune) {
	p := e.pending
	e.pending = 0
	switch p {
	case 'r':
		_, n := e.takeCount()
		if e.cur+n > len(e.text) {
			e.cancelPending()
			return
		}
		e.snapshot()
		for i := e.cur; i < e.cur+n; i++ {
			e.text[i] = r
		}
		e.cur, e.cmdChanged = e.cur+n-1, true
		e.finishCommand()
	case 'f', 'F', 't', 'T':
		e.lastFind, e.lastFindKind = r, p
		e.findMotion(p, r)
	case 'g':
		if r != '_' {
			e.cancelPending()
			return
		}
		target := lastNonBlank(e.text)
		if e.op != 0 {
			e.opMotion(target, true)
			return
		}
		e.cur = target
		e.finishCommand()
	case 'i', 'a':
		lo, hi, ok := textObject(e.text, e.cur, r, p == 'a')
		if !ok {
			e.cancelPending()
			return
		}
		if e.mode == editVisual {
			e.anchor, e.cur = lo, max(hi-1, lo)
			e.count = 0
			return
		}
		e.opRange(lo, hi)
	}
}

// findMotion moves to (or operates up to) the count-th occurrence of ch: f
// and t forward, F and T backward, t and T stopping one short.
func (e *lineEditor) findMotion(kind, ch rune) {
	_, n := e.takeCount()
	target, ok := findChar(e.text, e.cur, kind, ch, n, false)
	if !ok {
		e.cancelPending()
		return
	}
	if e.op != 0 {
		e.opMotion(target, kind == 'f' || kind == 't')
		return
	}
	e.cur = target
	if e.mode != editVisual {
		e.finishCommand()
	}
}

// findChar implements f, F, t and T on text from cur. With repeat (the ;
// and , commands) t and T skip the character next to the cursor, as vim does,
// so repeating them makes progress.
func findChar(text []rune, cur int, kind, ch rune, n int, repeat bool) (int, bool) {
	pos := cur
	for ; n > 0; n-- {
		switch kind {
		case 'f', 't':
			start := pos + 1
			if kind == 't' && (n > 1 || repeat) {
				start++
			}
			found := -1
			for i := start; i < len(text); i++ {
				if text[i] == ch {
					found = i
					break
				}
			}
			if found < 0 {
				return 0, false
			}
			pos = found
		default:
			start := pos - 1
			if kind == 'T' && (n > 1 || repeat) {
				start--
			}
			found := -1
			for i := start; i >= 0; i-- {
				if text[i] == ch {
					found = i
					break
				}
			}
			if found < 0 {
				return 0, false
			}
			pos = found
		}
	}
	switch kind {
	case 't':
		pos--
	case 'T':
		pos++
	}
	return pos, true
}

// motion returns where a motion key moves the cursor from e.cur with count
// n (raw is the typed count or 0), whether the motion is inclusive, and
// whether it can move at all. With forOp the motion is resolved for an
// operator: l may reach past the last character, w on the last word reaches
// the end, and cw acts like ce as in vim.
func (e *lineEditor) motion(r rune, raw, n int, forOp bool) (target int, inclusive, ok bool) {
	text, cur, end := e.text, e.cur, len(e.text)
	switch r {
	case 'h':
		return max(cur-n, 0), false, cur > 0
	case 'l', ' ':
		if forOp {
			return min(cur+n, end), false, cur < end
		}
		return min(cur+n, end-1), false, cur < end-1
	case '0':
		return 0, false, true
	case '^':
		return firstNonBlank(text), false, true
	case '$':
		return max(end-1, 0), true, true
	case '|':
		return clampInt(raw-1, 0, max(end-1, 0)), false, true
	case ';', ',':
		if e.lastFindKind == 0 {
			return 0, false, false
		}
		kind := e.lastFindKind
		if r == ',' {
			kind = reverseFind(kind)
		}
		t, found := findChar(text, cur, kind, e.lastFind, n, true)
		return t, kind == 'f' || kind == 't', found
	case 'w', 'W':
		big := r == 'W'
		if forOp && e.op == 'c' && cur < end && !unicode.IsSpace(text[cur]) {
			// cw changes to the end of the word, like ce.
			return wordEnd(text, cur, n, big), true, true
		}
		t := cur
		for i := 0; i < n; i++ {
			t = nextWordStart(text, t, big)
		}
		if t >= end {
			if forOp {
				return end, false, true
			}
			return max(end-1, 0), false, cur < end-1
		}
		return t, false, true
	case 'b', 'B':
		t := cur
		for i := 0; i < n; i++ {
			t = prevWordStart(text, t, r == 'B')
		}
		return t, false, cur > 0
	case 'e', 'E':
		t := wordEnd(text, cur, n, r == 'E')
		return t, true, t > cur || (forOp && cur < end)
	}
	return cur, false, false
}

// reverseFind swaps the direction of a find kind for the , command.
func reverseFind(kind rune) rune {
	switch kind {
	case 'f':
		return 'F'
	case 'F':
		return 'f'
	case 't':
		return 'T'
	}
	return 't'
}

// visualKey handles a key in charwise visual mode.
func (e *lineEditor) visualKey(k editKey) {
	if k.key == tcell.KeyEnter {
		e.done, e.applied = true, true
		return
	}
	if k.key == tcell.KeyEscape {
		e.mode = editNormal
		e.cancelPending()
		return
	}
	switch k.key {
	case tcell.KeyLeft:
		k = editKey{tcell.KeyRune, 'h'}
	case tcell.KeyRight:
		k = editKey{tcell.KeyRune, 'l'}
	case tcell.KeyHome:
		k = editKey{tcell.KeyRune, '0'}
	case tcell.KeyEnd:
		k = editKey{tcell.KeyRune, '$'}
	}
	if k.key != tcell.KeyRune {
		return
	}
	r := k.ch
	if e.pending != 0 {
		e.pendingChar(r)
		return
	}
	if r >= '1' && r <= '9' || (r == '0' && e.count > 0) {
		e.count = e.count*10 + int(r-'0')
		return
	}
	lo, hi := min(e.anchor, e.cur), max(e.anchor, e.cur)+1
	raw, n := e.takeCount()
	switch r {
	case 'v':
		e.mode = editNormal
	case 'o':
		e.anchor, e.cur = e.cur, e.anchor
	case 'i', 'a':
		e.pending = r
	case 'f', 'F', 't', 'T':
		e.pending = r
	case 'r':
		e.pending = 'r'
		e.mode = editNormal
		e.cur, e.count = lo, hi-lo
	case 'd', 'x', 'X', 'D':
		e.mode = editNormal
		e.deleteRange(lo, hi, false)
		e.finishCommand()
	case 'c', 's', 'C', 'S':
		e.deleteRange(lo, hi, true)
	case 'y', 'Y':
		lineRegister = append([]rune(nil), e.text[lo:hi]...)
		e.mode, e.cur = editNormal, lo
		e.finishCommand()
	case 'p', 'P':
		reg := lineRegister
		e.mode = editNormal
		e.deleteRange(lo, hi, false)
		lineRegister = reg
		e.paste(false, 1)
		e.finishCommand()
	case '~', 'u', 'U':
		e.snapshot()
		for i := lo; i < hi; i++ {
			switch r {
			case '~':
				e.text[i] = toggleCase(e.text[i])
			case 'u':
				e.text[i] = unicode.ToLower(e.text[i])
			default:
				e.text[i] = unicode.ToUpper(e.text[i])
			}
		}
		e.mode, e.cur, e.cmdChanged = editNormal, lo, true
		e.finishCommand()
	default:
		if target, _, ok := e.motion(r, raw, n, false); ok {
			e.cur = target
		}
	}
}

// insertKey handles a key in insert or replace mode.
func (e *lineEditor) insertKey(k editKey) {
	switch k.key {
	case tcell.KeyEnter:
		e.done, e.applied = true, true
	case tcell.KeyEscape:
		e.mode = editNormal
		e.cur--
		e.finishCommand()
	case tcell.KeyLeft:
		e.cur--
	case tcell.KeyRight:
		e.cur++
	case tcell.KeyHome:
		e.cur = 0
	case tcell.KeyEnd:
		e.cur = len(e.text)
	case tcell.KeyBackspace2:
		if e.cur == 0 {
			return
		}
		e.cur--
		if e.mode == editReplace && e.cur < len(e.replaceBase) {
			e.text[e.cur] = e.replaceBase[e.cur]
			return
		}
		e.text = append(e.text[:e.cur], e.text[e.cur+1:]...)
	case tcell.KeyDelete:
		if e.cur < len(e.text) {
			e.text = append(e.text[:e.cur], e.text[e.cur+1:]...)
		}
	case tcell.KeyCtrlW:
		start := prevWordStart(e.text, e.cur, false)
		e.text = append(e.text[:start], e.text[e.cur:]...)
		e.cur = start
	case tcell.KeyCtrlU:
		e.text = e.text[e.cur:]
		e.cur = 0
	case tcell.KeyRune:
		if e.mode == editReplace && e.cur < len(e.text) {
			e.text[e.cur] = k.ch
		} else {
			e.text = append(e.text[:e.cur], append([]rune{k.ch}, e.text[e.cur:]...)...)
		}
		e.cur++
	}
}

// Word motions use vim's classes: blank, keyword (letters, digits, _) and
// other punctuation; W, B and E treat every non-blank as one class.
func runeClass(r rune, big bool) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case big || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	}
	return 2
}

// nextWordStart is the index of the start of the word after pos (w, W); it
// returns len(text) when there is none.
func nextWordStart(text []rune, pos int, big bool) int {
	n := len(text)
	if pos >= n {
		return n
	}
	if c := runeClass(text[pos], big); c != 0 {
		for pos < n && runeClass(text[pos], big) == c {
			pos++
		}
	}
	for pos < n && runeClass(text[pos], big) == 0 {
		pos++
	}
	return pos
}

// prevWordStart is the index of the start of the word before pos (b, B).
func prevWordStart(text []rune, pos int, big bool) int {
	pos = min(pos, len(text))
	for pos > 0 && runeClass(text[pos-1], big) == 0 {
		pos--
	}
	if pos == 0 {
		return 0
	}
	c := runeClass(text[pos-1], big)
	for pos > 0 && runeClass(text[pos-1], big) == c {
		pos--
	}
	return pos
}

// wordEnd is the index of the end of the n-th word from pos (e, E): it moves
// at least one character, as vim does.
func wordEnd(text []rune, pos, n int, big bool) int {
	end := len(text)
	for ; n > 0 && pos < end-1; n-- {
		pos++
		for pos < end && runeClass(text[pos], big) == 0 {
			pos++
		}
		if pos >= end {
			return end - 1
		}
		c := runeClass(text[pos], big)
		for pos+1 < end && runeClass(text[pos+1], big) == c {
			pos++
		}
	}
	return clampInt(pos, 0, max(end-1, 0))
}

// firstNonBlank is the index of the first non-blank character (^, I).
func firstNonBlank(text []rune) int {
	for i, r := range text {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return max(len(text)-1, 0)
}

// lastNonBlank is the index of the last non-blank character (g_).
func lastNonBlank(text []rune) int {
	for i := len(text) - 1; i >= 0; i-- {
		if !unicode.IsSpace(text[i]) {
			return i
		}
	}
	return 0
}

// toggleCase swaps the case of a letter (~).
func toggleCase(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.ToLower(r)
	}
	return unicode.ToUpper(r)
}

// textObject returns text[lo:hi) for a text object at cur: w and W (a word,
// around includes the trailing or else leading blanks), a quote (" ' `) or a
// bracket pair (( ) b, [ ], { } B, < >), inner or around the delimiters.
func textObject(text []rune, cur int, obj rune, around bool) (lo, hi int, ok bool) {
	n := len(text)
	if n == 0 {
		return 0, 0, false
	}
	cur = clampInt(cur, 0, n-1)
	switch obj {
	case 'w', 'W':
		big := obj == 'W'
		c := runeClass(text[cur], big)
		lo, hi = cur, cur+1
		for lo > 0 && runeClass(text[lo-1], big) == c {
			lo--
		}
		for hi < n && runeClass(text[hi], big) == c {
			hi++
		}
		if around && c != 0 {
			trailing := hi
			for trailing < n && runeClass(text[trailing], big) == 0 {
				trailing++
			}
			if trailing > hi {
				hi = trailing
			} else {
				for lo > 0 && runeClass(text[lo-1], big) == 0 {
					lo--
				}
			}
		}
		return lo, hi, true
	case '"', '\'', '`':
		var quotes []int
		for i, r := range text {
			if r == obj {
				quotes = append(quotes, i)
			}
		}
		for i := 0; i+1 < len(quotes); i += 2 {
			q1, q2 := quotes[i], quotes[i+1]
			if cur <= q2 { // the pair around the cursor, or the first one after it
				if around {
					return q1, q2 + 1, true
				}
				return q1 + 1, q2, true
			}
		}
		return 0, 0, false
	}
	open, close, ok := bracketPair(obj)
	if !ok {
		return 0, 0, false
	}
	start := -1
	if text[cur] == open {
		start = cur
	} else {
		depth := 0
		for i := cur; i >= 0; i-- {
			switch text[i] {
			case close:
				if i != cur {
					depth++
				}
			case open:
				if depth == 0 {
					start = i
				} else {
					depth--
				}
			}
			if start >= 0 {
				break
			}
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	depth := 0
	for i := start + 1; i < n; i++ {
		switch text[i] {
		case open:
			depth++
		case close:
			if depth == 0 {
				if around {
					return start, i + 1, true
				}
				return start + 1, i, true
			}
			depth--
		}
	}
	return 0, 0, false
}

// bracketPair maps a bracket text object key to its delimiters.
func bracketPair(obj rune) (open, close rune, ok bool) {
	switch obj {
	case '(', ')', 'b':
		return '(', ')', true
	case '[', ']':
		return '[', ']', true
	case '{', '}', 'B':
		return '{', '}', true
	case '<', '>':
		return '<', '>', true
	}
	return 0, 0, false
}
