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
