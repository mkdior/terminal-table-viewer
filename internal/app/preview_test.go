package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestPreviewLayout(t *testing.T) {
	lines, w, h, ok := previewLayout("hello world", 80, 24)
	if !ok || len(lines) != 1 || w != len("hello world")+4 || h != 3 {
		t.Fatalf("short value: lines=%v w=%d h=%d ok=%v", lines, w, h, ok)
	}

	long := strings.Repeat("word ", 40) // 200 chars
	lines, w, h, ok = previewLayout(long, 80, 24)
	if !ok || len(lines) < 2 || w > 80 || h != len(lines)+2 {
		t.Fatalf("wrapped value: lines=%d w=%d h=%d ok=%v", len(lines), w, h, ok)
	}

	if _, _, _, ok := previewLayout(strings.Repeat("x", maxPreviewRunes+1), 200, 100); ok {
		t.Error("values over maxPreviewRunes must not be previewed")
	}
	if _, _, _, ok := previewLayout(strings.Repeat("word ", 100), 40, 10); ok {
		t.Error("a value taller than half the table must not be previewed")
	}
	if _, w, _, ok := previewLayout("日本語のテキスト", 80, 24); !ok || w != 16+4 {
		t.Errorf("wide runes must be measured by display width, got w=%d ok=%v", w, ok)
	}
}

func TestPreviewListsOneItemPerLine(t *testing.T) {
	oldSplit, oldSep := previewSplitItems, previewSeparator
	t.Cleanup(func() { previewSplitItems, previewSeparator = oldSplit, oldSep })
	previewSplitItems, previewSeparator = true, ";"

	lines, w, h, ok := previewLayout("alpha; beta;gamma;", 80, 24)
	if !ok || strings.Join(lines, "|") != "alpha|beta|gamma" || w != len("alpha")+4 || h != 3+2 {
		t.Errorf("list value: lines=%q w=%d h=%d ok=%v", lines, w, h, ok)
	}
	if lines, _, _, ok := previewLayout("no separator here", 80, 24); !ok || len(lines) != 1 {
		t.Errorf("a plain value must stay one line, got %q", lines)
	}
	// An item longer than the box wraps on its own; the next item starts a new line.
	lines, _, _, ok = previewLayout(strings.Repeat("word ", 10)+"; tail", 30, 24)
	if !ok || lines[len(lines)-1] != "tail" || len(lines) < 3 {
		t.Errorf("long item must wrap before the next item, got %q", lines)
	}
	// A list too long for the box falls back to the wrapped sentence.
	many := strings.TrimSuffix(strings.Repeat("item;", 20), ";")
	lines, _, _, ok = previewLayout(many, 80, 24) // 10 lines fit: 24/2 - 2
	if !ok || len(lines) != 2 {
		t.Errorf("an overlong list must be shown as wrapped text, got %d lines ok=%v: %q", len(lines), ok, lines)
	}

	previewSeparator = "|"
	if lines, _, _, ok := previewLayout("a|b|c", 80, 24); !ok || len(lines) != 3 {
		t.Errorf("the separator is configurable, got %q", lines)
	}
	previewSplitItems = false
	if lines, _, _, ok := previewLayout("a|b|c", 80, 24); !ok || len(lines) != 1 || lines[0] != "a|b|c" {
		t.Errorf("with split_items off the value is shown as it is, got %q", lines)
	}
}

func TestTruncatedCellTextUsesDisplayWidth(t *testing.T) {
	wide := strings.Repeat("日", 30) // 30 runes, 60 cells
	buf, _ := createNewBufferWithData([][]string{{"h"}, {wide}}, true)
	oldB, oldWrapped := b, wrappedColumns
	defer func() { b, wrappedColumns = oldB, oldWrapped }()
	b = buf
	wrappedColumns = map[int]int{0: 50}
	if _, ok := truncatedCellText(1, 0); !ok {
		t.Error("a 60-cell value under a 50-cell limit is cut and must be previewed")
	}
	wrappedColumns = map[int]int{0: 60}
	if _, ok := truncatedCellText(1, 0); ok {
		t.Error("a value that fits its limit must not be previewed")
	}
}

