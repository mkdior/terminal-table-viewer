package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// smallUI builds the main page over the 5x4 editing table on a 60x20
// simulation screen, so the table (a header and four rows) leaves a blank
// area below it, and draws one frame so every cell has a screen position.
func smallUI(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	setupEditTable(t)
	bufferTable = tview.NewTable().SetSelectable(true, true).SetFixed(1, 1)
	bufferTable.SetSelectedStyle(theme.selectedStyle())
	bufferTable.SetSelectionChangedFunc(selectionChanged)
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 0)
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
	screen.SetSize(60, 20)
	mainView.SetRect(0, 0, 60, 20)
	mainView.Draw(screen)
	screen.Show()
	return screen
}

// cellPos returns the screen position of a drawn cell.
func cellPos(t *testing.T, row, col int) (x, y int) {
	t.Helper()
	cell := currentContent.drawnCell(row, col)
	if cell == nil {
		t.Fatalf("cell %d,%d was not drawn", row, col)
	}
	x, y, w := cell.GetLastPosition()
	if w == 0 {
		t.Fatalf("cell %d,%d has no position", row, col)
	}
	return x, y
}

// mouse builds a mouse event at x, y with the given buttons held.
func mouse(x, y int, buttons tcell.ButtonMask) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, buttons, tcell.ModNone)
}

func TestClicksBesideTheTableAreSwallowed(t *testing.T) {
	smallUI(t)
	x, y := cellPos(t, 2, 1)
	if r, c, ok := cellUnderPointer(x, y); !ok || r != 2 || c != 1 {
		t.Fatalf("cellUnderPointer(%d,%d) = %d,%d %v", x, y, r, c, ok)
	}
	if _, _, ok := cellUnderPointer(x, 12); ok {
		t.Error("the blank area below the rows has no cell")
	}
	for _, tc := range []struct {
		name   string
		x, y   int
		action tview.MouseAction
	}{
		{"blank area below the table", x, 12, tview.MouseLeftClick},
		{"press in the blank area", x, 12, tview.MouseLeftDown},
		{"double click in the blank area", x, 12, tview.MouseLeftDoubleClick},
		{"the frozen header", x, 0, tview.MouseLeftClick},
	} {
		userMovedCursor = false
		act, ev := handleTableMouse(tc.action, mouse(tc.x, tc.y, tcell.ButtonPrimary))
		if ev != nil || act != tview.MouseConsumed || userMovedCursor {
			t.Errorf("%s: action %v event %v moved %v; the click must be swallowed", tc.name, act, ev, userMovedCursor)
		}
	}
	if r, c := bufferTable.GetSelection(); r != 1 || c != 0 {
		t.Errorf("selection moved to %d,%d", r, c)
	}
	// A click on a data cell still reaches tview and counts as moving the cursor.
	act, ev := handleTableMouse(tview.MouseLeftClick, mouse(x, y, tcell.ButtonPrimary))
	if ev == nil || act != tview.MouseLeftClick || !userMovedCursor {
		t.Errorf("a cell click must pass: action %v event %v moved %v", act, ev, userMovedCursor)
	}
	// While a cell is being edited nothing reaches the table at all.
	press(t, "E")
	if _, ev := handleTableMouse(tview.MouseLeftClick, mouse(x, y, tcell.ButtonPrimary)); ev != nil {
		t.Error("clicks are consumed while editing")
	}
	press(t, "esc")
}
