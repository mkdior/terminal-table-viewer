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

// runeWidth is the number of terminal cells a rune occupies.
func runeWidth(r rune) int {
	return uniseg.StringWidth(string(r))
}

// draw paints the editor over its cell: the text on the panel background,
// scrolled so the cursor is visible, the cursor as a reverse block in normal
// and visual mode and as the terminal cursor while inserting, and the visual
// selection on the selection background.
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
	widths := make([]int, len(ed.text))
	total := 0
	for i, r := range ed.text {
		widths[i] = runeWidth(r)
		total += widths[i]
	}
	w = clampInt(max(w, total+1), 1, avail)
	curX := 0
	for i := 0; i < ed.cur && i < len(widths); i++ {
		curX += widths[i]
	}
	off := 0
	if curX >= w {
		off = curX - w + 1
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
	col := 0
	for i, r := range ed.text {
		rw := widths[i]
		if rw > 0 && col-off >= 0 && col-off+rw <= w {
			st := base
			if i >= lo && i <= hi {
				st = st.Background(theme.Selection)
			}
			if i == ed.cur && !typing {
				st = st.Reverse(true)
			}
			screen.SetContent(x+col-off, y, r, nil, st)
		}
		col += rw
	}
	cx := x + curX - off
	if ed.cur >= len(ed.text) && !typing && cx < x+w {
		screen.SetContent(cx, y, ' ', nil, base.Reverse(true))
	}
	if typing {
		screen.ShowCursor(cx, y)
	}
}
