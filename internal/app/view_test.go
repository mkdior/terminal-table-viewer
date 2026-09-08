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
