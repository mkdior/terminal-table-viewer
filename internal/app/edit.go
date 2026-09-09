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
	hidden   map[int]bool          // hidden flags that were on the removed columns
	cells    []cellChange          // cells changed
	order    [][]string            // row order before a sort
	sortedBy string                // "Age ascending", for the summary
	added    []removedRow          // empty rows inserted (undo removes them by identity)
	addedAt  int                   // index of the first inserted column
	addedN   int                   // number of inserted columns
	prevRows [][]string            // row slices before the columns were inserted
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

// tableRegister holds the cells of the last yank or removal, for p and P.
// Like vim's unnamed register it is filled by y, Y, visual y, X and d; x
// (clear) leaves it alone so a yanked value survives clearing other cells.
var tableRegister [][]string

// setRegister records a yanked or removed block; a single cell also becomes
// the line editor's register, so it can be pasted inside another cell.
func setRegister(block [][]string) {
	if len(block) == 0 || len(block[0]) == 0 {
		return
	}
	tableRegister = block
	if len(block) == 1 && len(block[0]) == 1 {
		lineRegister = []rune(block[0][0])
	}
}

// pasteCells replaces cells with the register. A single value fills rows
// r1..r2, columns c1..c2 (the cursor cell, or the visual selection); a block
// is laid out from the top-left corner and clipped to the table. When nothing
// was yanked from the table, the line editor's register is pasted.
func pasteCells(r1, c1, r2, c2 int) {
	if !editsAllowed() {
		return
	}
	reg := tableRegister
	if len(reg) == 0 && len(lineRegister) > 0 {
		reg = [][]string{{string(lineRegister)}}
	}
	if len(reg) == 0 || len(reg[0]) == 0 {
		drawFooterText(fileNameStr, "Nothing to paste", cursorPosStr)
		return
	}
	var targets []cellTarget
	if len(reg) == 1 && len(reg[0]) == 1 {
		v := reg[0][0]
		targets = rectTargets(r1, c1, r2, c2, func(string) string { return v })
	} else {
		top, _ := orderRange(r1, r2, firstDataRow(b), b.rowLen-1)
		left, _ := orderRange(c1, c2, 0, b.colLen-1)
		for i, row := range reg {
			r := top + i
			if r >= b.rowLen {
				break
			}
			for j, v := range row {
				if c := left + j; c < len(b.cont[r]) {
					targets = append(targets, cellTarget{b.cont[r], c, v})
				}
			}
		}
	}
	if setCells(targets, "Pasted") == 0 {
		drawFooterText(fileNameStr, "No change", cursorPosStr)
	}
}

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
func loading() bool { return !baseBuffer().progress.IsComplete.Load() }

