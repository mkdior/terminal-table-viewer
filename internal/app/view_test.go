package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// wideUI builds the main page the way drawUI does over a table of 12 wide
// columns and 40 rows, on a 60x12 simulation screen, so only six columns and
// ten rows fit at a time.
func wideUI(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	data := [][]string{{}}
	for c := 0; c < 12; c++ {
		data[0] = append(data[0], "header"+I2S(c))
	}
	for r := 1; r <= 40; r++ {
		row := []string{}
		for c := 0; c < 12; c++ {
			row = append(row, "r"+I2S(r)+"c"+I2S(c)+"xxxx")
		}
		data = append(data, row)
	}
	buf, err := createNewBufferWithData(data, true)
	if err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1
	setupEditTable(t)
	b = buf
	bufferTable = tview.NewTable().SetSelectable(true, true).SetFixed(1, 1)
	bufferTable.SetSelectedStyle(theme.selectedStyle())
	drawBuffer(b, bufferTable)
	bufferTable.Focus(func(tview.Primitive) {})
	oldPage, oldView := mainPage, mainView
	t.Cleanup(func() { mainPage, mainView = oldPage, oldView })
	mainPage = tview.NewFrame(bufferTable).SetBorders(0, 0, 0, 0, 0, 0)
	mainView = newCellPreview(mainPage)
	fileNameStr, cursorPosStr = footerFileName(), buildCursorPosStr(1, 0)
	drawFooterText(fileNameStr, "All Done", cursorPosStr)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(60, 12)
	mainView.SetRect(0, 0, 60, 12)
	return screen
}

func TestTypingACountKeepsTheFooterStill(t *testing.T) {
	screen := wideUI(t)
	screen.SetSize(100, 12) // a realistic width: left text, status and position all fit
	mainView.SetRect(0, 0, 100, 12)
	bufferTable.Select(1, 0)
	draw := func() []string {
		mainView.Draw(screen)
		screen.Show()
		return strings.Split(screenText(screen), "\n")
	}
	before := draw()
	press(t, "1 2")
	after := draw()
	t.Cleanup(func() { pendingCount = 0 })
	footer := len(before) - 2 // the last screen row (the split leaves a trailing empty string)
	if x, y := strings.Index(before[footer], "Column Type"), strings.Index(after[footer], "Column Type"); x < 0 || x != y {
		t.Errorf("the cursor position must not move when a count is typed: %d -> %d\n%q\n%q", x, y, before[footer], after[footer])
	}
	if !strings.HasSuffix(strings.TrimRight(after[footer], " "), "1,0  12") {
		t.Errorf("the count shows in the fixed showcmd slot: %q", after[footer])
	}
	if !strings.Contains(before[footer], "All Done") || !strings.Contains(after[footer], "All Done") {
		t.Errorf("the status must stay visible: %q", after[footer])
	}
	for i := 0; i < footer; i++ {
		if before[i] != after[i] {
			t.Errorf("table row %d moved:\n%q\n%q", i, before[i], after[i])
		}
	}
	press(t, "j") // the count is consumed and the slot empties
	if line := draw()[footer]; strings.Contains(line, "  12") {
		t.Errorf("the slot must clear after the motion: %q", line)
	}
}

func TestVerticalMotionsKeepTheHorizontalScroll(t *testing.T) {
	screen := wideUI(t)
	draw := func() (int, int) {
		mainView.Draw(screen)
		screen.Show()
		return bufferTable.GetOffset()
	}
	// Scroll so columns 5..10 are on screen, with the cursor on column 6.
	bufferTable.SetOffset(0, 4)
	bufferTable.Select(1, 6)
	if _, col := draw(); col != 4 {
		t.Fatalf("precondition: column offset %d, want 4", col)
	}
	for _, keys := range []string{"G", "g g", "2 0 0 j", "ctrl+d", "ctrl+u", "pgdn", "1 0 G", "k"} {
		press(t, keys)
		row, col := draw()
		if _, c := bufferTable.GetSelection(); c != 6 || col != 4 {
			t.Errorf("%q: cursor column %d, column offset %d, want 6 and 4 (row offset %d)", keys, c, col, row)
		}
	}
	if r, _ := bufferTable.GetSelection(); r != 9 {
		t.Errorf("10G then k lands on row 9, got %d", r)
	}
	press(t, "G")
	row, _ := draw()
	if row != 30 {
		t.Errorf("G must scroll so the last row is on screen: row offset %d", row)
	}
	if out := screenText(screen); !strings.Contains(out, "r40c6xxxx") || !strings.Contains(out, "r40c9xxxx") || strings.Contains(out, "r40c1xxxx") {
		t.Errorf("the last row and the scrolled columns must be visible:\n%s", out)
	}
}

