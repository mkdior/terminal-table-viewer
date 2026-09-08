package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestCellEditorThroughKeys(t *testing.T) {
	setupEditTable(t)
	t.Cleanup(func() { cellEdit, lineRegister, lineLastChange = nil, nil, nil })

	press(t, "E")
	if cellEdit == nil || cellEdit.row != 1 || cellEdit.col != 0 || cellEdit.ed.mode != editNormal {
		t.Fatalf("E must open the editor on the cell: %+v", cellEdit)
	}
	if !strings.Contains(statusMessage, "-- EDIT --") || !strings.HasPrefix(statusMessage, "h1") {
		t.Errorf("status %q", statusMessage)
	}
	press(t, "$ x enter") // a1 -> a
	if cellEdit != nil || b.cont[1][0] != "a" || editSummary() != "1 cell changed" {
		t.Errorf("Enter must apply: cell %q summary %q editor %v", b.cont[1][0], editSummary(), cellEdit)
	}
	if !strings.Contains(statusMessage, "Changed h1 at row 1") || !strings.Contains(fileNameStr, "[+]") {
		t.Errorf("status %q file %q", statusMessage, fileNameStr)
	}
	press(t, "u")
	if b.cont[1][0] != "a1" || dirty() {
		t.Errorf("u must restore the cell: %q", b.cont[1][0])
	}

	press(t, "i X")
	if cellEdit == nil || cellEdit.ed.mode != editInsert || !strings.Contains(statusMessage, "-- INSERT --") {
		t.Fatalf("i must open in insert mode: %q", statusMessage)
	}
	press(t, "enter")
	if b.cont[1][0] != "Xa1" {
		t.Errorf("i inserts at the start: %q", b.cont[1][0])
	}
	press(t, "a Y enter")
	if b.cont[1][0] != "Xa1Y" {
		t.Errorf("a appends at the end: %q", b.cont[1][0])
	}
	press(t, "c c n e w enter")
	if b.cont[1][0] != "new" {
		t.Errorf("cc replaces the value: %q", b.cont[1][0])
	}
	press(t, "3 u")
	if b.cont[1][0] != "a1" || dirty() {
		t.Errorf("three undos back to the start: %q", b.cont[1][0])
	}

	// Esc in normal mode cancels; an unchanged value records nothing.
	press(t, "E x esc")
	if cellEdit != nil || b.cont[1][0] != "a1" || dirty() || statusMessage != "Edit cancelled" {
		t.Errorf("Esc must cancel: %q dirty=%v %q", b.cont[1][0], dirty(), statusMessage)
	}
	press(t, "E enter")
	if dirty() {
		t.Error("applying the same value is not an edit")
	}

	// Digits and table keys go to the editor while it is open.
	press(t, "E 2 x enter")
	if b.cont[1][0] != "" || pendingCount != 0 {
		t.Errorf("2x inside the editor: %q", b.cont[1][0])
	}
	press(t, "u")

	// The register and . carry from cell to cell.
	press(t, "E x enter j E . enter") // remove the first character of a1, then of a2
	if b.cont[1][0] != "1" || b.cont[2][0] != "2" {
		t.Errorf(". across cells: %q %q", b.cont[1][0], b.cont[2][0])
	}
	press(t, "E $ p enter") // paste the deleted "a"
	if b.cont[2][0] != "2a" {
		t.Errorf("register across cells: %q", b.cont[2][0])
	}

	// A filtered view edits the shared row.
	press(t, "u u u")
	originalBuffer = b
	activeFilters[0] = FilterOptions{Query: "a3", Operator: "equals"}
	isFiltered = true
	b = applyActiveFilters(originalBuffer)
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 1)
	press(t, "c c z enter")
	if originalBuffer.cont[3][1] != "z" || b.cont[1][1] != "z" {
		t.Errorf("edit through a filtered view: base %q view %q", originalBuffer.cont[3][1], b.cont[1][1])
	}
	if originalBuffer.columnWidth(1) < 1 {
		t.Error("width tracked")
	}
}

func TestCellEditorWaitsForLoadAndKeepsVisualOut(t *testing.T) {
	setupEditTable(t)
	t.Cleanup(func() { cellEdit = nil })
	loadProgress.IsComplete.Store(false)
	press(t, "E")
	if cellEdit != nil || statusMessage != "Still loading; wait before editing" {
		t.Errorf("editing must wait for the load: %v %q", cellEdit, statusMessage)
	}
	loadProgress.IsComplete.Store(true)
	press(t, "V j i") // a non-motion leaves visual mode, then opens the editor on the cursor cell
	if visual != visualOff || cellEdit == nil || cellEdit.row != 2 || cellEdit.ed.mode != editInsert {
		t.Errorf("i from visual mode: visual=%v editor %+v", visual, cellEdit)
	}
	press(t, "esc esc")
	if cellEdit != nil {
		t.Error("Esc twice closes the editor")
	}
	if got := keys.keysFor(actStats); got != "I" {
		t.Errorf("stats moved to I, got %q", got)
	}
}

