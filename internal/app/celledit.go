package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// cellEditor is the line editor open on a cell. It is drawn over the cell,
// widening to the right edge of the table as the text needs, and every key
// goes to it until Enter applies the text or Esc cancels.
type cellEditor struct {
	ed       *lineEditor
	row, col int
	bulk     *bulkEdit // set when the text goes to a visual selection
}

// bulkEdit is vim's block insert for a selection of cells: the editor opens
// on the first cell, and what is typed is inserted (i), appended (a) or
// substituted (cc) in every selected cell when Esc or Enter is pressed.
type bulkEdit struct {
	r1, c1, r2, c2 int
	how            action
	original       string // the first cell's value when the editor opened
}

// startBulkEdit opens the editor on the first cell of the selection for a
// bulk edit; the cursor moves there so the box is drawn where the text lands.
func startBulkEdit(how action, r1, c1, r2, c2 int) {
	if !editsAllowed() {
		return
	}
	r1, r2 = orderRange(r1, r2, firstDataRow(b), b.rowLen-1)
	c1, c2 = orderRange(c1, c2, 0, b.colLen-1)
	if r1 >= b.rowLen || c1 >= len(b.cont[r1]) {
		return
	}
	bufferTable.Select(r1, c1)
	text := b.cont[r1][c1]
	ed := newLineEditor(text)
	switch how {
	case actAppend:
		ed.key(editKey{tcell.KeyRune, 'A'})
	case actChange:
		ed.key(editKey{tcell.KeyRune, 'S'})
	default:
		ed.key(editKey{tcell.KeyRune, 'i'})
	}
	cellEdit = &cellEditor{ed: ed, row: r1, col: c1, bulk: &bulkEdit{r1, c1, r2, c2, how, text}}
	showEditStatus()
}

// startHeaderEdit opens the header cell of column col, appending, so a new
// column can be named; the table cursor stays on its data row.
func startHeaderEdit(col int) {
	if !editsAllowed() || b.rowFreeze == 0 || b.rowLen == 0 || col < 0 || col >= len(b.cont[0]) {
		return
	}
	ed := newLineEditor(b.cont[0][col])
	ed.key(editKey{tcell.KeyRune, 'A'})
	cellEdit = &cellEditor{ed: ed, row: 0, col: col}
	showEditStatus()
}

// applyBulk stores the edited text in every selected cell. The typed part is
// what was added before (i) or after (a) the first cell's original value; if
// the original itself was edited, or for cc, the whole text goes everywhere.
func (c *cellEditor) applyBulk() {
	text, orig := string(c.ed.text), c.bulk.original
	value := func(string) string { return text }
	switch {
	case c.bulk.how == actChange:
	case c.bulk.how == actAppend && strings.HasPrefix(text, orig):
		suffix := text[len(orig):]
		value = func(s string) string { return s + suffix }
	case c.bulk.how != actAppend && strings.HasSuffix(text, orig):
		prefix := text[:len(text)-len(orig)]
		value = func(s string) string { return prefix + s }
	}
	if setCells(rectTargets(c.bulk.r1, c.bulk.c1, c.bulk.r2, c.bulk.c2, value), "Changed") == 0 {
		drawFooterText(fileNameStr, "No change", cursorPosStr)
	}
}

// cellEdit is the editor currently open, nil when none.
var cellEdit *cellEditor

// startCellEdit opens the selected cell in the line editor: E in normal mode,
// i inserting at the start, a appending at the end, cc with the cell cleared.
// The vim key for the way in is fed to the editor so . can repeat it.
func startCellEdit(how action) {
	if !editsAllowed() {
		return
	}
	row, col := bufferTable.GetSelection()
	if row < firstDataRow(b) || row >= b.rowLen || col < 0 || col >= len(b.cont[row]) {
		return
	}
	ed := newLineEditor(b.cont[row][col])
	switch how {
	case actInsert:
		ed.key(editKey{tcell.KeyRune, 'i'})
	case actAppend:
		ed.key(editKey{tcell.KeyRune, 'A'})
	case actChange:
		ed.key(editKey{tcell.KeyRune, 'S'})
	}
	cellEdit = &cellEditor{ed: ed, row: row, col: col}
	showEditStatus()
}

// handleKey routes a key to the open editor and closes it when it is done. In
// the editor's normal sub-mode a vertical table motion (j, k, paging, G)
// applies the value and moves on, so "i text Esc j" edits a cell and steps to
// the next one the way a spreadsheet does; Esc there still cancels.
func (c *cellEditor) handleKey(ev *tcell.EventKey) {
	if c.bulk != nil {
		// Block insert: leaving insert mode (Esc) or Enter applies to all.
		c.ed.key(editKeyOf(ev))
		if c.ed.done || (c.ed.mode != editInsert && c.ed.mode != editReplace) {
			closeCellEdit()
			c.applyBulk()
			return
		}
		showEditStatus()
		return
	}
	if c.ed.mode == editNormal && leavesEditor(ev) {
		closeCellEdit()
		changeCell(c.row, c.col, string(c.ed.text))
		handleTableKey(ev)
		return
	}
	c.ed.key(editKeyOf(ev))
	if !c.ed.done {
		showEditStatus()
		return
	}
	closeCellEdit()
	if c.ed.applied {
		changeCell(c.row, c.col, string(c.ed.text))
		return
	}
	drawFooterText(fileNameStr, "Edit cancelled", cursorPosStr)
}

