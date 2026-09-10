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

// previewMode says which cells get the full-value box.
type previewMode int

const (
	previewCut previewMode = iota // values cut by a width limit (the default)
	previewAll                    // every cell, so any value can be read and selected as text
	previewOff                    // never
)

// previewShow is the active mode, from show in [preview]; zK switches it off
// and back, and previewBefore remembers what to come back to.
var (
	previewShow   = previewCut
	previewBefore = previewCut
)

// previewModeNames are the config spellings of the modes.
var previewModeNames = []string{"cut", "all", "off"}

// String is the config spelling of the mode.
func (m previewMode) String() string { return previewModeNames[m] }

// describe says what the mode shows, for the footer.
func (m previewMode) describe() string {
	switch m {
	case previewAll:
		return "shown for every cell"
	case previewOff:
		return "hidden"
	}
	return "shown for cut values"
}

// marker is the footer's reminder of a mode other than the default, "" for
// the default.
func (m previewMode) marker() string {
	switch m {
	case previewAll:
		return "box: every cell"
	case previewOff:
		return "box: hidden"
	}
	return ""
}

// parsePreviewMode reads the config spelling of a mode.
func parsePreviewMode(s string) (previewMode, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return previewCut, nil
	}
	for i, name := range previewModeNames {
		if s == name {
			return previewMode(i), nil
		}
	}
	return 0, fmt.Errorf("unknown preview mode %q; use cut, all or off", s)
}

// peekCell is K: it shows the full value of the current cell in the box
// whatever its length and whatever the mode, so any value can be read whole
// and selected as text, until the cursor moves on or Esc closes it. A one-off
// look, in the spirit of vim's K, rather than a mode.
func peekCell() {
	if mainView == nil {
		return
	}
	row, col := bufferTable.GetSelection()
	text, ok := b.cellAt(row, col)
	if !ok || row < b.rowFreeze || text == "" {
		drawFooterText(fileNameStr, "Nothing to show: the cell is empty", cursorPosStr)
		return
	}
	mainView.peek, mainView.peekRow, mainView.peekCol = true, row, col
	updateCellPreview(row, col)
	drawFooterText(fileNameStr, "Showing the full value of "+columnTitle(col)+"; a move or Esc closes it", cursorPosStr)
}

// closePeek ends a K peek and reports whether one was open.
func closePeek() bool {
	if mainView == nil || !mainView.peek {
		return false
	}
	mainView.peek = false
	updateCellPreview(bufferTable.GetSelection())
	return true
}

// togglePreviewBox is zK: it hides the automatic full-value box, or shows it
// again as the show setting says, and says so in the footer.
func togglePreviewBox() {
	if previewShow != previewOff {
		previewBefore, previewShow = previewShow, previewOff
	} else {
		previewShow = previewBefore
		if previewShow == previewOff { // off was the setting: cut is the way back
			previewShow = previewCut
		}
	}
	row, col := bufferTable.GetSelection()
	cursorPosStr = buildCursorPosStr(row, col)
	updateCellPreview(row, col)
	drawFooterText(fileNameStr, "Full-value box: "+previewShow.describe()+" ("+keyHintOr(actTogglePreview, "zK")+" toggles, "+keyHintOr(actPeek, "K")+" shows a cell once)", cursorPosStr)
}

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

	// The box as drawn last, for the mouse: its rectangle (bw is 0 when no
	// box was drawn), the lines of text shown and where each begins in text.
	bx, by, bw, bh int
	lines          []string
	starts         []int

	// Text selected in the box with the mouse, after Claude Code's selection:
	// a drag over the box selects the value's own text rather than the cells
	// behind it, and the stretch is copied when the button is released.
	sel boxSelection

	// A K peek: the box shows the cell at peekRow, peekCol whatever its
	// length until the cursor leaves it.
	peek             bool
	peekRow, peekCol int
}

// boxSelection is a stretch of the preview's text: the anchor where the
// button went down and the focus under the pointer, as byte offsets.
type boxSelection struct {
	on, dragging  bool
	anchor, focus int
}

// bounds returns the selected stretch in order.
func (s boxSelection) bounds() (from, to int) {
	return min(s.anchor, s.focus), max(s.anchor, s.focus)
}

// newCellPreview wraps the main page frame.
func newCellPreview(frame *tview.Frame) *cellPreview {
	box := tview.NewTextView().SetWrap(false).SetTextColor(theme.Text)
	box.SetBackgroundColor(theme.Panel)
	box.SetBorder(true).SetBorderColor(theme.Accent).SetBorderPadding(0, 0, 1, 1)
	box.SetTitleAlign(tview.AlignLeft).SetTitleColor(theme.Accent)
	return &cellPreview{Frame: frame, box: box}
}

