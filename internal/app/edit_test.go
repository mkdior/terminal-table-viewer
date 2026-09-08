package app

import (
	"strings"
	"testing"
)

// setupEditTable installs the 5x4 table of setupVisualTable with editing
// enabled: load complete, no filters, empty undo stack, a file name.
func setupEditTable(t *testing.T) {
	t.Helper()
	setupVisualTable(t)
	oldOrig, oldFiltered, oldFilters, oldWrapped := originalBuffer, isFiltered, activeFilters, wrappedColumns
	oldEdits, oldName, oldComplete := edits, args.FileName, loadProgress.IsComplete.Load()
	oldQuery, oldResults := searchQuery, searchResults
	t.Cleanup(func() {
		originalBuffer, isFiltered, activeFilters, wrappedColumns = oldOrig, oldFiltered, oldFilters, oldWrapped
		edits, args.FileName = oldEdits, oldName
		loadProgress.IsComplete.Store(oldComplete)
		pendingOp, pendingOpCount = "", 0
		searchQuery = oldQuery
		setSearchResults(oldResults)
	})
	originalBuffer, isFiltered = nil, false
	activeFilters, wrappedColumns = map[int]FilterOptions{}, map[int]int{}
	edits = nil
	args.FileName = "table.csv"
	loadProgress.IsComplete.Store(true)
}

// column returns column c of buf as a slice, header included.
func column(buf *Buffer, c int) []string {
	out := make([]string, 0, buf.rowLen)
	for _, row := range buf.cont {
		if c < len(row) {
			out = append(out, row[c])
		}
	}
	return out
}

func joined(s []string) string { return strings.Join(s, " ") }

func TestDeleteRowsWithDdCountsAndUndo(t *testing.T) {
	setupEditTable(t)
	press(t, "d d")
	if got := joined(column(b, 0)); got != "h1 a2 a3 a4" {
		t.Fatalf("dd on the first row: %q", got)
	}
	if !strings.Contains(statusMessage, "Removed 1 row") || !strings.Contains(statusMessage, "pending: 1 row removed") {
		t.Errorf("status = %q", statusMessage)
	}
	if !strings.Contains(fileNameStr, "table.csv [+]") {
		t.Errorf("dirty marker missing: %q", fileNameStr)
	}
	press(t, "2 d d") // a2 and a3
	if got := joined(column(b, 0)); got != "h1 a4" {
		t.Fatalf("2dd: %q", got)
	}
	press(t, "d d")
	if statusMessage != "Cannot remove every row" || b.rowLen != 2 {
		t.Errorf("the last row must stay: %q, rows %d", statusMessage, b.rowLen)
	}
	press(t, "u")
	if got := joined(column(b, 0)); got != "h1 a2 a3 a4" {
		t.Fatalf("u must restore both rows in place: %q", got)
	}
	press(t, "u")
	if got := joined(column(b, 0)); got != "h1 a1 a2 a3 a4" || dirty() || strings.Contains(fileNameStr, "[+]") {
		t.Fatalf("after undoing everything: %q dirty=%v %q", got, dirty(), fileNameStr)
	}
	press(t, "u")
	if statusMessage != "Already at oldest change" {
		t.Errorf("status = %q", statusMessage)
	}
	press(t, "j j 3 d d") // only a3 and a4 are below row 3: vim deletes what is there
	if got := joined(column(b, 0)); got != "h1 a1 a2" {
		t.Errorf("an overshooting count removes the rows that exist: %q", got)
	}
	if r, _ := bufferTable.GetSelection(); r != 2 {
		t.Errorf("cursor must land on the new last row, got %d", r)
	}
	press(t, "3 d d") // from a2: only a2 is left below, a1 stays
	if got := joined(column(b, 0)); got != "h1 a1" {
		t.Errorf("3dd from the last row: %q", got)
	}
}