// The first column is frozen, so it is drawn whatever the horizontal scroll:
// nothing in tview or in pinColumnOffset moves the view when the cursor lands
// on it. A motion that reaches it must scroll back by itself, or 0 from the
// right edge would only move the cursor and the next l would snap the view.
func TestReachingTheFirstColumnScrollsTheViewBack(t *testing.T) {
	screen := wideUI(t)
	draw := func() int {
		mainView.Draw(screen)
		screen.Show()
		_, col := bufferTable.GetOffset()
		return col
	}
	for _, keys := range []string{"0", "^", "9 9 h", "9 9 b"} {
		bufferTable.Select(1, 11)
		if off := draw(); off == 0 {
			t.Fatalf("%q: the last column must scroll the view right first", keys)
		}
		press(t, keys)
		off := draw()
		_, col := bufferTable.GetSelection()
		if col != 0 || off != 0 {
			t.Errorf("%q: cursor column %d, column offset %d, want 0 and 0", keys, col, off)
		}
		if out := screenText(screen); !strings.Contains(out, "r1c1xxxx") {
			t.Errorf("%q: the second column must come back on screen:\n%s", keys, out)
		}
	}

	// A vertical motion with the cursor already in the frozen column keeps the
	// scroll, as every other vertical motion does.
	bufferTable.Select(1, 0)
	bufferTable.SetOffset(0, 4)
	press(t, "j")
	if off := draw(); off != 4 {
		t.Errorf("j in the frozen column must keep the horizontal scroll, offset %d", off)
	}
}

func TestCursorRowIsTinted(t *testing.T) {
	screen := wideUI(t)
	bufferTable.Select(2, 1)
	mainView.Draw(screen)
	screen.Show()
	cells, w, _ := screen.GetContents()
	out := strings.Split(screenText(screen), "\n")
	row := rowOf(screenText(screen), "r2c0xxxx")
	other := rowOf(screenText(screen), "r3c0xxxx")
	bgAt := func(y, x int) tcell.Color {
		_, bg, _ := cells[y*w+x].Style.Decompose()
		return bg
	}
	x3 := strings.Index(out[row], "r2c3xxxx") // a cell in the cursor's row, away from the cursor
	if x3 < 0 {
		t.Fatalf("layout:\n%s", screenText(screen))
	}
	if got := bgAt(row, x3); got != theme.CursorLine {
		t.Errorf("cells of the cursor's row are tinted: got %v, want %v", got, theme.CursorLine)
	}
	if got := bgAt(row, strings.Index(out[row], "r2c0xxxx")); got != theme.CursorLine {
		t.Errorf("the frozen column of the cursor's row is tinted too: %v", got)
	}
	if got := bgAt(row, strings.Index(out[row], "r2c1xxxx")); got != theme.Accent {
		t.Errorf("the selected cell keeps the cursor style: %v", got)
	}
	if got := bgAt(other, strings.Index(out[other], "r3c3xxxx")); got != theme.Background {
		t.Errorf("other rows keep the plain background: %v", got)
	}
	if got := bgAt(rowOf(screenText(screen), "header0"), strings.Index(out[rowOf(screenText(screen), "header0")], "header3")); got != theme.Panel {
		t.Errorf("the header keeps its panel background: %v", got)
	}
}