// show displays text in the box; an empty text hides it. A selection made in
// the box belongs to the value it showed and goes with it.
func (p *cellPreview) show(title, text string, row, col int) {
	if text != p.text || row != p.row || col != p.col {
		p.sel = boxSelection{}
	}
	p.text, p.row, p.col = text, row, col
	p.box.SetTitle(" " + title + " ")
}

// hide removes the box.
func (p *cellPreview) hide() {
	p.text = ""
	p.sel = boxSelection{}
}

// clearSelection drops the text selected in the box and reports whether
// there was one.
func (p *cellPreview) clearSelection() bool {
	on := p.sel.on
	p.sel = boxSelection{}
	return on
}

// lineStarts finds where each displayed line begins in text: the lines are
// stretches of the text in order (word wrapping and the item split only drop
// the spaces and separators between them), so a selection over several lines
// maps back onto the value itself, separators included.
func lineStarts(text string, lines []string) []int {
	starts := make([]int, len(lines))
	pos := 0
	for i, line := range lines {
		if at := strings.Index(text[pos:], line); at >= 0 {
			pos += at
		}
		starts[i] = pos
		pos += len(line)
	}
	return starts
}

// offsetAt maps a screen position inside the box to a byte offset in the
// text: the start of the grapheme under the pointer, the line's start left
// of its text, its end right of it. ok is false outside the box.
func (p *cellPreview) offsetAt(x, y int) (offset int, ok bool) {
	if p.bw == 0 || x < p.bx || x >= p.bx+p.bw || y < p.by || y >= p.by+p.bh {
		return 0, false
	}
	line := clampInt(y-(p.by+1), 0, len(p.lines)-1) // the border row above
	if len(p.lines) == 0 {
		return 0, true
	}
	text, start := p.lines[line], p.starts[line]
	col := p.bx + 2 // border and padding
	gr := uniseg.NewGraphemes(text)
	for gr.Next() {
		if w := gr.Width(); x < col+w {
			from, _ := gr.Positions()
			return start + from, true
		} else {
			col += w
		}
	}
	return start + len(text), true
}

// boxMouse handles a mouse action over the preview box and reports whether
// it took it. A press anchors a text selection, a move with the button held
// extends it, the release copies it (with copy_on_select) and leaves it
// highlighted, a click clears it, a double click selects the whole value.
// A drag that started on a cell keeps selecting cells, box or no box, and a
// press elsewhere clears the box's selection.
func (p *cellPreview) boxMouse(action tview.MouseAction, event *tcell.EventMouse) bool {
	x, y := event.Position()
	offset, inBox := p.offsetAt(x, y)
	if mouseDrag.pressed || p.text == "" {
		return false
	}
	if !inBox {
		if action == tview.MouseLeftDown {
			p.sel = boxSelection{}
		}
		return false
	}
	switch action {
	case tview.MouseLeftDown:
		p.sel = boxSelection{on: true, dragging: true, anchor: offset, focus: offset}
	case tview.MouseMove:
		if p.sel.dragging && event.Buttons()&tcell.ButtonPrimary != 0 {
			p.sel.focus = offset
		}
	case tview.MouseLeftUp:
		if p.sel.dragging {
			p.sel.dragging = false
			if from, to := p.sel.bounds(); from == to {
				p.sel.on = false // no drag: the click that follows keeps it cleared
			} else if clipboardCopyOnSelect {
				p.copySelection()
			}
		}
	case tview.MouseLeftClick:
		p.sel = boxSelection{}
	case tview.MouseLeftDoubleClick:
		p.sel = boxSelection{on: true, anchor: 0, focus: len(p.text)}
		if clipboardCopyOnSelect {
			p.copySelection()
		}
	case tview.MouseScrollUp, tview.MouseScrollDown, tview.MouseScrollLeft, tview.MouseScrollRight:
		return false // the wheel still moves the table under the box
	}
	return true
}

// copySelection copies the text selected in the box to the register and the
// clipboard and reports it in the footer.
func (p *cellPreview) copySelection() {
	from, to := p.sel.bounds()
	text := p.text[from:to]
	if strings.TrimSpace(text) == "" {
		drawFooterText(fileNameStr, "Nothing to copy", cursorPosStr)
		return
	}
	setRegister([][]string{{text}})
	what := fmt.Sprintf("Copied %d characters of %s", utf8.RuneCountInString(text), columnTitle(p.col))
	if from == 0 && to == len(p.text) {
		what = fmt.Sprintf("Copied the whole value of %s (%d characters)", columnTitle(p.col), utf8.RuneCountInString(text))
	}
	announceCopy(what, text)
}

