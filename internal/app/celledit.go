package app

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// cellEditor is the line editor open on a cell. It is drawn over the cell,
// widening to the right edge of the table as the text needs, and every key
// goes to it until Enter applies the text or Esc cancels.
type cellEditor struct {
	ed       *lineEditor
	row, col int
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

// handleKey routes a key to the open editor and closes it when it is done.
func (c *cellEditor) handleKey(ev *tcell.EventKey) {
	c.ed.key(editKeyOf(ev))
	if !c.ed.done {
		showEditStatus()
		return
	}
	cellEdit = nil
	if c.ed.applied {
		changeCell(c.row, c.col, string(c.ed.text))
		return
	}
	drawFooterText(fileNameStr, "Edit cancelled", cursorPosStr)
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
	drawFooterText(fileNameStr, columnTitle(cellEdit.col)+"  "+mode, cursorPosStr)
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
	}
}
