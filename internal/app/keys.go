package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
)

// chordTimeout is how long a partial key sequence (such as the first g of gg)
// waits for its next key before being discarded.
const chordTimeout = 500 * time.Millisecond

// Pending key sequence state for multi-key bindings.
var (
	pendingChord      []keyStroke
	pendingChordSince time.Time
)

// Operator-pending state: d waits for a motion (dj, d$) or for itself (dd),
// as in vim. pendingOpRaw is the count typed before the operator (0 when
// none) and pendingOpCount the same as a repeat factor (at least 1).
var (
	pendingOp      action
	pendingOpRaw   int
	pendingOpCount int
)

// handleTableKey is the table's input capture: it turns key events into
// actions through the active keymap. Digits form a count prefix. Every
// unbound key is swallowed, so tview's own table bindings (arrows, paging,
// Home/End, Escape) cannot bypass the keymap once a user remaps them.
// Ctrl-C is routed to the quit flow by the application-level capture.
func handleTableKey(event *tcell.EventKey) *tcell.EventKey {
	// A cell being edited takes every key until Enter or Esc closes it.
	if cellEdit != nil {
		cellEdit.handleKey(event)
		return nil
	}

	// Vim-style count prefix: digits accumulate and the next action uses
	// them (5j, 3l, 12G). A leading 0 is left to the keymap (first_column).
	if event.Key() == tcell.KeyRune && pushCountDigit(event.Rune()) {
		drawFooterText(fileNameStr, statusMessage, cursorPosStr)
		return nil
	}

	stroke := strokeFromEvent(event)
	if len(pendingChord) > 0 && time.Since(pendingChordSince) > chordTimeout {
		pendingChord = nil
	}
	seq := append(append([]keyStroke{}, pendingChord...), stroke)
	act, prefix := keys.resolve(seq)
	if act == "" && len(pendingChord) > 0 && pendingOp == "" {
		// The sequence went nowhere: drop the prefix and retry the key alone.
		// With an operator pending the failed motion cancels it instead, so
		// "d g x" never runs x on its own.
		seq = []keyStroke{stroke}
		act, prefix = keys.resolve(seq)
	}
	if act == "" && prefix {
		pendingChord, pendingChordSince = seq, time.Now()
		return nil
	}
	pendingChord = nil
	if act == "" {
		pendingCount = 0
		cancelOperator()
		return nil
	}

	rawCount, count := takeCount()
	info, _ := actionByName(string(act))
	if pendingOp != "" {
		finishOperator(act, info, rawCount, count)
		return nil
	}
	if visual != visualOff {
		switch act {
		case actYank:
			yankVisual(false)
			return nil
		case actYankRow:
			yankVisual(true)
			return nil
		case actDelete, actCut:
			// Structure: V removes the selected rows, v the selected columns.
			r1, c1, r2, c2 := visualRect()
			kind := visual
			visual = visualOff
			if kind == visualRows {
				deleteRows(r1, r2, act == actCut)
			} else {
				deleteColumns(c1, c2, act == actCut)
			}
			return nil
		case actClear:
			r1, c1, r2, c2 := visualRect()
			visual = visualOff
			clearCells(r1, c1, r2, c2)
			return nil
		case actInsert, actAppend, actChange:
			// Block insert: type once, apply to every selected cell.
			r1, c1, r2, c2 := visualRect()
			visual = visualOff
			startBulkEdit(act, r1, c1, r2, c2)
			return nil
		case actPaste:
			r1, c1, r2, c2 := visualRect()
			visual = visualOff
			pasteCells(r1, c1, r2, c2)
			return nil
		case actCancel, actQuit:
			// In visual mode q backs out of the selection like Esc; it never quits.
			exitVisual("All Done")
			return nil
		case actVisual, actVisualRow, actVisualSwap:
		default:
			if !info.motion {
				exitVisual("All Done")
			}
		}
	}
	if act == actDelete {
		pendingOp, pendingOpRaw, pendingOpCount = act, rawCount, count
		drawFooterText(fileNameStr, statusMessage, cursorPosStr)
		return nil
	}
	if info.motion {
		userMovedCursor = true
		if rawCount > 0 {
			// Redraw the footer after the motion so the pending count disappears
			// even when the selection-changed throttle skips this update.
			defer drawFooterText(fileNameStr, statusMessage, cursorPosStr)
		}
	}
	runAction(act, rawCount, count)
	return nil
}

