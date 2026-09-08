package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// setupVisualTable installs a 5x4 table (header + 4 rows) as the UI globals.
func setupVisualTable(t *testing.T) {
	t.Helper()
	data := [][]string{{"h1", "h2", "h3", "h4"}}
	for r := 1; r <= 4; r++ {
		data = append(data, []string{"a" + I2S(r), "b" + I2S(r), "c" + I2S(r), "d" + I2S(r)})
	}
	buf, err := createNewBufferWithData(data, true)
	if err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1
	oldB, oldTable, oldPage, oldView, oldKeys := b, bufferTable, mainPage, mainView, keys
	t.Cleanup(func() {
		b, bufferTable, mainPage, mainView, keys = oldB, oldTable, oldPage, oldView, oldKeys
		visual, pendingCount, pendingChord = visualOff, 0, nil
	})
	b = buf
	mainPage, mainView = nil, nil
	keys = defaultKeymap()
	bufferTable = tview.NewTable().SetSelectable(true, true).SetFixed(1, 1)
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 0)
	visual = visualOff
}

func press(t *testing.T, spec string) {
	t.Helper()
	for _, k := range strings.Fields(spec) {
		stroke, err := parseStroke(k)
		if err != nil {
			t.Fatal(err)
		}
		if stroke.key == tcell.KeyRune {
			handleTableKey(tcell.NewEventKey(tcell.KeyRune, stroke.ch, tcell.ModNone))
		} else {
			handleTableKey(tcell.NewEventKey(stroke.key, 0, tcell.ModNone))
		}
	}
}

func TestVisualBlockSelectionAndYank(t *testing.T) {
	setupVisualTable(t)
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)

	press(t, "j l v") // anchor at (2,1)
	press(t, "j l")   // cursor at (3,2)
	if visual != visualBlock {
		t.Fatalf("visual = %v, want block", visual)
	}
	if r1, c1, r2, c2 := visualRect(); r1 != 2 || c1 != 1 || r2 != 3 || c2 != 2 {
		t.Fatalf("rect = %d,%d..%d,%d", r1, c1, r2, c2)
	}
	if !inVisual(2, 2) || inVisual(1, 1) || inVisual(2, 3) {
		t.Error("inVisual boundaries wrong")
	}
	if got := visualStatus(); !strings.Contains(got, "2 rows x 2 columns") {
		t.Errorf("status = %q", got)
	}

	press(t, "o") // swap: cursor goes back to the anchor
	if row, col := bufferTable.GetSelection(); row != 2 || col != 1 {
		t.Errorf("after o cursor = %d,%d, want 2,1", row, col)
	}
	if r1, c1, r2, c2 := visualRect(); r1 != 2 || c1 != 1 || r2 != 3 || c2 != 2 {
		t.Errorf("swap must keep the same rectangle, got %d,%d..%d,%d", r1, c1, r2, c2)
	}

	press(t, "y")
	if visual != visualOff {
		t.Error("y must leave visual mode")
	}
	want := "b2\tc2\nb3\tc3"
	if len(*ran) != 1 || (*ran)[0] != "xclip:"+want {
		t.Errorf("yanked %v, want %q", *ran, want)
	}
}