func TestDeleteOperatorWithMotions(t *testing.T) {
	setupEditTable(t)
	cases := []struct{ keys, want, cursor string }{
		{"d j", "h1 a3 a4", "1,0"},       // linewise: current and next
		{"j d G", "h1 a1", "1,0"},        // to the last row
		{"j j d g g", "h1 a4", "1,0"},    // to the first data row
		{"d 2 j", "h1 a4", "1,0"},        // count after the operator
		{"2 d j", "h1 a4", "1,0"},        // count before the operator multiplies
		{"d k", "h1 a1 a2 a3 a4", "1,0"}, // cannot move up from the first row: nothing
		{"G d j", "h1 a1 a2 a3 a4", "4,0"},
	}
	for _, tc := range cases {
		setupEditTable(t)
		press(t, tc.keys)
		if got := joined(column(b, 0)); got != tc.want {
			t.Errorf("%q: rows %q, want %q", tc.keys, got, tc.want)
		}
		if r, c := bufferTable.GetSelection(); I2S(r)+","+I2S(c) != tc.cursor {
			t.Errorf("%q: cursor %d,%d, want %s", tc.keys, r, c, tc.cursor)
		}
		if pendingOp != "" {
			t.Errorf("%q: operator still pending", tc.keys)
		}
	}
	colCases := []struct{ keys, want, cursor string }{
		{"l d l", "h1 h3 h4", "1,1"},   // the current column, like x in vim
		{"l l d $", "h1 h2", "1,1"},    // current to last
		{"l l d 0", "h3 h4", "1,0"},    // first to before the cursor
		{"l l d h", "h1 h3 h4", "1,1"}, // the column before the cursor
		{"d h", "h1 h2 h3 h4", "1,0"},  // nothing before the first column
		{"d 0", "h1 h2 h3 h4", "1,0"},  // nothing before the first column
		{"$ 3 d l", "h1 h2 h3", "1,2"}, // does not wrap: only the last column
		{"d $", "h1 h2 h3 h4", "1,0"},  // every column: refused
		{"l d w", "h1 h3 h4", "1,1"},   // w is a horizontal motion too
		{"l l d 2 b", "h3 h4", "1,0"},  // two columns before the cursor
	}
	for _, tc := range colCases {
		setupEditTable(t)
		press(t, tc.keys)
		if got := joined(b.cont[0]); got != tc.want {
			t.Errorf("%q: header %q, want %q", tc.keys, got, tc.want)
		}
		if r, c := bufferTable.GetSelection(); I2S(r)+","+I2S(c) != tc.cursor {
			t.Errorf("%q: cursor %d,%d, want %s", tc.keys, r, c, tc.cursor)
		}
	}
	setupEditTable(t)
	press(t, "d $")
	if statusMessage != "Cannot remove every column" {
		t.Errorf("status = %q", statusMessage)
	}
	press(t, "l d l u")
	if got := joined(b.cont[0]); got != "h1 h2 h3 h4" || dirty() {
		t.Errorf("undo of a column removal: %q", got)
	}
	if got := joined(column(b, 1)); got != "h2 b1 b2 b3 b4" {
		t.Errorf("restored column cells: %q", got)
	}
}

func TestDeleteOperatorCancels(t *testing.T) {
	setupEditTable(t)
	press(t, "2 d 3")
	if pendingOp != actDelete || pendingKeys() != "2d3" {
		t.Fatalf("pending = %q keys %q", pendingOp, pendingKeys())
	}
	press(t, "esc")
	if pendingOp != "" || pendingCount != 0 || dirty() {
		t.Error("Esc must cancel the operator and its counts")
	}
	press(t, "d x") // not a motion: cancelled, and x does not run
	if pendingOp != "" || dirty() || b.cont[1][0] != "a1" {
		t.Errorf("d x must cancel without clearing the cell: %v", b.cont[1])
	}
	press(t, "d g x") // failed chord while an operator is pending: no retry of x alone
	if pendingOp != "" || dirty() || b.cont[1][0] != "a1" {
		t.Errorf("d g x must cancel without running x: %v", b.cont[1])
	}
	press(t, "d n") // search motions are not operator motions
	if pendingOp != "" || dirty() {
		t.Error("d n must cancel")
	}
	press(t, "d Z") // unbound key
	if pendingOp != "" || dirty() {
		t.Error("an unbound key must cancel the operator")
	}
	if saturatingMul(maxCountPrefix, maxCountPrefix) != maxCountPrefix || saturatingMul(3, 4) != 12 {
		t.Error("saturatingMul")
	}
}

