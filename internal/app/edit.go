package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Editing works like fdisk: every change is staged in memory and the file is
// only touched by an explicit write. The stack of edits is the undo history
// and, at the same time, the change log that tells the user what differs from
// the file.

// edit is one undoable change: rows removed, columns removed, or cells changed.
type edit struct {
	rows     []removedRow          // rows removed, with their indexes in the unfiltered table
	colAt    int                   // index of the first removed column
	cols     []removedCol          // columns removed, in order
	names    []string              // header names of the removed columns, for the summary
	filters  map[int]FilterOptions // filters that were on the removed columns
	widths   map[int]int           // width limits that were on the removed columns
	cells    []cellChange          // cells changed
	order    [][]string            // row order before a sort
	sortedBy string                // "Age ascending", for the summary
}

// cellChange remembers the previous value of one cell. The row slice is the
// row's identity (see the note above removedRow in buffer.go).
type cellChange struct {
	row []string
	col int
	old string
}

// edits is the undo stack: the changes not yet written, oldest first.
var edits []edit

// dirty reports whether the table differs from the file.
func dirty() bool { return len(edits) > 0 }

// baseBuffer is the unfiltered table that edits apply to: the buffer the
// filters were derived from, or b itself when no filter has been applied.
func baseBuffer() *Buffer {
	if originalBuffer != nil {
		return originalBuffer
	}
	return b
}

// loading reports whether rows are still being appended or post-processed by
// the loader. Edits wait for it: a removed column would be re-added by
// resizeColUnsafe, and type detection indexes columns by position.
func loading() bool { return !loadProgress.IsComplete.Load() }

// editsAllowed reports whether the table may be changed now and says why not
// in the footer otherwise.
func editsAllowed() bool {
	if loading() {
		drawFooterText(fileNameStr, "Still loading; wait before editing", cursorPosStr)
		return false
	}
	return true
}

// plural renders "1 row" or "3 rows".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// editSummary describes the pending edits, e.g. "1 column (Age) and 3 rows
// removed, 2 cells changed", or "" when there are none.
func editSummary() string {
	rows, cells := 0, 0
	var cols []string
	sortedBy := ""
	for _, e := range edits {
		rows += len(e.rows)
		cells += len(e.cells)
		cols = append(cols, e.names...)
		if e.sortedBy != "" {
			sortedBy = e.sortedBy
		}
	}
	var removed []string
	if len(cols) > 0 {
		names := cols
		if len(names) > 4 {
			names = append(append([]string{}, names[:3]...), "...")
		}
		removed = append(removed, plural(len(cols), "column")+" ("+strings.Join(names, ", ")+")")
	}
	if rows > 0 {
		removed = append(removed, plural(rows, "row"))
	}
	var parts []string
	if len(removed) > 0 {
		parts = append(parts, strings.Join(removed, " and ")+" removed")
	}
	if cells > 0 {
		parts = append(parts, plural(cells, "cell")+" changed")
	}
	if sortedBy != "" {
		parts = append(parts, "sorted by "+sortedBy)
	}
	return strings.Join(parts, ", ")
}

// footerFileName is the footer's left text: the file name, [+] while edits
// are pending, and the help hint.
func footerFileName() string {
	name := filepath.Base(args.FileName)
	if dirty() {
		name += " [+]"
	}
	return name + "  |  ? help"
}

// keyHint renders "W write" for the footer, or "" when the action is unbound.
func keyHint(act action, verb string) string {
	if k := keys.keysFor(act); k != "" {
		return k + " " + verb
	}
	return ""
}

// editStatus reports what an edit did together with what is pending.
func editStatus(did string) {
	fileNameStr = footerFileName()
	msg := did
	if summary := editSummary(); summary != "" {
		hints := []string{}
		for _, h := range []string{keyHint(actWrite, "write"), keyHint(actUndo, "undo")} {
			if h != "" {
				hints = append(hints, h)
			}
		}
		msg += "  |  pending: " + summary
		if len(hints) > 0 {
			msg += "  |  " + strings.Join(hints, ", ")
		}
	} else {
		msg += "  |  no pending changes"
	}
	drawFooterText(fileNameStr, msg, cursorPosStr)
}

// refreshView re-derives the filtered view after an edit, as adding or
// removing a filter does, and puts the cursor on row, col clamped to the
// table. Filters that no longer match any row are cleared rather than shown
// as an empty table; the returned note says so for the footer.
func refreshView(row, col int) (note string) {
	if len(activeFilters) > 0 && originalBuffer != nil {
		view := applyActiveFilters(originalBuffer)
		if view.rowLen <= view.rowFreeze {
			for c := range activeFilters {
				delete(activeFilters, c)
			}
			note = "; filters cleared, no rows matched"
		} else {
			b, isFiltered = view, true
		}
	}
	if len(activeFilters) == 0 {
		if originalBuffer != nil {
			b = originalBuffer
		}
		isFiltered = false
	}
	resetSearch()
	drawBuffer(b, bufferTable)
	bufferTable.Select(clampRow(row, b), clampInt(col, 0, b.colLen-1))
	updateCellPreview(bufferTable.GetSelection())
	return note
}