// screenText joins the simulation screen rows into one string.
func screenText(s tcell.SimulationScreen) string {
	cells, w, h := s.GetContents()
	var sb strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				sb.WriteString(string(c.Runes))
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestCellPreviewShowsFullValueForTruncatedCell(t *testing.T) {
	long := "The quick brown fox jumps over the lazy dog again and again until dusk"
	data := [][]string{{"id", "text"}, {"1", long}, {"2", "short"}}
	buf, err := createNewBufferWithData(data, true)
	if err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1

	// Wire up the globals the UI reads, restoring them afterwards.
	oldB, oldWrapped, oldTable, oldPage, oldView := b, wrappedColumns, bufferTable, mainPage, mainView
	defer func() {
		b, wrappedColumns, bufferTable, mainPage, mainView = oldB, oldWrapped, oldTable, oldPage, oldView
	}()
	b = buf
	wrappedColumns = map[int]int{1: 20}
	setSearchResults(nil)
	searchQuery = ""

	bufferTable = tview.NewTable().SetSelectable(true, true).SetFixed(1, 1)
	drawBuffer(b, bufferTable)
	bufferTable.Focus(func(tview.Primitive) {})
	mainPage = tview.NewFrame(bufferTable)
	mainView = newCellPreview(mainPage)
	oldPos := previewPos
	defer func() { previewPos = oldPos }()
	previewPos = previewBottom // keep the box off the selected cell so its ellipsis stays visible

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(100, 24)
	mainView.SetRect(0, 0, 100, 24)

	bufferTable.Select(1, 1)
	updateCellPreview(1, 1)
	mainView.Draw(screen)
	screen.Show()
	out := screenText(screen)
	if !strings.Contains(out, "again and again until dusk") {
		t.Fatalf("full value must be visible in the preview box:\n%s", out)
	}
	if !strings.Contains(out, "...") {
		t.Errorf("the table cell itself should still be cut with an ellipsis:\n%s", out)
	}
	if !strings.Contains(out, " text ") {
		t.Errorf("the box should be titled with the column name:\n%s", out)
	}

	screen.Clear()
	bufferTable.Select(2, 1)
	updateCellPreview(2, 1)
	mainView.Draw(screen)
	screen.Show()
	if out := screenText(screen); strings.Contains(out, "until dusk") {
		t.Errorf("preview must disappear on a cell that is not cut:\n%s", out)
	}
}

// rowOf returns the screen row (0-based) whose text contains needle, or -1.
func rowOf(out, needle string) int {
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

// colOf returns the screen column where needle starts in line, or -1. Box
// borders are multi-byte runes, so byte offsets would be wrong.
func colOf(line, needle string) int {
	i := strings.Index(line, needle)
	if i < 0 {
		return -1
	}
	return displayWidth(line[:i])
}

func TestCellPreviewPositions(t *testing.T) {
	long := "The quick brown fox jumps over the lazy dog again and again until dusk"
	data := [][]string{{"id", "text", "more"}}
	for i := 1; i <= 12; i++ {
		data = append(data, []string{I2S(i), long, "x"})
	}
	buf, err := createNewBufferWithData(data, true)
	if err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1
	oldB, oldWrapped, oldTable, oldPage, oldView, oldPos := b, wrappedColumns, bufferTable, mainPage, mainView, previewPos
	defer func() {
		b, wrappedColumns, bufferTable, mainPage, mainView, previewPos = oldB, oldWrapped, oldTable, oldPage, oldView, oldPos
	}()
	b = buf
	wrappedColumns = map[int]int{1: 20}
	setSearchResults(nil)
	searchQuery = ""

	bufferTable = tview.NewTable().SetSelectable(true, true).SetFixed(1, 1)
	drawBuffer(b, bufferTable)
	bufferTable.Focus(func(tview.Primitive) {})
	mainPage = tview.NewFrame(bufferTable)
	mainView = newCellPreview(mainPage)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	const width, height = 100, 16
	screen.SetSize(width, height)
	mainView.SetRect(0, 0, width, height)

	draw := func(row int) string {
		screen.Clear()
		bufferTable.Select(row, 1)
		updateCellPreview(row, 1)
		mainView.Draw(screen)
		screen.Show()
		return screenText(screen)
	}
	firstLine := "The quick brown fox" // the wrapped value starts like this

	// cursor: the value's first line sits on the selected cell's own row and column
	previewPos = previewAtCursor
	out := draw(3)
	cell := currentContent.drawnCell(3, 1)
	cx, cy, cw := cell.GetLastPosition()
	if cw == 0 {
		t.Fatal("selected cell position was not recorded during the draw")
	}
	if got := rowOf(out, firstLine); got != cy {
		t.Errorf("cursor mode: value starts on screen row %d, want the cell's row %d\n%s", got, cy, out)
	}
	if line := strings.Split(out, "\n")[cy]; colOf(line, firstLine) != cx {
		t.Errorf("cursor mode: value starts at column %d, want the cell's column %d", colOf(line, firstLine), cx)
	}

	// cursor near the bottom: the box grows upwards and its last line sits on the cell's row
	out = draw(12)
	_, cy, _ = currentContent.drawnCell(12, 1).GetLastPosition()
	if got := rowOf(out, "until dusk"); got != cy {
		t.Errorf("cursor mode at the bottom: last line on row %d, want the cell's row %d\n%s", got, cy, out)
	}

	// top: box starts right under the header, whatever the cursor row
	previewPos = previewTop
	out = draw(12)
	_, ty, _, th := bufferTable.GetInnerRect()
	if got, want := rowOf(out, firstLine), ty+b.rowFreeze+1; got != want { // header, then the box border, then the value
		t.Errorf("top mode: value on row %d, want %d\n%s", got, want, out)
	}

	// bottom: box ends at the bottom of the table area
	previewPos = previewBottom
	out = draw(3)
	if got := rowOf(out, "until dusk"); got != ty+th-2 { // last text line above the bottom border
		t.Errorf("bottom mode: last line on row %d, want %d\n%s", got, ty+th-2, out)
	}

	if _, err := parsePreviewPosition("sideways"); err == nil {
		t.Error("unknown positions must be rejected")
	}
	if pos, err := parsePreviewPosition(""); err != nil || pos != previewBottom {
		t.Errorf("an empty setting must mean the default, bottom; got %v %v", pos, err)
	}
}

func TestCellsArePaddedToColumnWidth(t *testing.T) {
	data := [][]string{{"h", "name"}, {"1", "short"}, {"2", "a considerably longer value"}}
	buf, _ := createNewBufferWithData(data, true)
	buf.rowFreeze = 1
	oldB, oldWrapped := b, wrappedColumns
	defer func() { b, wrappedColumns = oldB, oldWrapped }()
	b = buf
	wrappedColumns = map[int]int{}
	setSearchResults(nil)
	c := &bufferContent{b: buf}
	want := displayWidth("a considerably longer value")
	if got := displayWidth(c.GetCell(1, 1).Text); got != want {
		t.Errorf("short cell padded to %d cells, want the column width %d", got, want)
	}
	if got := c.GetCell(0, 1).Text; !strings.HasPrefix(got, " ") || !strings.HasSuffix(got, " ") || displayWidth(got) != want {
		t.Errorf("header must be centred within the column width, got %q", got)
	}
	first := c.GetCell(1, 1)
	if c.GetCell(1, 1) != first {
		t.Error("cells must be cached within a frame")
	}
	c.beginFrame()
	if c.GetCell(1, 1) == first {
		t.Error("beginFrame must drop the cached cells")
	}
	c.beginFrame()
	wrappedColumns = map[int]int{1: 10}
	if got := displayWidth(c.GetCell(1, 1).Text); got != 10 {
		t.Errorf("a width-limited column pads only to its limit, got %d", got)
	}
}