// drawSelection paints the selection background over the selected stretch
// of the box's text, as drawn.
func (p *cellPreview) drawSelection(screen tcell.Screen) {
	if !p.sel.on {
		return
	}
	from, to := p.sel.bounds()
	for i, line := range p.lines {
		x, y := p.bx+2, p.by+1+i
		gr := uniseg.NewGraphemes(line)
		for gr.Next() {
			w := gr.Width()
			if start, _ := gr.Positions(); start+p.starts[i] >= from && start+p.starts[i] < to {
				for dx := 0; dx < w; dx++ {
					mainc, combc, style, _ := screen.GetContent(x+dx, y)
					screen.SetContent(x+dx, y, mainc, combc, style.Background(theme.Selection))
				}
			}
			x += w
		}
	}
}

// MouseHandler routes a mouse action on the frame's own texts, the tab line
// and the footer, before the table sees it; everything else goes to the frame
// (and so to the table) as before. Nothing acts while a cell is being edited.
func (p *cellPreview) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
	return func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		if cellEdit == nil && p.frameMouse(action, event) {
			return true, nil
		}
		return p.Frame.MouseHandler()(action, event, setFocus)
	}
}

// frameMouse handles a mouse action on the tab line (the frame's first header
// row when several tabs are open) or the footer (its last row) and reports
// whether it did.
func (p *cellPreview) frameMouse(action tview.MouseAction, event *tcell.EventMouse) bool {
	x, y := event.Position()
	fx, fy, fw, fh := p.GetInnerRect()
	if x < fx || x >= fx+fw || y < fy || y >= fy+fh {
		return false
	}
	switch {
	case len(tabs) > 1 && y == fy:
		return tabLineMouse(action, x-fx)
	case y == fy+fh-1:
		return footerMouse(action, x-fx)
	}
	return p.boxMouse(action, event)
}

// footerMouse handles a click on the footer: "? help" at the end of the left
// text opens the help.
func footerMouse(action tview.MouseAction, x int) bool {
	if action != tview.MouseLeftClick {
		return false
	}
	hint := "? help"
	end := uniseg.StringWidth(fileNameStr)
	if strings.HasSuffix(fileNameStr, hint) && x >= end-uniseg.StringWidth(hint) && x < end {
		showHelpDialog()
		return true
	}
	return false
}

// Draw renders the page and then the box at the configured position.
func (p *cellPreview) Draw(screen tcell.Screen) {
	if currentContent != nil {
		currentContent.beginFrame()
	}
	// The table is as wide as the frame (no side borders), and the frame's
	// rect is known before the table's own is set by the draw.
	_, _, frameW, _ := p.GetInnerRect()
	if len(tabs) > 1 && frameW != tabLineWidth {
		// The tab line is laid out for this width, known only here.
		tabLineWidth = frameW
		drawFooterText(fileNameStr, statusMessage, cursorPosStr)
	}
	pinColumnOffset(frameW)
	p.Frame.Draw(screen)
	p.bw = 0 // no box drawn this frame unless it is below
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
	p.bx, p.by, p.bw, p.bh = x, y, w, h
	p.lines, p.starts = lines, lineStarts(p.text, lines)
	p.drawSelection(screen)
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

// updateCellPreview shows or hides the preview for the selected cell: the
// cell a K peek asked for, whatever its length; otherwise as the mode says,
// the full value of a cell cut by a width limit, or, on a hidden column, the
// column's name and value, which the fold marker does not show; with the box
// shown for every cell, any value that is not empty; hidden, nothing.
func updateCellPreview(row, col int) {
	if mainView == nil {
		return
	}
	if mainView.peek {
		if row == mainView.peekRow && col == mainView.peekCol {
			if text, ok := b.cellAt(row, col); ok && text != "" {
				mainView.show(columnTitle(col), text, row, col)
				return
			}
		}
		mainView.peek = false // the cursor moved on
	}
	if previewShow == previewOff {
		mainView.hide()
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
		return
	}
	if previewShow == previewAll && row >= b.rowFreeze {
		if text, ok := b.cellAt(row, col); ok && text != "" {
			mainView.show(columnTitle(col), text, row, col)
			return
		}
	}
	mainView.hide()
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
