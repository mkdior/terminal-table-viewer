package app

import (
	"strings"
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

// pagesClick sends a mouse action through the page container, as the
// application does, and reports whether it was consumed.
func pagesClick(action tview.MouseAction, x, y int, buttons tcell.ButtonMask) bool {
	consumed, _ := UI.MouseHandler()(action, mouse(x, y, buttons), func(tview.Primitive) {})
	return consumed
}

func TestMouseOnTheTabLineAndFooter(t *testing.T) {
	names, _ := setupTabs(t, csvRows("A", 2), csvRows("B", 2), csvRows("C", 2))
	stops := stubStop(t)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 10)
	UI.SetRect(0, 0, 80, 10)
	// The container sizes the page in front when it draws, as between events.
	draw := func() {
		UI.Draw(screen)
		screen.Show()
	}
	draw()
	// Labels are " N tN.csv ", ten cells each, a space apart: tab 2 spans 11..20.
	if !pagesClick(tview.MouseLeftClick, 12, 0, tcell.ButtonPrimary) || current != 1 || args.FileName != names[1] {
		t.Fatalf("a click on the second label shows it: current %d", current)
	}
	draw()
	if !pagesClick(tview.MouseScrollDown, 40, 0, tcell.WheelDown) || current != 2 {
		t.Errorf("the wheel over the tab line steps: current %d", current)
	}
	draw()
	if !pagesClick(tview.MouseScrollUp, 40, 0, tcell.WheelUp) || current != 1 {
		t.Errorf("and back: current %d", current)
	}
	draw()
	if !pagesClick(tview.MouseLeftClick, 70, 0, tcell.ButtonPrimary) || current != 1 {
		t.Errorf("a click on the empty end of the line does nothing: current %d", current)
	}
	if !pagesClick(tview.MouseMiddleClick, 23, 0, tcell.ButtonMiddle) || len(tabs) != 2 || current != 1 || *stops != 0 {
		t.Errorf("a middle click closes the third tab: tabs %d current %d stops %d", len(tabs), current, *stops)
	}
	draw()
	// The footer's "? help" opens the help; the rest of the footer is inert.
	end := len(fileNameStr)
	if !pagesClick(tview.MouseLeftClick, end-3, 9, tcell.ButtonPrimary) || !UI.HasPage("helpDialog") {
		t.Errorf("a click on ? help opens the help: %v", UI.GetPageNames(true))
	}
	UI.RemovePage("helpDialog")
	pagesClick(tview.MouseLeftClick, 40, 9, tcell.ButtonPrimary) // tview's frame takes it; nothing happens
	if r, c := bufferTable.GetSelection(); UI.HasPage("helpDialog") || r != 1 || c != 0 {
		t.Errorf("a click elsewhere on the footer does nothing: help %v selection %d,%d", UI.HasPage("helpDialog"), r, c)
	}
	// A click on a table cell is not the frame's business: it reaches the table.
	draw()
	x, y := cellPos(t, 2, 1)
	pagesClick(tview.MouseLeftClick, x, y, tcell.ButtonPrimary)
	if r, c := bufferTable.GetSelection(); r != 2 || c != 1 {
		t.Errorf("the click reached the table: selection %d,%d", r, c)
	}
}

