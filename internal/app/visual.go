package app

import "fmt"

// visualKind is the active visual mode: a rectangle of cells or whole rows.
type visualKind int

const (
	visualOff visualKind = iota
	visualBlock
	visualRows
)

// Visual mode state: the anchor is the cell where the mode was entered; the
// selection spans from it to the cursor.
var (
	visual          visualKind
	visualAnchorRow int
	visualAnchorCol int
)

// startVisual enters kind, switches to it when another kind is active, or
// leaves visual mode when kind is already active (as v does in vim).
func startVisual(kind visualKind) {
	row, col := bufferTable.GetSelection()
	switch visual {
	case visualOff:
		visual = kind
		visualAnchorRow, visualAnchorCol = row, col
	case kind:
		exitVisual("All Done")
		return
	default:
		visual = kind
	}
	drawFooterText(fileNameStr, visualStatus(), cursorPosStr)
}

// exitVisual leaves visual mode and shows status in the footer.
func exitVisual(status string) {
	visual = visualOff
	drawFooterText(fileNameStr, status, cursorPosStr)
}

// swapVisualAnchor moves the cursor to the anchor and the anchor to the
// cursor, so the other end of the selection can be adjusted.
func swapVisualAnchor() {
	if visual == visualOff {
		return
	}
	row, col := bufferTable.GetSelection()
	visualAnchorRow, visualAnchorCol, row, col = row, col, visualAnchorRow, visualAnchorCol
	bufferTable.Select(row, col)
}

// visualRect returns the selected rectangle, rows r1..r2 and columns c1..c2
// inclusive, for the current cursor position.
func visualRect() (r1, c1, r2, c2 int) {
	row, col := bufferTable.GetSelection()
	r1, r2 = visualAnchorRow, row
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	if visual == visualRows {
		return r1, 0, r2, b.colCount() - 1
	}
	c1, c2 = visualAnchorCol, col
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	return r1, c1, r2, c2
}

// inVisual reports whether the cell is inside the active selection.
func inVisual(row, col int) bool {
	if visual == visualOff {
		return false
	}
	r1, c1, r2, c2 := visualRect()
	return row >= r1 && row <= r2 && col >= c1 && col <= c2
}

// visualStatus describes the selection for the footer.
func visualStatus() string {
	r1, c1, r2, c2 := visualRect()
	if visual == visualRows {
		return fmt.Sprintf("-- VISUAL LINE --  %d rows", r2-r1+1)
	}
	return fmt.Sprintf("-- VISUAL --  %d rows x %d columns", r2-r1+1, c2-c1+1)
}

// yankVisual copies the selection and leaves visual mode.
func yankVisual(wholeRows bool) {
	r1, c1, r2, c2 := visualRect()
	if wholeRows {
		c1, c2 = 0, b.colCount()-1
	}
	visual = visualOff
	yankCells(r1, c1, r2, c2)
}