func TestVisualRowsCountsAndCancel(t *testing.T) {
	setupVisualTable(t)
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)

	press(t, "V 2 j") // rows 1..3, all columns
	if visual != visualRows {
		t.Fatalf("visual = %v, want rows", visual)
	}
	if r1, c1, r2, c2 := visualRect(); r1 != 1 || c1 != 0 || r2 != 3 || c2 != 3 {
		t.Fatalf("rect = %d,%d..%d,%d", r1, c1, r2, c2)
	}
	press(t, "v") // switch kind keeps the anchor
	if visual != visualBlock {
		t.Errorf("v in line mode should switch to block mode, got %v", visual)
	}
	press(t, "v") // same key again leaves
	if visual != visualOff {
		t.Errorf("pressing v twice must leave visual mode, got %v", visual)
	}

	press(t, "V esc")
	if visual != visualOff {
		t.Error("Esc must cancel visual mode")
	}
	press(t, "v j q") // q leaves visual mode instead of quitting (app is nil here, so quitting would panic)
	if visual != visualOff {
		t.Error("q must cancel visual mode")
	}
	if row, _ := bufferTable.GetSelection(); row != 4 {
		t.Errorf("q must keep the cursor where the motion left it (row 4), got row %d", row)
	}
	press(t, "V j t") // a non-motion command (toggle type) leaves visual mode first
	if visual != visualOff {
		t.Error("a non-motion command must leave visual mode")
	}
	if len(*ran) != 0 {
		t.Errorf("nothing should have been yanked, got %v", *ran)
	}

	press(t, "g g V j Y")
	if got := (*ran)[len(*ran)-1]; got != "xclip:a1\tb1\tc1\td1\na2\tb2\tc2\td2" {
		t.Errorf("Y in visual yanked %q", got)
	}
}

func TestSingleYanks(t *testing.T) {
	setupVisualTable(t)
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	press(t, "j l y")
	press(t, "Y")
	if len(*ran) != 2 || (*ran)[0] != "xclip:b2" || (*ran)[1] != "xclip:a2\tb2\tc2\td2" {
		t.Errorf("yanks = %v", *ran)
	}
}

func TestUnboundKeysNeverReachTview(t *testing.T) {
	setupVisualTable(t)
	a, _ := parseChord("a")
	keys.set(actMoveLeft, [][]keyStroke{a}) // Left is no longer bound
	keys.set(actCancel, nil)                // Esc is unbound; it used to quit via tview's done func

	bufferTable.Select(2, 2)
	for _, k := range []tcell.Key{tcell.KeyLeft, tcell.KeyEscape, tcell.KeyEnter, tcell.KeyTab, tcell.KeyF5} {
		if ev := handleTableKey(tcell.NewEventKey(k, 0, tcell.ModNone)); ev != nil {
			t.Errorf("unbound key %v must be swallowed, was passed to tview", k)
		}
	}
	if row, col := bufferTable.GetSelection(); row != 2 || col != 2 {
		t.Errorf("unbound keys must not move the cursor, now at %d,%d", row, col)
	}
	press(t, "a")
	if _, col := bufferTable.GetSelection(); col != 1 {
		t.Errorf("the remapped key must move left, col = %d", col)
	}
}

func TestPagingActions(t *testing.T) {
	setupVisualTable(t)
	bufferTable.SetRect(0, 0, 40, 3) // header plus two visible rows
	press(t, "pgdn")
	if row, _ := bufferTable.GetSelection(); row != 3 {
		t.Errorf("PgDn should move a page (2 rows) from row 1, got %d", row)
	}
	press(t, "end")
	if row, _ := bufferTable.GetSelection(); row != 4 {
		t.Errorf("End should go to the last row, got %d", row)
	}
	press(t, "home")
	if row, _ := bufferTable.GetSelection(); row != 1 {
		t.Errorf("Home should go to the first data row, got %d", row)
	}
}