// showcmdWidth is the fixed width of the footer slot that shows the typed but
// unfinished command (vim's 'showcmd'). The slot sits at the right edge, after
// the cursor position, and is always present, so the footer keeps still while
// a count is typed instead of growing into the status text.
const showcmdWidth = 8

// footerRight composes the footer's right text: the cursor position, then the
// showcmd slot.
func footerRight(pos string) string {
	return pos + fmt.Sprintf("%-*s", showcmdWidth, pendingKeys())
}

// pendingKeys renders the typed but unfinished command for the footer, as
// vim's showcmd does: "3", "d", "2d3".
func pendingKeys() string {
	s := ""
	if pendingOp != "" {
		if pendingOpRaw > 0 {
			s = strconv.Itoa(pendingOpRaw)
		}
		s += keys.keysFor(pendingOp)
	}
	if pendingCount > 0 {
		s += strconv.Itoa(pendingCount)
	}
	return s
}

// cancelOperator drops a pending operator and redraws the footer without it.
func cancelOperator() {
	if pendingOp == "" {
		return
	}
	pendingOp, pendingOpRaw, pendingOpCount = "", 0, 0
	drawFooterText(fileNameStr, statusMessage, cursorPosStr)
}

// saturatingMul multiplies two counts without exceeding the count cap.
func saturatingMul(a, b int) int {
	if a > maxCountPrefix/b {
		return maxCountPrefix
	}
	return a * b
}

// finishOperator completes a pending operator with the key that followed it:
// the operator itself works on the current row (dd), a motion on the rows or
// columns it spans, and anything else cancels it, as in vim. A count before
// the operator and one after it multiply (2d3j), and a count on either side
// reaches absolute motions (2dG works on rows 2 to the cursor).
func finishOperator(act action, info actionInfo, rawCount, count int) {
	op, opRaw, opCount := pendingOp, pendingOpRaw, pendingOpCount
	pendingOp, pendingOpRaw, pendingOpCount = "", 0, 0
	row, col := bufferTable.GetSelection()
	if opRaw > 0 {
		count = saturatingMul(count, opCount)
		if rawCount > 0 {
			rawCount = saturatingMul(rawCount, opRaw)
		} else {
			rawCount = opRaw
		}
	}
	switch {
	case act == op:
		deleteRows(row, row+count-1, false)
	case info.motion && act != actNextMatch && act != actPrevMatch:
		rows, lo, hi, ok := operatorRange(act, rawCount, count, row, col)
		switch {
		case !ok:
			drawFooterText(fileNameStr, statusMessage, cursorPosStr)
		case rows:
			deleteRows(lo, hi, false)
		default:
			deleteColumns(lo, hi, false)
		}
	default:
		drawFooterText(fileNameStr, statusMessage, cursorPosStr)
	}
}

// operatorRange turns a motion into the rows (linewise, both ends inclusive)
// or columns (exclusive end for h, l, w, b and 0; inclusive for $) an operator
// acts on, with vim's rules: horizontal motions do not wrap, and a motion that
// cannot move (dh in the first column, dj on the last row) does nothing.
func operatorRange(motion action, rawCount, count, row, col int) (rows bool, lo, hi int, ok bool) {
	last := b.colLen - 1
	switch motion {
	case actMoveRight, actNextColumn:
		return false, col, min(col+count-1, last), true
	case actMoveLeft, actPrevColumn:
		return false, max(col-count, 0), col - 1, col > 0
	case actLastColumn:
		return false, col, last, true
	case actFirstColumn:
		return false, 0, col - 1, col > 0
	}
	r, _, isMotion := motionTarget(motion, rawCount, count, row, col)
	if !isMotion {
		return false, 0, 0, false
	}
	if r == row && motion != actFirstRow && motion != actLastRow {
		return true, 0, 0, false
	}
	return true, min(row, r), max(row, r), true
}