func TestVisualDeleteAndClear(t *testing.T) {
	setupEditTable(t)
	press(t, "V j d") // rows a1, a2
	if got := joined(column(b, 0)); got != "h1 a3 a4" || visual != visualOff {
		t.Fatalf("V j d: %q visual=%v", got, visual)
	}
	press(t, "u l v l d") // block over columns 1..2 removes those columns
	if got := joined(b.cont[0]); got != "h1 h4" || visual != visualOff {
		t.Fatalf("v l d: %q", got)
	}
	if _, c := bufferTable.GetSelection(); c != 1 {
		t.Errorf("cursor col = %d, want 1 (start of the removed range)", c)
	}
	press(t, "u")
	if got := joined(b.cont[0]); got != "h1 h2 h3 h4" {
		t.Fatalf("undo: %q", got)
	}
	press(t, "v j l x") // clear a 2x2 block: b1 c1 b2 c2
	for _, cell := range [][2]int{{1, 1}, {1, 2}, {2, 1}, {2, 2}} {
		if b.cont[cell[0]][cell[1]] != "" {
			t.Errorf("cell %v not cleared: %q", cell, b.cont[cell[0]][cell[1]])
		}
	}
	if b.cont[1][0] != "a1" || b.cont[3][1] != "b3" || editSummary() != "4 cells changed" {
		t.Errorf("clear touched other cells or summary wrong: %q", editSummary())
	}
	press(t, "u")
	if b.cont[1][1] != "b1" || b.cont[2][2] != "c2" || dirty() {
		t.Error("undo must restore the cleared cells")
	}
	press(t, "0 j 2 x") // clears a2 and b2
	if b.cont[2][0] != "" || b.cont[2][1] != "" || b.cont[2][2] != "c2" {
		t.Errorf("2x: %v", b.cont[2])
	}
	press(t, "x")
	if statusMessage != "Nothing to clear" || len(edits) != 1 {
		t.Errorf("x on an empty cell: %q, edits %d", statusMessage, len(edits))
	}
}

func TestCutCopiesBeforeRemoving(t *testing.T) {
	setupEditTable(t)
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	press(t, "j X")
	if got := joined(column(b, 0)); got != "h1 a1 a3 a4" {
		t.Fatalf("X: %q", got)
	}
	if len(*ran) != 1 || (*ran)[0] != "xclip:a2\tb2\tc2\td2" || !strings.Contains(statusMessage, "Cut 1 row to xclip") {
		t.Errorf("clipboard %v, status %q", *ran, statusMessage)
	}
	press(t, "V j X") // rows a3, a4 selected from row 2 -> wait, cursor is on a3 now
	if got := (*ran)[len(*ran)-1]; got != "xclip:a3\tb3\tc3\td3\na4\tb4\tc4\td4" {
		t.Errorf("visual X copied %q", got)
	}
	press(t, "u u l v X") // column h2, every row including the header
	if got := (*ran)[len(*ran)-1]; got != "xclip:h2\nb1\nb2\nb3\nb4" {
		t.Errorf("column X copied %q", got)
	}
	if got := joined(b.cont[0]); got != "h1 h3 h4" {
		t.Errorf("column X removed: %q", got)
	}

	// Without a clipboard nothing is removed.
	setupEditTable(t)
	stubClipboard(t, map[string]bool{}, map[string]string{}, "linux", false)
	press(t, "X")
	if b.rowLen != 5 || dirty() || !strings.HasPrefix(statusMessage, "Not cut, clipboard failed") {
		t.Errorf("a failed copy must leave the table alone: rows %d, %q", b.rowLen, statusMessage)
	}
}