// editsAllowed reports whether the table may be changed now and says why not
// in the footer otherwise: a streamed table is read-only (its rows are not in
// memory to change), and a loading one is not settled yet.
func editsAllowed() bool {
	if baseBuffer().streamed() {
		drawFooterText(fileNameStr, "Read-only: "+filepath.Base(args.FileName)+" is streamed from disk; load it into memory to edit (see --stream-above)", cursorPosStr)
		return false
	}
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
	rows, cells, rowsAdded, colsAdded := 0, 0, 0, 0
	var cols []string
	sortedBy := ""
	for _, e := range edits {
		rows += len(e.rows)
		cells += len(e.cells)
		rowsAdded += len(e.added)
		colsAdded += e.addedN
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
	var added []string
	if colsAdded > 0 {
		added = append(added, plural(colsAdded, "column"))
	}
	if rowsAdded > 0 {
		added = append(added, plural(rowsAdded, "row"))
	}
	if len(added) > 0 {
		parts = append(parts, strings.Join(added, " and ")+" added")
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
// are pending or [streamed] for a table read from disk, and the help hint.
func footerFileName() string {
	name := filepath.Base(args.FileName)
	switch {
	case dirty():
		name += " [+]"
	case baseBuffer().streamed():
		name += " [streamed]"
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
// unfiltered table, as one edit; the rows go to the register and the clipboard.
func deleteRows(r1, r2 int) {
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
	block := b.cellBlock(r1, 0, r2, b.colLen-1)
	did := copyRemoved(tsv(block), "Removed "+plural(n, "row"))
	setRegister(block)
	removed := baseBuffer().removeRows(b.cont[r1 : r2+1])
	edits = append(edits, edit{rows: removed})
	editStatus(did + refreshView(r1, col))
}

// deleteColumns removes columns c1..c2 from the whole table as one edit,
// dropping the filters and width limits that were on them (undo restores
// them). The columns go to the clipboard with their header, and their data
// cells to the register.
func deleteColumns(c1, c2 int) {
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
	// Whole columns are removed, so whole columns are copied: every row of the
	// unfiltered table, header included.
	did := copyRemoved(tsv(base.cellBlock(0, c1, base.rowLen-1, c2)), "Removed "+plural(k, "column")+" ("+strings.Join(names, ", ")+")")
	setRegister(base.cellBlock(base.rowFreeze, c1, base.rowLen-1, c2)) // the data cells, for p
	e := edit{colAt: c1, cols: base.removeColumns(c1, c2), names: names}
	e.filters = dropColumnKeys(activeFilters, c1, c2)
	e.widths = dropColumnKeys(wrappedColumns, c1, c2)
	e.hidden = dropColumnKeys(hiddenCols, c1, c2)
	edits = append(edits, e)
	editStatus(did + refreshView(row, c1))
}

// cellTarget is one cell to store a value in: the row by identity, the column,
// and the new value.
type cellTarget struct {
	row   []string
	col   int
	value string
}

// rectTargets lists the cells of the view in rows r1..r2, columns c1..c2 with
// the value f gives for each current value.
func rectTargets(r1, c1, r2, c2 int, f func(string) string) []cellTarget {
	r1, r2 = orderRange(r1, r2, firstDataRow(b), b.rowLen-1)
	c1, c2 = orderRange(c1, c2, 0, b.colLen-1)
	var targets []cellTarget
	for r := r1; r <= r2; r++ {
		row := b.cont[r]
		for c := c1; c <= c2 && c < len(row); c++ {
			targets = append(targets, cellTarget{row, c, f(row[c])})
		}
	}
	return targets
}

// setCells stores values in many cells as one edit and re-derives a filtered
// view; cells that already hold their value are skipped. did names the
// operation for the footer ("Pasted", "Changed") and is followed by the cell
// count. It returns how many cells changed; on zero nothing is recorded and
// the footer is left to the caller.
func setCells(targets []cellTarget, did string) int {
	return setCellsStatus(targets, func(n int) string { return did + " " + plural(n, "cell") })
}

// setCellsStatus is setCells with the footer text built from the number of
// cells that changed.
func setCellsStatus(targets []cellTarget, status func(n int) string) int {
	if !editsAllowed() {
		return 0
	}
	base := baseBuffer()
	var changes []cellChange
	for _, tg := range targets {
		if tg.col < 0 || tg.col >= len(tg.row) || tg.row[tg.col] == tg.value {
			continue
		}
		changes = append(changes, cellChange{tg.row, tg.col, tg.row[tg.col]})
		base.setCell(tg.row, tg.col, tg.value)
		if b != base {
			b.trackWidth(tg.col, tg.value)
		}
	}
	if len(changes) == 0 {
		return 0
	}
	edits = append(edits, edit{cells: changes})
	row, col := bufferTable.GetSelection()
	editStatus(status(len(changes)) + refreshView(row, col))
	return len(changes)
}

// clearCells cuts the cells in rows r1..r2, columns c1..c2 of the view: the
// block goes to the register and the clipboard, as vim's x deletes into the
// register, and the cells are emptied as one edit. Cells that are already
// empty are left alone, and with nothing to cut the table is left as it is.
// Like every edit it re-derives a filtered view, so a row that stops matching
// disappears.
func clearCells(r1, c1, r2, c2 int) {
	if !editsAllowed() {
		return
	}
	r1, r2 = orderRange(r1, r2, firstDataRow(b), b.rowLen-1)
	c1, c2 = orderRange(c1, c2, 0, b.colLen-1)
	block := b.cellBlock(r1, c1, r2, c2)
	n := 0
	for _, row := range block {
		for _, v := range row {
			if v != "" {
				n++
			}
		}
	}
	if n == 0 {
		drawFooterText(fileNameStr, "Nothing to cut", cursorPosStr)
		return
	}
	did := copyRemoved(tsv(block), "Cut "+plural(n, "cell"))
	setRegister(block)
	setCellsStatus(rectTargets(r1, c1, r2, c2, func(string) string { return "" }), func(int) string { return did })
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
	cursorRow := rowIdx
	if rowIdx < firstDataRow(b) { // a header edit keeps the cursor on its row
		cursorRow, _ = bufferTable.GetSelection()
	}
	editStatus(fmt.Sprintf("Changed %s at row %d", columnTitle(col), rowIdx) + refreshView(cursorRow, col))
}

// maxInsert caps how many rows or columns one command may add.
const maxInsert = 1000

// insertRows adds n empty rows above (or, with below, under) row r of the
// table as one edit, moves the cursor to the first of them and starts typing
// in it, as vim's o does. With a filter active the new rows would not be
// visible, so it is refused.
func insertRows(r, n int, below bool) {
	if !editsAllowed() {
		return
	}
	if isFiltered {
		drawFooterText(fileNameStr, "Clear the filters to insert rows", cursorPosStr)
		return
	}
	n = clampInt(n, 1, maxInsert)
	at := r
	if below {
		at = r + 1
	}
	at = clampInt(at, firstDataRow(b), b.rowLen)
	rows := make([]removedRow, n)
	for i := range rows {
		rows[i] = removedRow{at + i, make([]string, b.colLen)}
	}
	b.insertRows(rows)
	edits = append(edits, edit{added: rows})
	_, col := bufferTable.GetSelection()
	editStatus("Added " + plural(n, "row") + refreshView(at, col))
	startCellEdit(actInsert)
}

// insertColumns adds n empty columns left of (or, with right, after) column c
// as one edit, moves the cursor onto the first and opens its header cell for
// a name. Filters and width limits follow their columns.
func insertColumns(c, n int, right bool) {
	if !editsAllowed() {
		return
	}
	n = clampInt(n, 1, maxInsert)
	base := baseBuffer()
	at := c
	if right {
		at = c + 1
	}
	at = clampInt(at, 0, base.colLen)
	cols := make([]removedCol, n)
	for i := range cols {
		cols[i] = removedCol{cells: make([]string, base.rowLen), colType: colTypeStr}
	}
	prev := base.insertColumns(at, cols)
	restoreColumnKeys(activeFilters, at, n, nil)
	restoreColumnKeys(wrappedColumns, at, n, nil)
	restoreColumnKeys(hiddenCols, at, n, nil)
	edits = append(edits, edit{addedAt: at, addedN: n, prevRows: prev})
	row, _ := bufferTable.GetSelection()
	editStatus("Added " + plural(n, "column") + refreshView(row, at))
	if b.rowFreeze > 0 {
		startHeaderEdit(at)
	}
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
			restoreColumnKeys(hiddenCols, e.colAt, len(e.cols), e.hidden)
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
		case len(e.added) > 0:
			rows := make([][]string, len(e.added))
			for i, r := range e.added {
				rows[i] = r.row
			}
			base.removeRows(rows)
			row = e.added[0].index
			did = append(did, plural(len(e.added), "added row")+" removed")
		case e.addedN > 0:
			base.undoInsertColumns(e.addedAt, e.addedN, e.prevRows)
			dropColumnKeys(activeFilters, e.addedAt, e.addedAt+e.addedN-1)
			dropColumnKeys(wrappedColumns, e.addedAt, e.addedAt+e.addedN-1)
			dropColumnKeys(hiddenCols, e.addedAt, e.addedAt+e.addedN-1)
			col = e.addedAt
			did = append(did, plural(e.addedN, "added column")+" removed")
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

// copyRemoved sends the text of a removal to the clipboard, as vim does with
// clipboard=unnamedplus, and returns the footer text for now: what with the
// channel that took the text, or with the tool still running, in which case
// the footer is redrawn with the outcome when it is done. The removal itself
// never waits for the clipboard: a copy that fails is only reported, and the
// register holds the text for p in any case.
func copyRemoved(text, what string) (did string) {
	var later bool // the report comes after this call returned
	did = what
	pending, err := copyToClipboard(text, func(channels string, err error) {
		if err != nil {
			did = what + "; clipboard failed: " + err.Error()
		} else {
			did = what + "; copied via " + channels
		}
		if later {
			editStatus(did)
		}
	})
	switch {
	case err != nil:
		return what + "; clipboard failed: " + err.Error()
	case pending != "":
		later = true
		return what + "; " + pending + " running"
	}
	return did
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