// leavesEditor reports whether a key is a vertical table motion under the
// active keymap: those end the edit instead of being fed to the line editor,
// which has no use for them.
func leavesEditor(ev *tcell.EventKey) bool {
	act, _ := keys.resolve([]keyStroke{strokeFromEvent(ev)})
	switch act {
	case actMoveDown, actMoveUp, actPageDown, actPageUp, actHalfPageDown, actHalfPageUp, actLastRow:
		return true
	}
	return false
}

// closeCellEdit removes the editor and hides the terminal cursor it showed
// while inserting: tview only hides the cursor on a focus change, so without
// this it would stay on screen, fixed at that spot while the table scrolls.
func closeCellEdit() {
	cellEdit = nil
	if screenRef != nil {
		screenRef.HideCursor()
	}
}

// showEditStatus puts the editor's sub-mode in the footer, as vim's showmode.
func showEditStatus() {
	if cellEdit == nil {
		return
	}
	mode := "-- EDIT --  Enter applies, Esc cancels"
	switch cellEdit.ed.mode {
	case editInsert:
		mode = "-- INSERT --  Esc for normal mode, Enter applies"
	case editReplace:
		mode = "-- REPLACE --  Esc for normal mode, Enter applies"
	case editVisual:
		mode = "-- VISUAL --  d, c, y on the selection, Esc for normal mode"
	}
	if bk := cellEdit.bulk; bk != nil {
		cells := (bk.r2 - bk.r1 + 1) * (bk.c2 - bk.c1 + 1)
		mode = fmt.Sprintf("-- INSERT --  Esc or Enter applies to %s", plural(cells, "cell"))
	}
	title := columnTitle(cellEdit.col)
	if cellEdit.row == 0 && b.rowFreeze > 0 {
		title = "header of column " + I2S(cellEdit.col)
	}
	drawFooterText(fileNameStr, title+"  "+mode, cursorPosStr)
}

// grapheme is one user-perceived character of the text being edited: the
// runes it spans (a base plus its combining marks, or an emoji sequence) and
// the terminal cells it occupies.
type grapheme struct {
	start, end int // rune indexes [start, end)
	runes      []rune
	width      int
}

// graphemes splits text into user-perceived characters so combining marks
// are drawn with their base and emoji sequences are measured as one unit.
func graphemes(text []rune) []grapheme {
	var out []grapheme
	gr := uniseg.NewGraphemes(string(text))
	pos := 0
	for gr.Next() {
		rs := gr.Runes()
		out = append(out, grapheme{pos, pos + len(rs), rs, gr.Width()})
		pos += len(rs)
	}
	return out
}

// draw paints the editor over its cell: the text on the panel background,
// scrolled so the cursor is visible, the cursor as a reverse block in normal
// and visual mode and as the terminal cursor while inserting, and the visual
// selection on the selection background. The cursor moves by rune; a cursor
// inside a multi-rune grapheme highlights the whole grapheme.
func (c *cellEditor) draw(screen tcell.Screen) {
	tx, ty, tw, _ := bufferTable.GetInnerRect()
	x, y, w := tx, ty+b.rowFreeze, 10
	if currentContent != nil {
		if cell := currentContent.drawnCell(c.row, c.col); cell != nil {
			if cx, cy, cw := cell.GetLastPosition(); cw > 0 {
				x, y, w = cx, cy, cw
			}
		}
	}
	avail := tx + tw - x
	if avail < 1 {
		return
	}
	ed := c.ed
	gs := graphemes(ed.text)
	total, curX, curW := 0, -1, 1
	for _, g := range gs {
		if curX < 0 && ed.cur >= g.start && ed.cur < g.end {
			curX, curW = total, max(g.width, 1)
		}
		total += g.width
	}
	if curX < 0 {
		curX = total // after the last character
	}
	w = clampInt(max(w, total+1), 1, avail)
	off := 0
	if curX+curW > w {
		off = curX + curW - w
	}
	base := tcell.StyleDefault.Background(theme.Panel).Foreground(theme.Text)
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, ' ', nil, base)
	}
	lo, hi := -1, -1
	if ed.mode == editVisual {
		lo, hi = min(ed.anchor, ed.cur), max(ed.anchor, ed.cur)
	}
	typing := ed.mode == editInsert || ed.mode == editReplace
	pos := 0
	for _, g := range gs {
		if g.width > 0 && pos-off >= 0 && pos-off+g.width <= w {
			st := base
			if g.start <= hi && g.end-1 >= lo {
				st = st.Background(theme.Selection)
			}
			if !typing && ed.cur >= g.start && ed.cur < g.end {
				st = st.Reverse(true)
			}
			screen.SetContent(x+pos-off, y, g.runes[0], g.runes[1:], st)
		}
		pos += g.width
	}
	cx := x + curX - off
	if ed.cur >= len(ed.text) && !typing && cx < x+w {
		screen.SetContent(cx, y, ' ', nil, base.Reverse(true))
	}
	if typing {
		screen.ShowCursor(cx, y)
	} else {
		screen.HideCursor()
	}
}