// motionTarget returns where a motion moves the cursor from row, col; ok is
// false for actions that are not motions over the table.
func motionTarget(act action, rawCount, count, row, col int) (r, c int, ok bool) {
	firstRow, lastRow, numCols := firstDataRow(b), b.rowLen-1, b.colLen
	switch act {
	case actMoveLeft, actPrevColumn:
		return row, wrapCol(col-count, numCols), true
	case actMoveRight, actNextColumn:
		return row, wrapCol(col+count, numCols), true
	case actMoveDown:
		return clampInt(row+count, firstRow, lastRow), col, true
	case actMoveUp:
		return clampInt(row-count, firstRow, lastRow), col, true
	case actFirstRow:
		return clampInt(rawCount, firstRow, lastRow), col, true
	case actLastRow:
		if rawCount > 0 {
			return clampInt(rawCount, firstRow, lastRow), col, true
		}
		return lastRow, col, true
	case actFirstColumn:
		return row, 0, true
	case actLastColumn:
		return row, numCols - 1, true
	case actHalfPageDown, actHalfPageUp:
		step := halfPageRows(bufferTable)
		if rawCount > 0 {
			step = rawCount
		}
		if act == actHalfPageUp {
			step = -step
		}
		return clampInt(row+step, firstRow, lastRow), col, true
	case actPageDown, actPageUp:
		step := pageRows(bufferTable) * count
		if act == actPageUp {
			step = -step
		}
		return clampInt(row+step, firstRow, lastRow), col, true
	}
	return row, col, false
}

// runAction performs an action. rawCount is the typed count or 0; count is
// rawCount or 1, ready to use as a repeat factor.
func runAction(act action, rawCount, count int) {
	row, col := bufferTable.GetSelection()
	if r, c, ok := motionTarget(act, rawCount, count, row, col); ok {
		// tview scrolls just enough to show the selection when it draws. Its
		// ScrollToBeginning/ScrollToEnd are not used: they zero the column
		// offset, so gg and G made the columns on screen jump. A vertical
		// motion keeps the horizontal scroll exactly where it was.
		_, colOffset := bufferTable.GetOffset()
		bufferTable.Select(r, c)
		if c == col {
			rowOffset, _ := bufferTable.GetOffset()
			bufferTable.SetOffset(rowOffset, colOffset)
		}
		return
	}

	switch act {
	case actSearch:
		openSearchDialog()
	case actNextMatch:
		gotoSearchResult(count)
	case actPrevMatch:
		gotoSearchResult(-count)
	case actCancel:
		clearSearch()
	case actFilter:
		openFilterDialog()
	case actRemoveFilter:
		removeCurrentFilter()
	case actSortAsc:
		sortCurrentColumn(false)
	case actSortDesc:
		sortCurrentColumn(true)
	case actToggleType:
		toggleColumnType()
	case actToggleWidth:
		toggleColumnWidth()
	case actYank:
		yankCells(row, col, row, col)
	case actYankRow:
		yankCells(row, 0, row, b.colLen-1)
	case actVisual:
		startVisual(visualBlock)
	case actVisualRow:
		startVisual(visualRows)
	case actVisualSwap:
		swapVisualAnchor()
	case actCut:
		deleteRows(row, row+count-1, true)
	case actClear:
		clearCells(row, col, row, col+count-1)
	case actPaste:
		pasteCells(row, col, row, col)
	case actUndo:
		undoEdits(count)
	case actEdit, actInsert, actAppend, actChange:
		startCellEdit(act)
	case actWrite:
		writeTable()
	case actStats:
		showCurrentColumnStats()
	case actHelp:
		showHelpDialog()
	case actQuit:
		requestQuit()
	}
}

// gotoSearchResult moves delta matches forward (or back when negative),
// wrapping around the result list.
func gotoSearchResult(delta int) {
	if len(searchResults) == 0 || currentSearchIndex < 0 {
		if searchQuery != "" {
			drawFooterText(fileNameStr, "No search results. Press / to search", cursorPosStr)
		}
		return
	}
	n := len(searchResults)
	currentSearchIndex = ((currentSearchIndex+delta)%n + n) % n
	bufferTable.Select(searchResults[currentSearchIndex].Row, searchResults[currentSearchIndex].Col)
	drawBuffer(b, bufferTable) // Redraw to update highlighting
	drawFooterText(fileNameStr, fmt.Sprintf("Match %d/%d", currentSearchIndex+1, n), cursorPosStr)
}

// clearSearch drops the search highlighting.
func clearSearch() {
	if searchQuery == "" {
		return
	}
	resetSearch()
	drawBuffer(b, bufferTable)
	drawFooterText(fileNameStr, "Search cleared", cursorPosStr)
}

// sortCurrentColumn sorts the table by the selected column using its detected
// type. The sort is an edit: it changes the order that is written, and u
// restores the previous order.
func sortCurrentColumn(desc bool) {
	_, column := bufferTable.GetSelection()
	sortTable(column, desc)
}
