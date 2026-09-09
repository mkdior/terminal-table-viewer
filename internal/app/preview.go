package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

const (
	// maxPreviewRunes is the longest value the preview box will show; longer
	// values cannot be read whole in a popup anyway.
	maxPreviewRunes = 1000
	// previewMaxWidth caps the text width of the preview box.
	previewMaxWidth = 100
)

// previewPosition says where the full-value box is drawn.
type previewPosition int

const (
	previewBottom   previewPosition = iota // at the bottom of the table, centred (default)
	previewTop                             // under the header, centred
	previewAtCursor                        // over the selected cell, so the value pops out in place
)

// previewPos is the active placement; the [preview] config section sets it.
var previewPos = previewBottom

// defaultPreviewSeparator is what separates the items of a list value.
const defaultPreviewSeparator = ";"

// List values in the preview box: a cell such as "red; green; blue" is shown
// one item per line rather than as one wrapped run of text. The [preview]
// config section sets both.
var (
	previewSplitItems = true
	previewSeparator  = defaultPreviewSeparator
)

// previewLines wraps text to innerW cells for the box. A value that lists
// several items is laid out one item per line, each item wrapped on its own,
// when that fits in maxLines; otherwise, and for any other value, the text is
// word-wrapped as it is.
func previewLines(text string, innerW, maxLines int) []string {
	if previewSplitItems && previewSeparator != "" && strings.Contains(text, previewSeparator) {
		var lines []string
		for _, item := range strings.Split(text, previewSeparator) {
			if item = strings.TrimSpace(item); item != "" {
				lines = append(lines, tview.WordWrap(item, innerW)...)
			}
		}
		if len(lines) > 0 && len(lines) <= maxLines {
			return lines
		}
	}
	return tview.WordWrap(text, innerW)
}

// parsePreviewPosition reads the config spelling of a placement.
func parsePreviewPosition(s string) (previewPosition, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "bottom":
		return previewBottom, nil
	case "top":
		return previewTop, nil
	case "cursor":
		return previewAtCursor, nil
	}
	return 0, fmt.Errorf("unknown preview position %q; use cursor, top or bottom", s)
}

// cellPreview is the main page (the table with its footer) plus a floating
// box that shows the full value of the selected cell whenever that cell was
// cut with an ellipsis by a column width limit.
type cellPreview struct {
	*tview.Frame
	box      *tview.TextView
	text     string // full value to show; "" hides the box
	row, col int    // the selected cell the box belongs to
}

// newCellPreview wraps the main page frame.
func newCellPreview(frame *tview.Frame) *cellPreview {
	box := tview.NewTextView().SetWrap(false).SetTextColor(theme.Text)
	box.SetBackgroundColor(theme.Panel)
	box.SetBorder(true).SetBorderColor(theme.Accent).SetBorderPadding(0, 0, 1, 1)
	box.SetTitleAlign(tview.AlignLeft).SetTitleColor(theme.Accent)
	return &cellPreview{Frame: frame, box: box}
}

// show displays text in the box; an empty text hides it.
func (p *cellPreview) show(title, text string, row, col int) {
	p.text, p.row, p.col = text, row, col
	p.box.SetTitle(" " + title + " ")
}

// hide removes the box.
func (p *cellPreview) hide() { p.text = "" }

// Draw renders the page and then the box at the configured position.
func (p *cellPreview) Draw(screen tcell.Screen) {
	if currentContent != nil {
		currentContent.beginFrame()
	}
	// The table is as wide as the frame (no side borders), and the frame's
	// rect is known before the table's own is set by the draw.
	_, _, frameW, _ := p.GetInnerRect()
	pinColumnOffset(frameW)
	p.Frame.Draw(screen)
	if cellEdit != nil {
		cellEdit.draw(screen)
		return
	}
	if p.text == "" || !bufferTable.HasFocus() {
		return
	}
	tx, ty, tw, th := bufferTable.GetInnerRect()
	lines, w, h, ok := previewLayout(p.text, tw, th)
	if !ok {
		return
	}
	// The title (the column name) must fit too, or a short value would cut it.
	if titleW := uniseg.StringWidth(p.box.GetTitle()) + 2; titleW > w {
		w = min(titleW, tw)
	}
	p.box.SetText(strings.Join(lines, "\n"))
	x, y := p.origin(w, h, tx, ty, tw, th)
	p.box.SetRect(x, y, w, h)
	p.box.Draw(screen)
}