func TestEditsThroughFilteredView(t *testing.T) {
	setupEditTable(t)
	originalBuffer = b
	activeFilters[0] = FilterOptions{Query: "a2|a4", Operator: "regex"}
	isFiltered = true
	b = applyActiveFilters(originalBuffer)
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 0)
	if got := joined(column(b, 0)); got != "h1 a2 a4" {
		t.Fatalf("view: %q", got)
	}

	press(t, "d d") // a2 leaves the view and the unfiltered table
	if got := joined(column(b, 0)); got != "h1 a4" {
		t.Errorf("view after dd: %q", got)
	}
	if got := joined(column(originalBuffer, 0)); got != "h1 a1 a3 a4" {
		t.Errorf("base after dd: %q", got)
	}
	press(t, "d d")
	if statusMessage != "Cannot remove every row" {
		t.Errorf("the last visible row stays: %q", statusMessage)
	}
	press(t, "u")
	if got := joined(column(originalBuffer, 0)); got != "h1 a1 a2 a3 a4" {
		t.Errorf("base after undo: %q", got)
	}
	if got := joined(column(b, 0)); got != "h1 a2 a4" || !isFiltered {
		t.Errorf("view after undo: %q filtered=%v", got, isFiltered)
	}

	// Removing the filtered column drops its filter; undo brings it back.
	wrappedColumns[1], wrappedColumns[2] = 30, 50
	press(t, "d l")
	if isFiltered || len(activeFilters) != 0 || b != originalBuffer || b.colLen != 3 {
		t.Errorf("after removing the filtered column: filtered=%v filters=%v colLen=%d", isFiltered, activeFilters, b.colLen)
	}
	if wrappedColumns[0] != 30 || wrappedColumns[1] != 50 || len(wrappedColumns) != 2 {
		t.Errorf("width limits must follow their columns: %v", wrappedColumns)
	}
	wrappedColumns[2] = 20 // a limit set after the removal, on the last column
	press(t, "u")
	if !isFiltered || activeFilters[0].Query != "a2|a4" || b.colLen != 4 || joined(column(b, 0)) != "h1 a2 a4" {
		t.Errorf("undo must restore the filter and the view: filtered=%v filters=%v rows %q", isFiltered, activeFilters, joined(column(b, 0)))
	}
	if wrappedColumns[1] != 30 || wrappedColumns[2] != 50 || wrappedColumns[3] != 20 || len(wrappedColumns) != 3 {
		t.Errorf("width limits after undo: %v", wrappedColumns)
	}

	// A cell edit re-derives the view at once: a row that stops matching
	// disappears, and when nothing matches any more the filters are dropped
	// instead of showing an empty table.
	activeFilters[0] = FilterOptions{Query: "a2|a4", Operator: "regex"}
	isFiltered = true
	b = applyActiveFilters(originalBuffer)
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 0)
	press(t, "x") // clear a2: the row no longer matches
	if got := joined(column(b, 0)); got != "h1 a4" || !isFiltered {
		t.Errorf("clearing the matching cell must drop the row from the view: %q", got)
	}
	press(t, "x") // clear a4 as well: no rows match, the filters go
	if isFiltered || len(activeFilters) != 0 || b != originalBuffer || !strings.Contains(statusMessage, "filters cleared") {
		t.Errorf("empty view must clear filters: filtered=%v filters=%v status %q", isFiltered, activeFilters, statusMessage)
	}
	if b.cont[2][0] != "" || b.cont[4][0] != "" {
		t.Errorf("the cells were cleared in the table: %q %q", b.cont[2][0], b.cont[4][0])
	}
}