func TestCellEditorDrawsOverTheCell(t *testing.T) {
	setupEditTable(t)
	t.Cleanup(func() { cellEdit = nil })
	oldPage, oldView := mainPage, mainView
	t.Cleanup(func() { mainPage, mainView = oldPage, oldView })
	bufferTable.Focus(func(tview.Primitive) {})
	mainPage = tview.NewFrame(bufferTable)
	mainView = newCellPreview(mainPage)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(60, 10)
	mainView.SetRect(0, 0, 60, 10)
	mainView.Draw(screen) // lay the table out so the cell has a screen position
	screen.Show()

	bufferTable.Select(2, 1)
	press(t, "E A x y z") // b2 -> b2xyz, inserting
	mainView.Draw(screen)
	screen.Show()
	out := screenText(screen)
	row := rowOf(out, "b2xyz")
	if row < 0 || row != rowOf(out, "a2") {
		t.Fatalf("the edited text must be drawn on the cell's row:\n%s", out)
	}
	if !strings.Contains(out, "-- INSERT --") {
		t.Errorf("the footer must show the mode:\n%s", out)
	}
	cx, cy, visible := screen.GetCursor()
	if !visible || cy != row {
		t.Errorf("the terminal cursor must be shown on the edited row while inserting: %d,%d %v", cx, cy, visible)
	}
	line := strings.Split(out, "\n")[row]
	if x := strings.Index(line, "b2xyz"); x < 0 || cx != x+5 {
		t.Errorf("cursor x = %d, text starts at %d", cx, x)
	}

	press(t, "esc") // normal mode: the character under the cursor is drawn in reverse
	mainView.Draw(screen)
	screen.Show()
	cells, w, _ := screen.GetContents()
	x := strings.Index(strings.Split(screenText(screen), "\n")[row], "b2xyz")
	_, _, attrs := cells[row*w+x+4].Style.Decompose()
	if attrs&tcell.AttrReverse == 0 {
		t.Error("the cursor character must be drawn in reverse in normal mode")
	}
	press(t, "enter")
	if b.cont[2][1] != "b2xyz" || cellEdit != nil {
		t.Errorf("applied %q", b.cont[2][1])
	}

	// A combining mark is drawn together with its base character.
	b.cont[3][1] = "e\u0301x"
	bufferTable.Select(3, 1)
	press(t, "E")
	mainView.Draw(screen)
	screen.Show()
	cells, w, _ = screen.GetContents()
	found := false
	for _, cell := range cells {
		if len(cell.Runes) == 2 && cell.Runes[0] == 'e' && cell.Runes[1] == 0x0301 {
			found = true
		}
	}
	if !found {
		t.Error("the accent must be drawn as a combining rune on its base")
	}
	_ = w
	press(t, "esc")
}

func TestCtrlCInEditorAndQuitFocus(t *testing.T) {
	setupWriteTable(t, "a,b\n1,2\n3,4\n")
	t.Cleanup(func() { cellEdit = nil })
	ctrlC := tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
	press(t, "i d r a f t")
	if cellEdit == nil || cellEdit.ed.mode != editInsert {
		t.Fatal("editor should be open in insert mode")
	}
	if ev := handleAppKey(ctrlC); ev != nil || cellEdit == nil || cellEdit.ed.mode != editNormal {
		t.Error("Ctrl-C in insert mode must act as Esc, not quit")
	}
	if ev := handleAppKey(ctrlC); ev != nil || cellEdit != nil || dirty() {
		t.Error("Ctrl-C in normal mode must cancel the editor, not quit")
	}
	if ev := handleAppKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)); ev == nil {
		t.Error("other keys pass through the application capture")
	}
	// With pending edits Ctrl-C opens the quit dialog; Cancel returns the
	// focus to whatever had it.
	press(t, "d d")
	form := tview.NewForm()
	app.SetFocus(form)
	if ev := handleAppKey(ctrlC); ev != nil || !UI.HasPage("quitDialog") {
		t.Fatal("Ctrl-C with pending edits must prompt")
	}
	_, front := UI.GetFrontPage()
	front.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(tview.Primitive) {})
	if UI.HasPage("quitDialog") || app.GetFocus() != form {
		t.Errorf("Esc must close the dialog and restore the focus: page=%v focus=%T", UI.HasPage("quitDialog"), app.GetFocus())
	}
}