// origin returns the top-left corner of a w x h box inside the table area.
// At the cursor the box is laid over the selected cell so that its first text
// line starts where the cell's text starts, or its last line when there is no
// room below; if the cell is off screen the box falls back to the half of the
// table away from the cursor.
func (p *cellPreview) origin(w, h, tx, ty, tw, th int) (int, int) {
	centred := tx + (tw-w)/2
	switch previewPos {
	case previewTop:
		return centred, ty + b.rowFreeze
	case previewBottom:
		return centred, ty + th - h
	}

	cx, cy, cw := 0, 0, 0
	if currentContent != nil {
		if cell := currentContent.drawnCell(p.row, p.col); cell != nil {
			cx, cy, cw = cell.GetLastPosition()
		}
	}
	if cw == 0 {
		rowOffset, _ := bufferTable.GetOffset()
		if ty+p.row-rowOffset >= ty+th/2 {
			return centred, ty + b.rowFreeze
		}
		return centred, ty + th - h
	}

	x := clampInt(cx-2, tx, tx+tw-w) // border and padding sit left of the value
	y := cy - 1                      // first text line on the cell's row
	if y+h > ty+th {
		y = cy - h + 2 // last text line on the cell's row
	}
	return x, clampInt(y, ty, ty+th-h)
}

// previewLayout wraps text for a box inside a table area of availW x availH
// cells and returns the wrapped lines and the box size including its border
// and padding. ok is false when the value cannot be shown whole in half the
// area, in which case no box is drawn.
func previewLayout(text string, availW, availH int) (lines []string, width, height int, ok bool) {
	if utf8.RuneCountInString(text) > maxPreviewRunes {
		return nil, 0, 0, false
	}
	innerW := availW - 4
	if innerW > previewMaxWidth {
		innerW = previewMaxWidth
	}
	maxLines := availH/2 - 2
	if innerW < 10 || maxLines < 1 {
		return nil, 0, 0, false
	}
	lines = previewLines(text, innerW, maxLines)
	if len(lines) > maxLines {
		return nil, 0, 0, false
	}
	longest := 0
	for _, line := range lines {
		if lw := uniseg.StringWidth(line); lw > longest {
			longest = lw
		}
	}
	return lines, longest + 4, len(lines) + 2, true
}

// truncatedCellText returns the full value of the cell when the column has a
// width limit that cuts this value, and false otherwise.
func truncatedCellText(row, col int) (string, bool) {
	width, limited := wrappedColumns[col]
	if !limited {
		return "", false
	}
	text, ok := b.cellAt(row, col)
	if !ok || uniseg.StringWidth(text) <= width {
		return "", false
	}
	return text, true
}

// columnTitle names a column by its header cell, or by its index without one.
func columnTitle(col int) string {
	if b.rowFreeze > 0 {
		if name, ok := b.cellAt(0, col); ok {
			return name
		}
	}
	return "Column " + I2S(col)
}

// updateCellPreview shows or hides the preview for the selected cell: the full
// value of a cell cut by a width limit, or, on a hidden column, the column's
// name and value, which the fold marker does not show.
func updateCellPreview(row, col int) {
	if mainView == nil {
		return
	}
	if hiddenCols[col] {
		if text, ok := hiddenCellText(row, col); ok {
			mainView.show(columnTitle(col)+" (hidden)", text, row, col)
			return
		}
	}
	if text, ok := truncatedCellText(row, col); ok {
		mainView.show(columnTitle(col), text, row, col)
	} else {
		mainView.hide()
	}
}

// hiddenCellText returns the value of a data cell in a hidden column for the
// preview, "(empty)" for a blank one so the box still names the column.
func hiddenCellText(row, col int) (string, bool) {
	if row < b.rowFreeze {
		return "", false
	}
	text, ok := b.cellAt(row, col)
	if !ok {
		return "", false
	}
	if text != "" {
		return text, true
	}
	return "(empty)", true
}