// setupTallTable installs a header plus 9 rows r1..r9 (one column).
func setupTallTable(t *testing.T) {
	t.Helper()
	setupEditTable(t)
	data := [][]string{{"h1", "h2"}}
	for i := 1; i <= 9; i++ {
		data = append(data, []string{"r" + I2S(i), "x"})
	}
	buf, err := createNewBufferWithData(data, true)
	if err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1
	b = buf
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 0)
}

func TestDeleteOperatorCountsWithAbsoluteMotions(t *testing.T) {
	cases := []struct{ keys, want string }{
		{"4 j 2 d G", "h1 r1 r6 r7 r8 r9"},         // rows 2..5, as vim's 2dG from row 5
		{"4 j 2 d g g", "h1 r1 r6 r7 r8 r9"},       // the same range with gg
		{"4 j 1 d G", "h1 r6 r7 r8 r9"},            // an explicit 1 is a row number: rows 1..5 go
		{"4 j d 2 G", "h1 r1 r6 r7 r8 r9"},         // the count after the operator reaches G too
		{"4 j 2 d 3 G", "h1 r1 r2 r3 r4 r7 r8 r9"}, // counts multiply: 6G, rows 5..6
		{"2 d 3 j", "h1 r8 r9"},                    // six rows down from row 1: rows 1..7 go
		{"4 j d G", "h1 r1 r2 r3 r4"},              // no count: to the last row
	}
	for _, tc := range cases {
		setupTallTable(t)
		press(t, tc.keys)
		if got := joined(column(b, 0)); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.keys, got, tc.want)
		}
	}
}

func TestSortIsAnUndoableEdit(t *testing.T) {
	setupEditTable(t)
	press(t, "S")
	if got := joined(column(b, 0)); got != "h1 a4 a3 a2 a1" {
		t.Fatalf("S: %q", got)
	}
	if editSummary() != "sorted by h1 descending" || !dirty() {
		t.Errorf("summary = %q", editSummary())
	}
	press(t, "j d d") // remove a3 after the sort, then undo both
	if got := joined(column(b, 0)); got != "h1 a4 a2 a1" {
		t.Fatalf("dd after sort: %q", got)
	}
	press(t, "u u")
	if got := joined(column(b, 0)); got != "h1 a1 a2 a3 a4" || dirty() {
		t.Errorf("undo must restore the row and then the order: %q", got)
	}
}

func TestEditsWaitForTheLoad(t *testing.T) {
	setupEditTable(t)
	loadProgress.IsComplete.Store(false)
	for _, k := range []string{"d d", "x", "X", "s", "l d l"} {
		press(t, k)
		if dirty() || b.rowLen != 5 || b.colLen != 4 || statusMessage != "Still loading; wait before editing" {
			t.Errorf("%q during load: dirty=%v status %q", k, dirty(), statusMessage)
		}
	}
	if pendingOp != "" {
		t.Error("operator left pending")
	}
}

func TestEditSummaryWording(t *testing.T) {
	setupEditTable(t)
	if editSummary() != "" {
		t.Errorf("clean table: %q", editSummary())
	}
	edits = []edit{
		{rows: []removedRow{{1, nil}, {2, nil}, {3, nil}}},
		{names: []string{"Age"}, cols: make([]removedCol, 1)},
		{cells: []cellChange{{nil, 0, "x"}, {nil, 1, "y"}}},
	}
	if got := editSummary(); got != "1 column (Age) and 3 rows removed, 2 cells changed" {
		t.Errorf("summary = %q", got)
	}
	edits = []edit{{names: []string{"a", "b", "c", "d", "e"}, cols: make([]removedCol, 5)}}
	if got := editSummary(); got != "5 columns (a, b, c, ...) removed" {
		t.Errorf("long column list: %q", got)
	}
	if plural(1, "row") != "1 row" || plural(0, "cell") != "0 cells" {
		t.Error("plural")
	}
}