// The keymap refactor rewrote these handlers; drive them through the key
// handler exactly as a user would.
func TestRewrittenHandlersThroughKeys(t *testing.T) {
	setupVisualTable(t)
	oldQuery, oldResults, oldIdx, oldWrapped := searchQuery, searchResults, currentSearchIndex, wrappedColumns
	t.Cleanup(func() {
		searchQuery, currentSearchIndex, wrappedColumns = oldQuery, oldIdx, oldWrapped
		setSearchResults(oldResults)
	})
	wrappedColumns = map[int]int{}

	// counts and column motions
	press(t, "3 j")
	if row, _ := bufferTable.GetSelection(); row != 4 {
		t.Errorf("3j from row 1 should reach row 4, got %d", row)
	}
	press(t, "$")
	if _, col := bufferTable.GetSelection(); col != 3 {
		t.Errorf("$ should go to the last column, got %d", col)
	}
	press(t, "0")
	if _, col := bufferTable.GetSelection(); col != 0 {
		t.Errorf("0 should go to the first column, got %d", col)
	}
	press(t, "2 k")
	if row, _ := bufferTable.GetSelection(); row != 2 {
		t.Errorf("2k from row 4 should reach row 2, got %d", row)
	}

	// search navigation with wrap-around and counts
	searchQuery = "x"
	setSearchResults([]SearchResult{{Row: 1, Col: 1}, {Row: 2, Col: 2}, {Row: 4, Col: 3}})
	currentSearchIndex = 0
	press(t, "n")
	if r, c := bufferTable.GetSelection(); currentSearchIndex != 1 || r != 2 || c != 2 {
		t.Errorf("n should select the second match, got index %d at %d,%d", currentSearchIndex, r, c)
	}
	press(t, "2 n") // wraps past the end back to the first match
	if r, c := bufferTable.GetSelection(); currentSearchIndex != 0 || r != 1 || c != 1 {
		t.Errorf("2n should wrap to the first match, got index %d at %d,%d", currentSearchIndex, r, c)
	}
	press(t, "N")
	if r, c := bufferTable.GetSelection(); currentSearchIndex != 2 || r != 4 || c != 3 {
		t.Errorf("N from the first match should wrap to the last, got index %d at %d,%d", currentSearchIndex, r, c)
	}
	press(t, "esc")
	if searchQuery != "" || len(searchResults) != 0 {
		t.Error("Esc must clear the search")
	}

	// width limit toggle on the current column (the search left the cursor in
	// the last column, so l wraps around to column 0)
	press(t, "l")
	_, col := bufferTable.GetSelection()
	if col != 0 {
		t.Fatalf("l from the last column should wrap to column 0, got %d", col)
	}
	press(t, "_")
	if _, limited := wrappedColumns[col]; !limited {
		t.Error("_ must add a width limit on the current column")
	}
	press(t, "_")
	if _, limited := wrappedColumns[col]; limited {
		t.Error("_ again must remove the width limit")
	}

	// type toggle cycles String -> Number -> Date -> String
	if got := b.getColType(col); got != colTypeStr {
		t.Fatalf("precondition: column %d should be Str, got %s", col, type2name(got))
	}
	press(t, "t")
	if got := b.getColType(col); got != colTypeFloat {
		t.Errorf("t should switch to Number, got %s", type2name(got))
	}
	press(t, "t t")
	if got := b.getColType(col); got != colTypeStr {
		t.Errorf("t twice more should return to String, got %s", type2name(got))
	}
}

func TestApplyActiveFiltersRunsValueFiltersBeforeUnique(t *testing.T) {
	data := [][]string{{"k", "v"}, {"a", "v1"}, {"bx", "v1"}, {"cx", "v2"}, {"dx", "v2"}}
	base, _ := createNewBufferWithData(data, true)
	base.rowFreeze = 1
	old := activeFilters
	defer func() { activeFilters = old }()
	activeFilters = map[int]FilterOptions{
		1: {Operator: opUnique},
		0: {Query: "x", Operator: "contains"},
	}
	got := applyActiveFilters(base)
	// unique must see only the rows containing "x": bx/v1 and cx/v2 survive.
	if got.rowLen != 3 || got.cont[1][0] != "bx" || got.cont[2][0] != "cx" {
		t.Errorf("expected bx and cx, got %v", got.cont[1:])
	}
	if describeFilter(FilterOptions{Operator: opUnique}) != "unique" || describeFilter(FilterOptions{Operator: "equals", Query: "q"}) != `equals "q"` {
		t.Error("describeFilter output unexpected")
	}
}