func TestHiddenColumnsFoldToAMarker(t *testing.T) {
	screen := wideUI(t)
	t.Cleanup(func() { hiddenCols = map[int]bool{} })
	hiddenCols = map[int]bool{}
	bufferTable.Select(1, 1)
	press(t, "z c")
	if !hiddenCols[1] || !strings.Contains(statusMessage, "Hid 1 column (header1)") || !strings.Contains(statusMessage, "zo shows") {
		t.Fatalf("zc: hidden %v status %q", hiddenCols, statusMessage)
	}
	mainView.Draw(screen)
	screen.Show()
	out := screenText(screen)
	header := strings.Split(out, "\n")[0]
	if strings.Contains(header, "header1") || !strings.Contains(header, foldMarker) {
		t.Errorf("a hidden column shows only its marker:\n%s", out)
	}
	if rowOf(out, "r1c1xxxx") != rowOf(out, "r1c0xxxx") && !strings.Contains(out, " header1 (hidden) ") {
		t.Errorf("the cursor on a hidden column previews its name and value:\n%s", out)
	}
	if mainView.text != "r1c1xxxx" || !strings.Contains(mainView.box.GetTitle(), "header1 (hidden)") {
		t.Errorf("preview text %q title %q", mainView.text, mainView.box.GetTitle())
	}
	press(t, "l")                                 // off the hidden column: no preview for a normal, short cell
	updateCellPreview(bufferTable.GetSelection()) // what the selection callback does in the app
	if mainView.text != "" {
		t.Errorf("preview must go when leaving the hidden column: %q", mainView.text)
	}
	press(t, "h")
	if !strings.Contains(header, "header2") || !strings.Contains(header, "header5") {
		t.Errorf("the freed space shows more columns:\n%s", out)
	}
	if !strings.HasPrefix(cursorPosStr, "hidden: header1") {
		t.Errorf("the footer names the hidden column under the cursor: %q", cursorPosStr)
	}
	press(t, "z o")
	if hiddenCols[1] || !strings.Contains(statusMessage, "Showing 1 column (header1)") {
		t.Errorf("zo: %v %q", hiddenCols, statusMessage)
	}
	press(t, "z a z a")
	if hiddenCols[1] {
		t.Error("za twice leaves the column visible")
	}
	press(t, "v l l z c") // three columns at once
	if len(hiddenCols) != 3 || visual != visualOff {
		t.Errorf("visual zc: %v", hiddenCols)
	}
	press(t, "z R")
	if len(hiddenCols) != 0 {
		t.Errorf("zR shows all: %v", hiddenCols)
	}
	press(t, "0 1 1 z c") // a count hides several; but never every column
	if len(hiddenCols) != 11 || hiddenCols[11] {
		t.Errorf("11zc from column 0: %v", hiddenCols)
	}
	press(t, "$ z c")
	if statusMessage != "Cannot hide every column" || !hiddenCols[0] {
		t.Errorf("the last visible column stays: %q", statusMessage)
	}
	press(t, "z R")

	// Hidden flags follow their columns through removals, insertions and undo.
	press(t, "0 l l z c") // hide column 2
	press(t, "0 l d l")   // remove column 1: the hidden one is now column 1
	if !hiddenCols[1] || len(hiddenCols) != 1 {
		t.Errorf("after removing a column to the left: %v", hiddenCols)
	}
	press(t, "u")
	if !hiddenCols[2] || len(hiddenCols) != 1 {
		t.Errorf("after undoing the removal: %v", hiddenCols)
	}
	press(t, "0 i c esc esc") // insert a column at 0: the hidden one moves to 3
	if !hiddenCols[3] || len(hiddenCols) != 1 {
		t.Errorf("after inserting a column: %v", hiddenCols)
	}
	press(t, "u")
	if !hiddenCols[2] {
		t.Errorf("after undoing the insertion: %v", hiddenCols)
	}

	// Editing a hidden cell opens the fold first.
	press(t, "0 l l E")
	if hiddenCols[2] || cellEdit == nil {
		t.Errorf("E on a hidden column opens it: hidden %v editor %v", hiddenCols, cellEdit)
	}
	press(t, "esc")
	if !dirty() == false {
		t.Error("hiding and showing are not edits")
	}
}

func TestColumnLayoutIsStableBetweenFrames(t *testing.T) {
	screen := wideUI(t) // twelve columns; six fit, with five spare cells for a cut one
	t.Cleanup(func() { hiddenCols = map[int]bool{} })
	hiddenCols = map[int]bool{}
	screen.SetSize(65, 12)
	mainView.SetRect(0, 0, 65, 12)
	header := func() string {
		mainView.Draw(screen)
		screen.Show()
		return strings.Split(screenText(screen), "\n")[0]
	}
	bufferTable.Select(1, 6)
	first := header()
	press(t, "l l l") // to column 9: the view scrolls right
	after := header()
	if !strings.Contains(after, "header9 ") {
		t.Fatalf("column 9 must be shown in full after moving onto it:\n%s", after)
	}
	if again := header(); again != after {
		t.Errorf("a redraw without a motion must not change the layout:\n%s\n%s", after, again)
	}
	press(t, "z") // starts a chord: only the footer changes
	if got := header(); got != after {
		t.Errorf("pressing z must not shift the columns:\n%s\n%s", after, got)
	}
	press(t, "c") // fold column 9: it becomes a marker, everything else stays put
	folded := header()
	upToH8 := after[:strings.Index(after, "header8")+len("header8")]
	if !strings.Contains(folded, foldMarker) || !strings.HasPrefix(folded, upToH8) || strings.Contains(folded, "header9") {
		t.Errorf("folding the selected column must keep the columns before it in place:\n%s\n%s", after, folded)
	}
	press(t, "z o")
	if got := header(); got != after {
		t.Errorf("unfolding restores the layout:\n%s\n%s", after, got)
	}
	_ = first
	// The selected column is never the cut one: moving back left keeps it whole.
	press(t, "h h")
	if got := header(); !strings.Contains(got, "header7 ") {
		t.Errorf("column 7 must be shown in full:\n%s", got)
	}
}