// orderRange returns lo <= hi clamped to [min, max].
func orderRange(a, b, min, max int) (lo, hi int) {
	if a > b {
		a, b = b, a
	}
	return clampInt(a, min, max), clampInt(b, min, max)
}

// deleteRows removes data rows r1..r2 of the view, and the same rows from the
// unfiltered table, as one edit. With toClipboard the rows are copied first (X).
func deleteRows(r1, r2 int, toClipboard bool) {
	if !editsAllowed() {
		return
	}
	r1, r2 = orderRange(r1, r2, firstDataRow(b), b.rowLen-1)
	n := r2 - r1 + 1
	if n >= b.rowLen-b.rowFreeze {
		drawFooterText(fileNameStr, "Cannot remove every row", cursorPosStr)
		return
	}
	_, col := bufferTable.GetSelection()
	did := "Removed " + plural(n, "row")
	if toClipboard {
		// The rows leave the table only once the clipboard has accepted them.
		channels, ok := cutToClipboard(tsv(b.cellBlock(r1, 0, r2, b.colLen-1)))
		if !ok {
			return
		}
		did = "Cut " + plural(n, "row") + " to " + channels
	}
	removed := baseBuffer().removeRows(b.cont[r1 : r2+1])
	edits = append(edits, edit{rows: removed})
	editStatus(did + refreshView(r1, col))
}

// deleteColumns removes columns c1..c2 from the whole table as one edit,
// dropping the filters and width limits that were on them (undo restores
// them). With toClipboard the visible cells of the columns are copied first.
func deleteColumns(c1, c2 int, toClipboard bool) {
	if !editsAllowed() {
		return
	}
	base := baseBuffer()
	c1, c2 = orderRange(c1, c2, 0, base.colLen-1)
	k := c2 - c1 + 1
	if k >= base.colLen {
		drawFooterText(fileNameStr, "Cannot remove every column", cursorPosStr)
		return
	}
	row, _ := bufferTable.GetSelection()
	names := make([]string, 0, k)
	for c := c1; c <= c2; c++ {
		names = append(names, columnTitle(c))
	}
	did := "Removed " + plural(k, "column") + " (" + strings.Join(names, ", ") + ")"
	if toClipboard {
		// Whole columns are removed, so whole columns are copied: every row of
		// the unfiltered table, header included.
		channels, ok := cutToClipboard(tsv(base.cellBlock(0, c1, base.rowLen-1, c2)))
		if !ok {
			return
		}
		did = "Cut " + plural(k, "column") + " (" + strings.Join(names, ", ") + ") to " + channels
	}
	e := edit{colAt: c1, cols: base.removeColumns(c1, c2), names: names}
	e.filters = dropColumnKeys(activeFilters, c1, c2)
	e.widths = dropColumnKeys(wrappedColumns, c1, c2)
	edits = append(edits, e)
	editStatus(did + refreshView(row, c1))
}

// clearCells empties the cells in rows r1..r2, columns c1..c2 of the view as
// one edit. Cells that are already empty are left alone.
func clearCells(r1, c1, r2, c2 int) {
	if !editsAllowed() {
		return
	}
	r1, r2 = orderRange(r1, r2, firstDataRow(b), b.rowLen-1)
	c1, c2 = orderRange(c1, c2, 0, b.colLen-1)
	base := baseBuffer()
	var changes []cellChange
	for r := r1; r <= r2; r++ {
		row := b.cont[r]
		for c := c1; c <= c2 && c < len(row); c++ {
			if row[c] == "" {
				continue
			}
			changes = append(changes, cellChange{row, c, row[c]})
			base.setCell(row, c, "")
		}
	}
	if len(changes) == 0 {
		drawFooterText(fileNameStr, "Nothing to clear", cursorPosStr)
		return
	}
	edits = append(edits, edit{cells: changes})
	resetSearch()
	editStatus("Cleared " + plural(len(changes), "cell"))
}

// changeCell records the previous value of one cell and stores the new one as
// an edit. Nothing is recorded when the value is unchanged.
func changeCell(rowIdx, col int, value string) {
	if !editsAllowed() || rowIdx < 0 || rowIdx >= b.rowLen || col < 0 || col >= len(b.cont[rowIdx]) {
		return
	}
	row := b.cont[rowIdx]
	if row[col] == value {
		drawFooterText(fileNameStr, statusMessage, cursorPosStr)
		return
	}
	edits = append(edits, edit{cells: []cellChange{{row, col, row[col]}}})
	base := baseBuffer()
	base.setCell(row, col, value)
	if b != base {
		b.trackWidth(col, value)
	}
	resetSearch()
	editStatus(fmt.Sprintf("Changed %s at row %d", columnTitle(col), rowIdx))
}