func TestYankAndPasteCells(t *testing.T) {
	setupEditTable(t)
	t.Cleanup(func() { tableRegister, lineRegister, cellEdit = nil, nil, nil })
	stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)

	press(t, "y j p") // a1 over a2
	if b.cont[2][0] != "a1" || editSummary() != "1 cell changed" || !strings.Contains(statusMessage, "Pasted 1 cell") {
		t.Fatalf("p: %q summary %q status %q", b.cont[2][0], editSummary(), statusMessage)
	}
	if string(lineRegister) != "a1" {
		t.Errorf("a single-cell yank also feeds the editor register: %q", string(lineRegister))
	}
	press(t, "l v j P") // fill (2,1) and (3,1) with the single value
	if b.cont[2][1] != "a1" || b.cont[3][1] != "a1" || b.cont[4][1] != "b4" {
		t.Errorf("visual paste of one value fills the selection: %v", column(b, 1))
	}
	press(t, "u u")
	if b.cont[2][0] != "a2" || b.cont[3][1] != "b3" || dirty() {
		t.Error("undo restores pasted cells")
	}

	// A block pastes from the cursor and is clipped at the table's edge.
	setupEditTable(t)
	press(t, "v j l y")         // a1 b1 / a2 b2; the cursor stays at (2,1)
	press(t, "g g 0 2 j 2 l p") // top-left (3,2)
	if b.cont[3][2] != "a1" || b.cont[3][3] != "b1" || b.cont[4][2] != "a2" || b.cont[4][3] != "b2" {
		t.Errorf("block paste: %v %v", b.cont[3], b.cont[4])
	}
	press(t, "u G 0 2 l p") // at (4,2) only one row is left: two cells change
	if b.cont[4][2] != "a1" || b.cont[4][3] != "b1" || !strings.Contains(statusMessage, "Pasted 2 cells") {
		t.Errorf("clipped block paste: %v status %q", b.cont[4], statusMessage)
	}

	// Y yanks the row; P lays it over another row.
	setupEditTable(t)
	press(t, "Y 2 j P")
	if got := strings.Join(b.cont[3], " "); got != "a1 b1 c1 d1" {
		t.Errorf("row paste: %q", got)
	}

	// X and d fill the register like vim; p puts the removed row elsewhere.
	setupEditTable(t)
	press(t, "X j p") // cut a1's row (cursor lands on a2), paste over a3
	if got := strings.Join(b.cont[2], " "); got != "a1 b1 c1 d1" || b.rowLen != 4 {
		t.Errorf("paste after cut: %q rows %d", got, b.rowLen)
	}
	setupEditTable(t)
	press(t, "d d G p") // dd fills the register too
	if got := strings.Join(b.cont[3], " "); got != "a1 b1 c1 d1" {
		t.Errorf("paste after dd: %q", got)
	}

	// x does not touch the register, so the yank survives clearing.
	setupEditTable(t)
	press(t, "y l x l p")
	if b.cont[1][2] != "a1" || b.cont[1][1] != "" {
		t.Errorf("x must not overwrite the register: %v", b.cont[1])
	}

	// A column removal pastes its data cells downwards, without the header.
	setupEditTable(t)
	press(t, "l v X g g 0 l l p") // cut column h2 (visual block X), paste over what is now the third column (h4)
	if b.cont[1][2] != "b1" || b.cont[4][2] != "b4" || b.cont[0][2] != "h4" {
		t.Errorf("column cut then paste: %v", column(b, 2))
	}

	// The editor's register is pasted when nothing was yanked from the table.
	setupEditTable(t)
	tableRegister, lineRegister = nil, nil
	press(t, "E y y esc j p")
	if b.cont[2][0] != "a1" {
		t.Errorf("editor yank pasted into a cell: %q", b.cont[2][0])
	}
	press(t, "u")
	tableRegister, lineRegister = nil, nil
	press(t, "p")
	if statusMessage != "Nothing to paste" || dirty() {
		t.Errorf("empty register: %q", statusMessage)
	}
	if got := keys.keysFor(actPaste); got != "p, P" {
		t.Errorf("paste keys = %q", got)
	}
}