func TestDragSelectsABlockAndCopiesItOnRelease(t *testing.T) {
	screen := smallUI(t)
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	oldCopy := clipboardCopyOnSelect
	t.Cleanup(func() {
		clipboardCopyOnSelect, visual, mouseDrag.pressed, mouseDrag.dragging = oldCopy, visualOff, false, false
	})
	clipboardCopyOnSelect = true
	x1, y1 := cellPos(t, 1, 0)
	x2, y2 := cellPos(t, 3, 2)

	// Press on a1, move to c3 with the button held, release.
	if _, ev := handleTableMouse(tview.MouseLeftDown, mouse(x1, y1, tcell.ButtonPrimary)); ev == nil || !mouseDrag.pressed || mouseDrag.dragging {
		t.Fatalf("the press is noted and passed on: %+v", mouseDrag)
	}
	act, ev := handleTableMouse(tview.MouseMove, mouse(x2, y2, tcell.ButtonPrimary))
	if ev != nil || act != tview.MouseConsumed || visual != visualBlock {
		t.Fatalf("a move with the button held starts the selection: visual %v", visual)
	}
	if r1, c1, r2, c2 := visualRect(); r1 != 1 || c1 != 0 || r2 != 3 || c2 != 2 {
		t.Errorf("selection %d,%d..%d,%d, want 1,0..3,2", r1, c1, r2, c2)
	}
	if !strings.Contains(statusMessage, "3 rows x 3 columns") {
		t.Errorf("the footer follows the drag: %q", statusMessage)
	}
	act, ev = handleTableMouse(tview.MouseLeftUp, mouse(x2, y2, tcell.ButtonNone))
	if ev != nil || act != tview.MouseConsumed || mouseDrag.pressed || mouseDrag.dragging {
		t.Fatalf("the release ends the drag: %+v", mouseDrag)
	}
	if len(*ran) != 1 || (*ran)[0] != "xclip:a1\tb1\tc1\na2\tb2\tc2\na3\tb3\tc3" {
		t.Errorf("the block is copied on release: %q", *ran)
	}
	if visual != visualBlock || !strings.HasPrefix(statusMessage, "Yanked 3 rows x 3 columns") {
		t.Errorf("the selection stays with the yank reported: visual %v status %q", visual, statusMessage)
	}
	press(t, "y") // the standing selection still takes the keys
	if len(*ran) != 2 || (*ran)[1] != (*ran)[0] || visual != visualOff {
		t.Errorf("y copies it again and leaves visual mode: %d copies, visual %v", len(*ran), visual)
	}

	// A move without the button is a hover; a drag that left the table extends
	// a step past the cursor; a click clears the standing selection.
	handleTableMouse(tview.MouseLeftDown, mouse(x1, y1, tcell.ButtonPrimary))
	if _, ev := handleTableMouse(tview.MouseMove, mouse(x2, y2, tcell.ButtonNone)); ev == nil || visual != visualOff {
		t.Error("a move with no button held selects nothing")
	}
	handleTableMouse(tview.MouseMove, mouse(x2, 12, tcell.ButtonPrimary)) // below the rows
	if r, _ := bufferTable.GetSelection(); visual != visualBlock || r != 2 {
		t.Errorf("a drag below the table steps a row down from the anchor each move: visual %v row %d", visual, r)
	}
	handleTableMouse(tview.MouseMove, mouse(x2, 12, tcell.ButtonPrimary))
	if r, c := bufferTable.GetSelection(); r != 3 || c != 2 {
		t.Errorf("and again: %d,%d", r, c)
	}
	handleTableMouse(tview.MouseLeftUp, mouse(x2, 12, tcell.ButtonNone))
	if len(*ran) != 3 {
		t.Errorf("copied again on release: %d", len(*ran))
	}
	handleTableMouse(tview.MouseLeftDown, mouse(x1, y1, tcell.ButtonPrimary))
	handleTableMouse(tview.MouseLeftUp, mouse(x1, y1, tcell.ButtonNone))
	if _, ev := handleTableMouse(tview.MouseLeftClick, mouse(x1, y1, tcell.ButtonNone)); ev == nil || visual != visualOff {
		t.Errorf("a click clears the selection and reaches the table: visual %v", visual)
	}
	if len(*ran) != 3 {
		t.Errorf("a click copies nothing: %d", len(*ran))
	}

	// With copy_on_select off the selection waits for y.
	clipboardCopyOnSelect = false
	handleTableMouse(tview.MouseLeftDown, mouse(x1, y1, tcell.ButtonPrimary))
	handleTableMouse(tview.MouseMove, mouse(x2, y2, tcell.ButtonPrimary))
	handleTableMouse(tview.MouseLeftUp, mouse(x2, y2, tcell.ButtonNone))
	if len(*ran) != 3 || visual != visualBlock || !strings.Contains(statusMessage, "3 rows x 3 columns") {
		t.Errorf("no copy on release: %d copies, visual %v, status %q", len(*ran), visual, statusMessage)
	}
	press(t, "esc")
	if visual != visualOff {
		t.Error("Esc clears the selection")
	}
	_ = screen
}

