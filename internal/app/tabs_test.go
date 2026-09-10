package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// csvRows builds a two-column CSV with a header and n data rows whose first
// cells are tag1, tag2, ..., so the tables of different tabs tell apart.
func csvRows(tag string, n int) string {
	var sb strings.Builder
	sb.WriteString("h1,h2\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "%s%d,x\n", tag, i)
	}
	return sb.String()
}

// setupTabs writes the contents to files t1.csv, t2.csv, ... in a temp
// directory and opens them in tabs, loading synchronously without a memory
// limit, with a UI container and an application that is not running. It
// returns the paths and the notes about skipped files.
func setupTabs(t *testing.T, contents ...string) (names, skipped []string) {
	t.Helper()
	return setupTabsWithin(t, 0, contents...)
}

// setupTabsWithin is setupTabs with a memory budget of limit bytes shared by
// the tabs (0 for none).
func setupTabsWithin(t *testing.T, limit int64, contents ...string) (names, skipped []string) {
	t.Helper()
	setupEditTable(t) // clipboard stub, editing allowed, globals restored after
	dir := t.TempDir()
	oldAsync, oldUI, oldApp, oldBudget, oldIDs, oldStopped := args.AsyncLoad, UI, app, budget, tabIDs, loadStopped
	t.Cleanup(func() {
		args.AsyncLoad, UI, app, budget, tabIDs, loadStopped = oldAsync, oldUI, oldApp, oldBudget, oldIDs, oldStopped
		tabs, current, loaded = nil, -1, nil
		visual, cellEdit = visualOff, nil
	})
	args.AsyncLoad = false
	app = tview.NewApplication()
	tabIDs, budget = 0, nil
	if limit > 0 {
		budget = &memoryBudget{limit: limit}
	}
	for i, c := range contents {
		name := filepath.Join(dir, fmt.Sprintf("t%d.csv", i+1))
		if err := os.WriteFile(name, []byte(c), 0o640); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	skipped, err := openTabs(names, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return names, skipped
}

// stubStop counts calls to stopApp instead of stopping an application.
func stubStop(t *testing.T) *int {
	t.Helper()
	stops := 0
	old := stopApp
	stopApp = func() { stops++ }
	t.Cleanup(func() { stopApp = old })
	return &stops
}

// frontName returns the name of the page in front.
func frontName() string {
	name, _ := UI.GetFrontPage()
	return name
}

func TestTabsOpenOnePerFileAndSwitch(t *testing.T) {
	names, skipped := setupTabs(t, csvRows("A", 3), csvRows("B", 3), csvRows("C", 3))
	if len(tabs) != 3 || current != 0 || len(skipped) != 0 || frontName() != "tab1" {
		t.Fatalf("tabs %d current %d skipped %v front %q", len(tabs), current, skipped, frontName())
	}
	if args.FileName != names[0] || b.cont[1][0] != "A1" || !strings.Contains(fileNameStr, "t1.csv") {
		t.Errorf("the first tab is in front: %q %q %q", args.FileName, b.cont[1][0], fileNameStr)
	}
	press(t, "g t")
	if current != 1 || args.FileName != names[1] || b.cont[1][0] != "B1" || frontName() != "tab2" {
		t.Errorf("gt: current %d file %q cell %q front %q", current, args.FileName, b.cont[1][0], frontName())
	}
	if app.GetFocus() != bufferTable || bufferTable != tabs[1].bufferTable {
		t.Errorf("the tab's own table has the focus: %T", app.GetFocus())
	}
	press(t, "g t g t") // wraps to the first
	if current != 0 || b.cont[1][0] != "A1" {
		t.Errorf("gt wraps: current %d cell %q", current, b.cont[1][0])
	}
	press(t, "g T") // and back
	if current != 2 || b.cont[1][0] != "C1" {
		t.Errorf("gT wraps: current %d cell %q", current, b.cont[1][0])
	}
	press(t, "2 g t")
	if current != 1 {
		t.Errorf("2gt goes to tab 2, got %d", current)
	}
	press(t, "9 g t")
	if current != 2 {
		t.Errorf("a count past the end lands on the last tab, got %d", current)
	}
	press(t, "2 g T")
	if current != 0 {
		t.Errorf("2gT from the last tab of three reaches the first, got %d", current)
	}
	if got := statusMessage; got != "All Done" {
		t.Errorf("a switch shows the tab's own status: %q", got)
	}
}

func TestTabsKeepTheirOwnState(t *testing.T) {
	setupTabs(t, csvRows("A", 4), csvRows("B", 4))
	press(t, "j l d d _") // tab 1: remove A2 with the cursor on column 1, limit the column's width
	if !dirty() || b.rowLen != 4 || len(wrappedColumns) != 1 {
		t.Fatalf("tab 1 edits: dirty %v rows %d wrapped %v", dirty(), b.rowLen, wrappedColumns)
	}
	if r, c := bufferTable.GetSelection(); r != 2 || c != 1 {
		t.Fatalf("cursor %d,%d", r, c)
	}
	press(t, "V g t") // the visual selection does not travel
	if current != 1 || visual != visualOff {
		t.Fatalf("gt: current %d visual %v", current, visual)
	}
	if dirty() || b.rowLen != 5 || len(wrappedColumns) != 0 || len(edits) != 0 {
		t.Errorf("tab 2 is untouched: dirty %v rows %d wrapped %v", dirty(), b.rowLen, wrappedColumns)
	}
	if r, c := bufferTable.GetSelection(); r != 1 || c != 0 {
		t.Errorf("tab 2 has its own cursor: %d,%d", r, c)
	}
	if strings.Contains(fileNameStr, "[+]") || !strings.Contains(fileNameStr, "t2.csv") {
		t.Errorf("footer of tab 2: %q", fileNameStr)
	}
	press(t, "x") // an edit in tab 2
	press(t, "g T")
	if current != 0 || !dirty() || b.rowLen != 4 || wrappedColumns[1] == 0 || !strings.Contains(fileNameStr, "t1.csv [+]") {
		t.Errorf("tab 1 comes back as it was: dirty %v rows %d wrapped %v footer %q", dirty(), b.rowLen, wrappedColumns, fileNameStr)
	}
	if r, c := bufferTable.GetSelection(); r != 2 || c != 1 {
		t.Errorf("tab 1 cursor restored: %d,%d", r, c)
	}
	if !tabs[1].hasEdits() || tabs[1].b.cont[1][0] != "" {
		t.Errorf("tab 2 keeps its edit while parked: %v", tabs[1].b.cont[1])
	}
	line := tabLine()
	if !strings.Contains(line, "[::b] 1 t1.csv [+] [-:-:-]") || !strings.Contains(line, " 2 t2.csv [+] ") {
		t.Errorf("tab line = %q", line)
	}
	press(t, "u u")
	if dirty() || !strings.Contains(tabLine(), " 1 t1.csv ") || strings.Contains(tabLine(), "t1.csv [+]") {
		t.Errorf("undo clears the marker: %q", tabLine())
	}
}

func TestTabLineShowsLoadProgress(t *testing.T) {
	setupTabs(t, csvRows("A", 2), csvRows("B", 2), csvRows("C", 2))
	behind := &tabs[1].base.progress
	behind.IsComplete.Store(false)
	behind.TotalBytes.Store(200)
	behind.LoadedBytes.Store(50)
	unknown := &tabs[2].base.progress // size unknown: a pipe or a gzip file
	unknown.IsComplete.Store(false)
	unknown.TotalBytes.Store(0)
	if line := tabLine(); !strings.Contains(line, " 2 t2.csv 25% ") || !strings.Contains(line, " 3 t3.csv loading ") {
		t.Errorf("tab line = %q", line)
	}
	// The tab line is the first header line of the frame in front, redrawn by
	// the loading tabs behind now and then.
	refreshTabLine()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 10)
	mainView.SetRect(0, 0, 80, 10)
	mainView.Draw(screen)
	screen.Show()
	top := strings.Split(screenText(screen), "\n")[0]
	if !strings.Contains(top, "1 t1.csv") || !strings.Contains(top, "2 t2.csv 25%") || !strings.Contains(top, "3 t3.csv loading") || strings.Contains(top, "[") {
		t.Errorf("rendered tab line: %q", top)
	}
	behind.IsComplete.Store(true)
	unknown.IsComplete.Store(true)
	if line := tabLine(); strings.Contains(line, "%") || strings.Contains(line, "loading") {
		t.Errorf("done loading: %q", line)
	}
}

func TestTabLineScrollsToKeepTheFrontTabVisible(t *testing.T) {
	setupTabs(t, csvRows("A", 1), csvRows("B", 1), csvRows("C", 1), csvRows("D", 1), csvRows("E", 1), csvRows("F", 1))
	t.Cleanup(func() { tabLineWidth, tabLineStart, tabSpans = 0, 0, nil })
	// Every label is " N tN.csv ", ten cells; 34 cells hold three of them and
	// one edge marker, or two and both markers.
	tabLineWidth = 34
	line := tabLine()
	if want := " 1 t1.csv   2 t2.csv   3 t3.csv  >"; strings.Contains(line, "t4.csv") || !strings.HasSuffix(line, " 3 t3.csv  >") || !strings.HasPrefix(line, "[") {
		t.Errorf("from the first tab: %q, want the labels of %q", line, want)
	}
	if tab, ok := tabAt(22); !ok || tab != 2 {
		t.Errorf("tabAt(22) = %d %v, want tab 2", tab, ok)
	}
	if tab, ok := tabAt(33); !ok || tab != tabsAfter {
		t.Errorf("tabAt(33) = %d %v, want the right marker", tab, ok)
	}
	if _, ok := tabAt(10); ok {
		t.Error("the space between two labels is no tab")
	}
	press(t, "5 g t") // tab 5 is past the edge: the window scrolls until it shows
	line = tabLine()
	if !strings.HasPrefix(line, "< ") || !strings.Contains(line, "[::b] 5 t5.csv [-:-:-]") || !strings.HasSuffix(line, " 6 t6.csv ") || strings.Contains(line, "t3.csv") {
		t.Errorf("after 5gt: %q", line)
	}
	if tab, ok := tabAt(0); !ok || tab != tabsBefore {
		t.Errorf("tabAt(0) = %d %v, want the left marker", tab, ok)
	}
	if tab, ok := tabAt(2); !ok || tab != 3 {
		t.Errorf("tabAt(2) = %d %v, want tab 3 (the first shown)", tab, ok)
	}
	press(t, "g T") // tab 4 is within the window: nothing scrolls
	if line = tabLine(); !strings.Contains(line, "[::b] 4 t4.csv [-:-:-]") || tabLineStart != 3 {
		t.Errorf("after gT: %q start %d", line, tabLineStart)
	}
	press(t, "g T") // tab 3 is before the window: it becomes the first shown
	if line = tabLine(); !strings.HasPrefix(line, "< ") || !strings.Contains(line, "[::b] 3 t3.csv [-:-:-]") || !strings.HasSuffix(line, " >") || strings.Contains(line, "t5.csv") {
		t.Errorf("after gT gT: %q", line)
	}
	// The draw records the frame's width and lays the line out for it.
	tabLineWidth = 0
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(34, 8)
	mainView.SetRect(0, 0, 34, 8)
	mainView.Draw(screen)
	screen.Show()
	top := strings.Split(screenText(screen), "\n")[0]
	if tabLineWidth != 34 || !strings.HasPrefix(top, "<  3 t3.csv   4 t4.csv  >") {
		t.Errorf("drawn at 34 cells (width %d): %q", tabLineWidth, top)
	}
	// A long file name is cut so several tabs stay readable.
	tabs[2].name = strings.Repeat("n", 60) + ".csv"
	tabLineWidth = 0
	line = tabLine()
	for _, s := range tabSpans {
		if s.tab == 2 && s.x2-s.x1+1 > maxTabTitle+4 {
			t.Errorf("a long name is cut: label %d cells wide in %q", s.x2-s.x1+1, line)
		}
	}
	if !strings.Contains(line, "...") {
		t.Errorf("the cut shows an ellipsis: %q", line)
	}
}

func TestRegisterIsSharedAcrossTabs(t *testing.T) {
	setupTabs(t, csvRows("A", 2), csvRows("B", 2))
	press(t, "y g t p")
	if current != 1 || b.cont[1][0] != "A1" || !strings.Contains(statusMessage, "Pasted 1 cell") {
		t.Errorf("paste across tabs: cell %q status %q", b.cont[1][0], statusMessage)
	}
	press(t, "j d d g T G p") // a removed row travels too
	if current != 0 || strings.Join(b.cont[2], " ") != "B2 x" {
		t.Errorf("row paste across tabs: %v", b.cont[2])
	}
}

func TestQClosesTheTabInFront(t *testing.T) {
	names, _ := setupTabs(t, csvRows("A", 2), csvRows("B", 2), csvRows("C", 2))
	stops := stubStop(t)
	press(t, "q")
	if len(tabs) != 2 || current != 0 || args.FileName != names[1] || UI.HasPage("tab1") || frontName() != "tab2" {
		t.Fatalf("q: tabs %d current %d file %q front %q", len(tabs), current, args.FileName, frontName())
	}
	if statusMessage != "Closed t1.csv" || *stops != 0 {
		t.Errorf("status %q stops %d", statusMessage, *stops)
	}
	if !strings.Contains(tabLine(), "[::b] 1 t2.csv [-:-:-]") || strings.Contains(tabLine(), "t1.csv") {
		t.Errorf("tab line after closing: %q", tabLine())
	}
	press(t, "g t q") // the last tab closed: its left neighbour comes to the front
	if len(tabs) != 1 || current != 0 || args.FileName != names[1] || len(UI.GetPageNames(false)) != 1 {
		t.Errorf("closing the last tab: tabs %d current %d file %q pages %v", len(tabs), current, args.FileName, UI.GetPageNames(false))
	}
	press(t, "g t")
	if statusMessage != "No other tab is open" {
		t.Errorf("gt with one tab: %q", statusMessage)
	}
	press(t, "q") // the only tab: quits
	if *stops != 1 || len(tabs) != 1 {
		t.Errorf("q on the last tab quits: stops %d tabs %d", *stops, len(tabs))
	}
}

func TestClosingADirtyTabAsksFirst(t *testing.T) {
	names, _ := setupTabs(t, csvRows("A", 2), csvRows("B", 2), csvRows("C", 2))
	stops := stubStop(t)
	press(t, "d d q")
	if !UI.HasPage("quitDialog") || len(tabs) != 3 {
		t.Fatal("pending edits must prompt before the tab closes")
	}
	pressModal(t, tcell.KeyTab, tcell.KeyTab, tcell.KeyEnter) // Cancel
	if UI.HasPage("quitDialog") || len(tabs) != 3 || !dirty() || app.GetFocus() != bufferTable {
		t.Errorf("Cancel stays: tabs %d dirty %v focus %T", len(tabs), dirty(), app.GetFocus())
	}
	press(t, "q")
	pressModal(t, tcell.KeyTab, tcell.KeyEnter) // Discard
	if len(tabs) != 2 || current != 0 || args.FileName != names[1] || readFile(t, names[0]) != csvRows("A", 2) {
		t.Errorf("Discard closes without writing: tabs %d file %q content %q", len(tabs), args.FileName, readFile(t, names[0]))
	}
	press(t, "d d q")
	pressModal(t, tcell.KeyEnter) // Write
	if len(tabs) != 1 || args.FileName != names[2] || readFile(t, names[1]) != "h1,h2\nB2,x\n" {
		t.Errorf("Write writes and closes: tabs %d file %q content %q", len(tabs), args.FileName, readFile(t, names[1]))
	}
	if *stops != 0 {
		t.Errorf("closing tabs never quits: %d", *stops)
	}
}

// dialogText renders the modal in front and returns the screen text.
func dialogText(t *testing.T) string {
	t.Helper()
	_, front := UI.GetFrontPage()
	if _, ok := front.(*tview.Modal); !ok {
		t.Fatalf("front page is %T, not a modal", front)
	}
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(200, 40)
	front.SetRect(0, 0, 200, 40)
	front.Draw(screen)
	screen.Show()
	return screenText(screen)
}

func TestCtrlCQuitsEveryTabWithOnePrompt(t *testing.T) {
	names, _ := setupTabs(t, csvRows("A", 3), csvRows("B", 3), csvRows("C", 3))
	stops := stubStop(t)
	ctrlC := tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)

	handleAppKey(ctrlC)
	if *stops != 1 || UI.HasPage("quitDialog") {
		t.Fatalf("clean tabs quit at once: stops %d", *stops)
	}
	press(t, "d d g t g t d d g T") // edits in tabs 1 and 3, tab 2 in front
	handleAppKey(ctrlC)
	if !UI.HasPage("quitDialog") || *stops != 1 {
		t.Fatal("unwritten changes must prompt")
	}
	text := dialogText(t)
	for _, want := range []string{"2 of 3 tabs", "t1.csv: 1 row removed", "t3.csv: 1 row removed", "Write them and quit?"} {
		if !strings.Contains(text, want) {
			t.Errorf("dialog lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "t2.csv") {
		t.Errorf("a clean tab is not listed:\n%s", text)
	}
	pressModal(t, tcell.KeyTab, tcell.KeyTab, tcell.KeyEnter) // Cancel
	if UI.HasPage("quitDialog") || *stops != 1 || current != 1 {
		t.Errorf("Cancel stays on the same tab: current %d stops %d", current, *stops)
	}
	handleAppKey(ctrlC)
	pressModal(t, tcell.KeyEnter) // Write: every dirty tab in turn, then quit
	if *stops != 2 || readFile(t, names[0]) != "h1,h2\nA2,x\nA3,x\n" || readFile(t, names[2]) != "h1,h2\nC2,x\nC3,x\n" {
		t.Errorf("Write all: stops %d\n%q\n%q", *stops, readFile(t, names[0]), readFile(t, names[2]))
	}
	if readFile(t, names[1]) != csvRows("B", 3) || tabs[0].hasEdits() || tabs[2].hasEdits() {
		t.Error("the clean tab is left alone and the written ones are clean")
	}
	if current != 2 {
		t.Errorf("writing all leaves the last written tab in front: %d", current)
	}

	press(t, "d d") // in the tab in front
	if !dirty() {
		t.Fatal("precondition: an edit in tab 3")
	}
	handleAppKey(ctrlC)
	pressModal(t, tcell.KeyTab, tcell.KeyEnter) // Discard
	if *stops != 3 || readFile(t, names[2]) != "h1,h2\nC2,x\nC3,x\n" {
		t.Errorf("Discard quits without writing: stops %d %q", *stops, readFile(t, names[2]))
	}

	// A tab whose changes cannot be written leaves only Discard.
	loadStopped = true
	handleAppKey(ctrlC)
	text = dialogText(t)
	if !strings.Contains(text, "cannot be written: the load stopped early") || !strings.Contains(text, "Quit and discard them?") || strings.Contains(text, "Write") {
		t.Errorf("blocked write:\n%s", text)
	}
	pressModal(t, tcell.KeyEnter) // Discard is the first button now
	if *stops != 4 {
		t.Errorf("Discard on a blocked write quits: stops %d", *stops)
	}
}

func TestEmptyFilesAmongSeveralAreSkipped(t *testing.T) {
	names, skipped := setupTabs(t, csvRows("A", 2), "", "h1,h2\n", csvRows("D", 2))
	if len(tabs) != 2 || len(skipped) != 2 || args.FileName != names[0] || tabs[1].name != names[3] {
		t.Fatalf("tabs %d skipped %v", len(tabs), skipped)
	}
	want := "Skipped t2.csv: empty (no rows); Skipped t3.csv: empty (only header, no data rows)"
	if statusMessage != want || tabs[0].note != want {
		t.Errorf("status %q note %q", statusMessage, tabs[0].note)
	}
	if !strings.Contains(tabLine(), " 2 t4.csv ") {
		t.Errorf("tab line %q", tabLine())
	}
}

func TestTheOnlyEmptyFileIsAnError(t *testing.T) {
	setupEditTable(t)
	t.Cleanup(func() { tabs, current, loaded = nil, -1, nil })
	oldAsync := args.AsyncLoad
	t.Cleanup(func() { args.AsyncLoad = oldAsync })
	dir := t.TempDir()
	name := filepath.Join(dir, "only.csv")
	if err := os.WriteFile(name, []byte("h1,h2\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, async := range []bool{false, true} {
		args.AsyncLoad = async
		_, err := openTabs([]string{name}, nil, 0)
		if !errors.Is(err, errEmpty) || err.Error() != "empty (only header, no data rows)" || len(tabs) != 0 {
			t.Errorf("async=%v: err %v tabs %d", async, err, len(tabs))
		}
	}
}

func TestMemoryBudgetIsSharedAndReleased(t *testing.T) {
	// The budget takes the whole small file, part of the second and nothing
	// of the third; each tab says what happened to it.
	small, big := csvRows("A", 3), csvRows("B", 2000)
	limit := int64(len(small)+len(big)/2) * 3 // rows are estimated at several times their text
	names, skipped := setupTabsWithin(t, limit, small, big, csvRows("C", 5))
	if len(tabs) != 2 || tabs[0].name != names[0] || tabs[1].name != names[1] {
		t.Fatalf("tabs %d skipped %v", len(tabs), skipped)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "Skipped t3.csv: empty (memory limit reached") || !strings.Contains(skipped[0], "before any row was read)") {
		t.Errorf("the third file did not fit: %v", skipped)
	}
	if got := tabs[1].statusMessage; !strings.HasPrefix(got, "Stopped after ") || !strings.Contains(got, "memory limit reached") || !tabs[1].loadStopped {
		t.Errorf("the second tab stopped at the limit: %q", got)
	}
	if rows := tabs[1].base.rowLen; rows < 2 || rows >= 2001 {
		t.Errorf("the second tab holds part of its file: %d rows", rows)
	}
	used := budget.used.Load()
	if used > limit || used < tabs[0].base.getMemoryUsage()+tabs[1].base.getMemoryUsage() {
		t.Errorf("used %d limit %d tabs %d+%d", used, limit, tabs[0].base.getMemoryUsage(), tabs[1].base.getMemoryUsage())
	}
	stubStop(t)
	press(t, "q") // closing the first tab gives its share back
	if len(tabs) != 1 || budget.used.Load() != tabs[0].base.getMemoryUsage() {
		t.Errorf("after closing: used %d, the remaining tab holds %d", budget.used.Load(), tabs[0].base.getMemoryUsage())
	}
}

func TestClosingALoadingTabCancelsTheLoad(t *testing.T) {
	setupTabsWithin(t, 1<<20, csvRows("A", 2), csvRows("B", 2))
	if len(tabs) != 2 {
		t.Fatalf("tabs %d", len(tabs))
	}
	first := tabs[0]
	first.done = make(chan error) // pretend the load is still running
	stubStop(t)
	press(t, "q")
	if !first.closed || !first.base.stopLoad.Load() || first.released || budget.used.Load() <= tabs[0].base.getMemoryUsage() {
		t.Fatalf("closing tells the load to stop and waits for it: closed %v stop %v released %v used %d", first.closed, first.base.stopLoad.Load(), first.released, budget.used.Load())
	}
	// The loader sees the flag on its next line and returns; the handler then
	// reports to the closed tab, which gives its memory back.
	buf := createNewBuffer()
	buf.stopLoad.Store(true)
	if err := loadPipeToBuffer(strings.NewReader(csvRows("Z", 50)), buf); !errors.Is(err, errLoadCancelled) || buf.rowLen > separatorSampleLines {
		t.Errorf("a cancelled load stops after the sample: err %v rows %d", err, buf.rowLen)
	}
	first.loadFinished(errLoadCancelled)
	if !first.released || budget.used.Load() != tabs[0].base.getMemoryUsage() || first.base.budget != nil {
		t.Errorf("released %v used %d, the remaining tab holds %d", first.released, budget.used.Load(), tabs[0].base.getMemoryUsage())
	}
	if current != 0 || args.FileName != tabs[0].name || statusMessage != "Closed t1.csv" {
		t.Errorf("the closed tab's report must not touch the tab in front: %q", statusMessage)
	}
}

func TestLoadFinishedReportsToAParkedTab(t *testing.T) {
	setupTabs(t, csvRows("A", 2), csvRows("B", 2))
	second := tabs[1]
	second.note = "Skipped x.csv: empty (no rows)"
	second.loadFinished(nil)
	if current != 0 || statusMessage != "All Done" || loaded != tabs[0] {
		t.Errorf("the tab in front is untouched: current %d status %q", current, statusMessage)
	}
	if second.statusMessage != "Loaded 3 rows; Skipped x.csv: empty (no rows)" || second.loadStopped {
		t.Errorf("parked status %q stopped %v", second.statusMessage, second.loadStopped)
	}
	second.loadFinished(errors.New("boom"))
	press(t, "g t")
	if statusMessage != "Stopped after 3 rows: boom; Skipped x.csv: empty (no rows)" || !loadStopped {
		t.Errorf("shown status %q stopped %v", statusMessage, loadStopped)
	}
}