// undoEdits reverts the n most recent edits.
func undoEdits(n int) {
	if len(edits) == 0 {
		drawFooterText(fileNameStr, "Already at oldest change", cursorPosStr)
		return
	}
	row, col := bufferTable.GetSelection()
	var did []string
	for ; n > 0 && len(edits) > 0; n-- {
		e := edits[len(edits)-1]
		edits = edits[:len(edits)-1]
		base := baseBuffer()
		switch {
		case len(e.rows) > 0:
			base.insertRows(e.rows)
			row = e.rows[0].index
			did = append(did, plural(len(e.rows), "row")+" restored")
		case len(e.cols) > 0:
			base.insertColumns(e.colAt, e.cols)
			restoreColumnKeys(activeFilters, e.colAt, len(e.cols), e.filters)
			restoreColumnKeys(wrappedColumns, e.colAt, len(e.cols), e.widths)
			col = e.colAt
			did = append(did, plural(len(e.cols), "column")+" ("+strings.Join(e.names, ", ")+") restored")
		case len(e.cells) > 0:
			for i := len(e.cells) - 1; i >= 0; i-- {
				c := e.cells[i]
				base.setCell(c.row, c.col, c.old)
				if b != base {
					b.trackWidth(c.col, c.old)
				}
			}
			if r := rowIndex(b, e.cells[0].row); r >= 0 {
				row, col = r, e.cells[0].col
			}
			did = append(did, plural(len(e.cells), "cell")+" restored")
		case e.order != nil:
			base.restoreOrder(e.order)
			did = append(did, "order before sorting by "+e.sortedBy+" restored")
		}
	}
	editStatus("Undo: " + strings.Join(did, ", ") + refreshView(row, col))
}

// rowIndex finds a row in buf by identity; -1 when it is not in buf (filtered out).
func rowIndex(buf *Buffer, row []string) int {
	if len(row) == 0 {
		return -1
	}
	buf.mu.RLock()
	defer buf.mu.RUnlock()
	for i, r := range buf.cont {
		if len(r) > 0 && &r[0] == &row[0] {
			return i
		}
	}
	return -1
}

// cutToClipboard copies the text of a cut and reports the channels used; when
// no channel accepted it the footer says so and ok is false, so the caller
// leaves the table unchanged.
func cutToClipboard(text string) (channels string, ok bool) {
	channels, err := copyToClipboard(text)
	if err != nil {
		drawFooterText(fileNameStr, "Not cut, clipboard failed: "+err.Error(), cursorPosStr)
		return "", false
	}
	return channels, true
}

// sortTable sorts the unfiltered table by column using its detected type and
// records the previous order as an edit, so the sort is written by W and
// undone by u. A filtered view is re-derived, so it shows the new order too.
func sortTable(column int, desc bool) {
	if !editsAllowed() {
		return
	}
	base := baseBuffer()
	if column < 0 || column >= base.colLen {
		return
	}
	row, _ := bufferTable.GetSelection()
	drawFooterText(fileNameStr, "Sorting...", cursorPosStr)
	if app != nil {
		app.ForceDraw()
	}
	base.mu.RLock()
	order := append([][]string(nil), base.cont...)
	base.mu.RUnlock()
	switch base.getColType(column) {
	case colTypeFloat:
		base.sortByNum(column, desc)
	case colTypeDate:
		base.sortByDate(column, desc)
	default:
		base.sortByStr(column, desc)
	}
	direction := "ascending"
	if desc {
		direction = "descending"
	}
	edits = append(edits, edit{order: order, sortedBy: columnTitle(column) + " " + direction})
	editStatus("Sorted by " + columnTitle(column) + " " + direction + refreshView(row, column))
}

// resetSearch drops the search highlighting without a footer message; edits
// move cells, so the recorded positions no longer apply.
func resetSearch() {
	searchQuery = ""
	setSearchResults(nil)
	currentSearchIndex = -1
}

// dropColumnKeys removes the entries of columns c1..c2 from m and returns
// them; the entries of later columns move down by the width of the range.
func dropColumnKeys[V any](m map[int]V, c1, c2 int) map[int]V {
	k := c2 - c1 + 1
	dropped := make(map[int]V)
	cols := sortedKeys(m)
	for _, c := range cols {
		if c < c1 {
			continue
		}
		v := m[c]
		delete(m, c)
		if c <= c2 {
			dropped[c] = v
		} else {
			m[c-k] = v
		}
	}
	return dropped
}

// restoreColumnKeys undoes dropColumnKeys: entries of columns at and later
// move up by k and the dropped entries come back.
func restoreColumnKeys[V any](m map[int]V, at, k int, dropped map[int]V) {
	cols := sortedKeys(m)
	for i := len(cols) - 1; i >= 0; i-- {
		if c := cols[i]; c >= at {
			m[c+k] = m[c]
			delete(m, c)
		}
	}
	for c, v := range dropped {
		m[c] = v
	}
}

// sortedKeys returns the keys of m in ascending order.
func sortedKeys[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}