func TestRightAndMiddleClickAndSidewaysWheel(t *testing.T) {
	screen := smallUI(t)
	stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	t.Cleanup(func() { visual, tableRegister, lineRegister = visualOff, nil, nil })
	// Edits re-derive the view, so the cells are drawn again before each hit test.
	draw := func() {
		mainView.Draw(screen)
		screen.Show()
	}
	x, y := cellPos(t, 2, 1)
	press(t, "y") // a1 into the register
	act, ev := handleTableMouse(tview.MouseRightClick, mouse(x, y, tcell.ButtonSecondary))
	if ev != nil || act != tview.MouseConsumed || b.cont[2][1] != "a1" || !strings.Contains(statusMessage, "Pasted 1 cell") {
		t.Errorf("a right click without a selection pastes over the cell: %v status %q", b.cont[2], statusMessage)
	}
	press(t, "u")
	draw()
	x3, y3 := cellPos(t, 3, 2)
	handleTableMouse(tview.MouseMiddleClick, mouse(x3, y3, tcell.ButtonMiddle))
	if b.cont[3][2] != "a1" {
		t.Errorf("a middle click pastes: %v", b.cont[3])
	}
	press(t, "u")
	draw()
	x, y = cellPos(t, 2, 1)
	press(t, "g g 0 v j") // a standing selection
	handleTableMouse(tview.MouseRightClick, mouse(x, y, tcell.ButtonSecondary))
	if visual != visualOff || b.cont[2][1] != "b2" {
		t.Errorf("a right click with a selection clears it and pastes nothing: visual %v cell %q", visual, b.cont[2][1])
	}
	bufferTable.Select(1, 1)
	handleTableMouse(tview.MouseScrollRight, mouse(x, y, tcell.WheelRight))
	if _, c := bufferTable.GetSelection(); c != 2 {
		t.Errorf("the wheel sideways moves a column: %d", c)
	}
	handleTableMouse(tview.MouseScrollLeft, mouse(x, y, tcell.WheelLeft))
	handleTableMouse(tview.MouseScrollLeft, mouse(x, y, tcell.WheelLeft))
	handleTableMouse(tview.MouseScrollLeft, mouse(x, y, tcell.WheelLeft))
	if _, c := bufferTable.GetSelection(); c != 0 {
		t.Errorf("and stops at the first column: %d", c)
	}
}

func TestDoubleClickEditsAndClickOpensAFold(t *testing.T) {
	screen := smallUI(t)
	t.Cleanup(func() { cellEdit = nil; hiddenCols = map[int]bool{} })
	x, y := cellPos(t, 2, 1)
	act, ev := handleTableMouse(tview.MouseLeftDoubleClick, mouse(x, y, tcell.ButtonPrimary))
	if ev != nil || act != tview.MouseConsumed || cellEdit == nil || cellEdit.row != 2 || cellEdit.col != 1 {
		t.Fatalf("a double click opens the editor on the cell: editor %+v", cellEdit)
	}
	press(t, "esc")
	hiddenCols = map[int]bool{1: true}
	drawBuffer(b, bufferTable)
	mainView.Draw(screen)
	x, y = cellPos(t, 2, 1)
	if _, ev := handleTableMouse(tview.MouseLeftClick, mouse(x, y, tcell.ButtonPrimary)); ev == nil || hiddenCols[1] {
		t.Errorf("a click on the fold marker opens the column and still selects the cell: hidden %v", hiddenCols)
	}
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
