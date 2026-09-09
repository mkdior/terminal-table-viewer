package app

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	rtdebug "runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Tabs, as in vim: ttv A.csv B.csv opens one tab per file, gt and gT switch
// between them, q closes the tab in front and Ctrl-C quits every tab. The
// rest of the package keeps the state of the table in front in package
// variables (the buffer with its filters and pending edits, the widgets, the
// footer texts); a tab is where that state is parked while another tab is in
// front. Showing a tab parks the variables into the outgoing tab and restores
// them from the incoming one, so the code that works on the table needs no
// notion of tabs. What every tab shares stays where it is: the registers, the
// keymap, the theme, the clipboard and the memory budget.
//
// Every file loads on its own goroutine, all of them at once, so a tab can be
// read while the others fill in. Two large files therefore compete for one
// --memory budget rather than each taking it, and closing a tab stops its
// load and gives its memory back.

// tab is one open file with its per-file state parked.
type tab struct {
	id     int     // the page is named after it, so it survives closing other tabs
	name   string  // args.FileName of this tab: the path, or pipeSourceName
	base   *Buffer // the unfiltered table; its progress says whether the load is over
	note   string  // added to the footer once the load is over: files skipped at startup
	closed bool    // the tab was closed; a load still running reports nowhere

	// A load running in the background: its channels (nil after a synchronous
	// load), whether it has ended, and whether the tab is in front, read by the
	// load's goroutine to decide what to refresh.
	updates   <-chan bool
	done      <-chan error
	loadEnded bool
	inFront   atomic.Bool
	released  bool // the closed tab's memory was given back

	// The package variables, parked while another tab is in front.
	b, originalBuffer *Buffer
	isFiltered        bool
	activeFilters     map[int]FilterOptions
	wrappedColumns    map[int]int
	hiddenCols        map[int]bool
	edits             []edit
	loadStopped       bool
	userMovedCursor   bool
	bufferTable       *tview.Table
	mainPage          *tview.Frame
	mainView          *cellPreview
	content           *bufferContent
	fileNameStr       string
	cursorPosStr      string
	statusMessage     string
	cursorColumn      int
	searchQuery       string
	searchResults     []SearchResult
	searchIndex       int
}

// The open tabs in order. current is the index of the tab in front, -1 until
// the first one is shown. loaded is the tab whose state is in the package
// variables: the one in front, or, inside withTab, another one for a moment.
// Without tabs (tests drive the package variables directly) everything
// behaves as one tab that cannot be closed without quitting.
var (
	tabs    []*tab
	current = -1
	loaded  *tab
	tabIDs  int
)

// budget is the --memory limit shared by every open table, nil without one.
var budget *memoryBudget

// newTab prepares a tab for name with an empty buffer that reads with sep (0
// to detect the separator) within the shared memory budget. The frozen rows
// and columns are settled here, before the loader's goroutine reads them.
func newTab(name string, sep rune) *tab {
	tabIDs++
	buf := createNewBuffer()
	buf.sep = sep
	buf.budget = budget
	setupFreezeMode(buf)
	return &tab{
		id: tabIDs, name: name, base: buf, b: buf,
		activeFilters: map[int]FilterOptions{}, wrappedColumns: map[int]int{}, hiddenCols: map[int]bool{},
		searchIndex: -1,
	}
}

// page is the tab's page name in the UI container.
func (t *tab) page() string { return "tab" + strconv.Itoa(t.id) }

// park stores the package variables of the table in front into t.
func (t *tab) park() {
	t.b, t.originalBuffer, t.isFiltered, t.activeFilters = b, originalBuffer, isFiltered, activeFilters
	t.wrappedColumns, t.hiddenCols, t.edits = wrappedColumns, hiddenCols, edits
	t.loadStopped, t.userMovedCursor = loadStopped, userMovedCursor
	t.bufferTable, t.mainPage, t.mainView, t.content = bufferTable, mainPage, mainView, currentContent
	t.fileNameStr, t.cursorPosStr, t.statusMessage, t.cursorColumn = fileNameStr, cursorPosStr, statusMessage, currentCursorColumn
	t.searchQuery, t.searchResults, t.searchIndex = searchQuery, searchResults, currentSearchIndex
}

// restore loads t's state into the package variables.
func (t *tab) restore() {
	args.FileName = t.name
	b, originalBuffer, isFiltered, activeFilters = t.b, t.originalBuffer, t.isFiltered, t.activeFilters
	wrappedColumns, hiddenCols, edits = t.wrappedColumns, t.hiddenCols, t.edits
	loadStopped, userMovedCursor = t.loadStopped, t.userMovedCursor
	bufferTable, mainPage, mainView, currentContent = t.bufferTable, t.mainPage, t.mainView, t.content
	fileNameStr, cursorPosStr, statusMessage, currentCursorColumn = t.fileNameStr, t.cursorPosStr, t.statusMessage, t.cursorColumn
	searchQuery, currentSearchIndex = t.searchQuery, t.searchIndex
	setSearchResults(t.searchResults)
}

// withTab runs fn with t's state in the package variables: at once when t is
// the tab in front, otherwise with the variables swapped for the call and
// swapped back after. Calls nest.
func withTab(t *tab, fn func()) {
	if loaded == t {
		fn()
		return
	}
	prev := loaded
	if prev != nil {
		prev.park()
	}
	t.restore()
	loaded = t
	fn()
	t.park()
	loaded = prev
	if prev != nil {
		prev.restore()
	}
}

// hasEdits reports whether t has pending edits, wherever its state is.
func (t *tab) hasEdits() bool {
	if loaded == t {
		return dirty()
	}
	return len(t.edits) > 0
}

// title is the tab's label in the tab line: the file's base name, marked [+]
// while it has pending edits as the footer marks the file, and, while its
// load runs, how far it is: a percentage for a file, or "loading" for a pipe
// or a gzip file, whose size is unknown.
func (t *tab) title() string {
	name := filepath.Base(t.name)
	if t.hasEdits() {
		name += " [+]"
	}
	if p := &t.base.progress; !p.IsComplete.Load() {
		if p.TotalBytes.Load() > 0 {
			name += fmt.Sprintf(" %.0f%%", p.GetPercentage())
		} else {
			name += " loading"
		}
	}
	return name
}

// tabLine renders the tab line for the frame header, like vim's tabline: the
// tabs numbered from 1 with their titles, the one in front in the accent
// colour and bold.
func tabLine() string {
	labels := make([]string, len(tabs))
	for i, t := range tabs {
		label := " " + strconv.Itoa(i+1) + " " + tview.Escape(t.title()) + " "
		if i == current {
			label = theme.tag(theme.Accent) + "[::b]" + label + "[-:-:-]"
		}
		labels[i] = label
	}
	return strings.Join(labels, " ")
}

// refreshTabLine redraws the footer of the tab in front so the tab line shows
// the progress of the loads behind it.
func refreshTabLine() {
	if len(tabs) > 1 {
		drawFooterText(fileNameStr, statusMessage, cursorPosStr)
	}
}

// showTab brings tabs[i] to the front: the outgoing tab's state is parked,
// its transient input (a visual selection, a pending operator, count or
// chord) is dropped as vim drops them on a tab switch, and the incoming
// tab's page, focus and footer take over.
func showTab(i int) {
	if i < 0 || i >= len(tabs) {
		return
	}
	t := tabs[i]
	if loaded != t {
		if loaded != nil {
			visual = visualOff
			pendingOp, pendingOpRaw, pendingOpCount = "", 0, 0
			pendingCount = 0
			pendingChord, pendingAct = nil, ""
			chordGeneration++
			loaded.inFront.Store(false)
			loaded.park()
		}
		t.restore()
		loaded = t
	}
	t.inFront.Store(true)
	current = i
	UI.SwitchToPage(t.page())
	app.SetFocus(bufferTable)
	drawFooterText(fileNameStr, statusMessage, cursorPosStr)
}

// closeCurrentTab removes the tab in front and shows its right neighbour, or
// the left one when it was the last, as vim does. A load still running for
// it is told to stop; its memory is given back once it has.
func closeCurrentTab() {
	if current < 0 || len(tabs) < 2 {
		return
	}
	t := tabs[current]
	t.closed = true
	t.inFront.Store(false)
	t.base.stopLoad.Store(true)
	UI.RemovePage(t.page())
	tabs = append(tabs[:current], tabs[current+1:]...)
	next := min(current, len(tabs)-1)
	current, loaded = -1, nil // t's state stays in the variables until restore replaces it
	showTab(next)
	if t.done == nil || t.loadEnded {
		t.release()
	}
	drawFooterText(fileNameStr, "Closed "+filepath.Base(t.name), cursorPosStr)
}

// releaseThreshold is the estimated size from which a closed tab's memory is
// worth a full collection to hand back to the system.
const releaseThreshold = 64 << 20

// release gives a closed tab's memory back: its share of the budget at once,
// and the heap it held, which Go returns to the system only after a full
// collection, so one is asked for when the table was large.
func (t *tab) release() {
	if t.released {
		return
	}
	t.released = true
	large := t.base.getMemoryUsage() >= releaseThreshold
	t.base.releaseBudget()
	if s := t.base.stream; s != nil {
		s.close()
	}
	if large {
		go rtdebug.FreeOSMemory()
	}
}

// otherTabs reports whether there is another tab to switch to and says so in
// the footer otherwise.
func otherTabs() bool {
	if len(tabs) > 1 {
		return true
	}
	drawFooterText(fileNameStr, "No other tab is open", cursorPosStr)
	return false
}

// nextTab shows the next tab, wrapping to the first; with a count, tab N (the
// first is 1, a count past the end lands on the last), as vim's gt.
func nextTab(rawCount int) {
	if !otherTabs() {
		return
	}
	if rawCount > 0 {
		showTab(min(rawCount, len(tabs)) - 1)
		return
	}
	showTab((current + 1) % len(tabs))
}

// prevTab shows the tab count places back, wrapping to the last, as vim's gT.
func prevTab(count int) {
	if !otherTabs() {
		return
	}
	n := len(tabs)
	showTab(((current-count)%n + n) % n)
}

// errEmpty marks an input with nothing to show; the message says which way.
var errEmpty = errors.New("empty")

// emptyReason reports why buf has nothing to show, or nil. The loader may
// still be running, so the row count is read under the lock.
func emptyReason(buf *Buffer) error {
	switch rows := buf.rowCount(); {
	case rows == 0:
		return fmt.Errorf("%w (no rows)", errEmpty)
	case rows-buf.rowFreeze <= 0:
		return fmt.Errorf("%w (only header, no data rows)", errEmpty)
	}
	return nil
}

// openTab loads name (or pipe, when it is not nil) into a new tab and builds
// the tab's widgets. With --async the load goes on in the background, to be
// followed by startLoadHandler, and the tab comes back once the first rows
// are in; the error is the one that stopped the load before any were, or
// errEmpty for an input with nothing to show.
func openTab(name string, pipe io.Reader, sep rune) (*tab, error) {
	t := newTab(name, sep)
	buf := t.base
	stream := shouldStream(name, pipe)
	var loadErr error // a memory-limit stop; other errors return at once
	if args.AsyncLoad {
		loader := func(b *Buffer, updateChan chan<- bool, doneChan chan<- error) {
			switch {
			case pipe != nil:
				go loadPipeToBufferAsync(pipe, b, updateChan, doneChan)
			case stream:
				go loadStreamAsync(name, b, updateChan, doneChan)
			default:
				go loadFileToBufferAsync(name, b, updateChan, doneChan)
			}
		}
		updates, done, err := loadDataAsync(loader, buf)
		if err != nil {
			return nil, err
		}
		t.updates, t.done = updates, done
		if emptyReason(buf) != nil {
			// No rows after the first signal means the loader has nothing more
			// to read (the input is empty, or the memory limit stopped it before
			// the first row); wait for its outcome, which says which.
			loadErr = <-done
			t.updates, t.done = nil, nil
			if loadErr != nil && !errors.Is(loadErr, errMemoryLimit) {
				return nil, loadErr
			}
		}
	} else {
		switch {
		case pipe != nil:
			loadErr = loadPipeToBuffer(pipe, buf)
		case stream:
			loadErr = loadStream(name, buf)
		default:
			loadErr = loadFileToBuffer(name, buf)
		}
		if loadErr != nil && !errors.Is(loadErr, errMemoryLimit) {
			return nil, loadErr
		}
	}
	if reason := emptyReason(buf); reason != nil {
		if loadErr != nil {
			return nil, fmt.Errorf("%w (%v before any row was read)", errEmpty, loadErr)
		}
		return nil, reason
	}
	if loadErr != nil {
		// Rows loaded before the cap stay viewable; say so in the footer.
		t.statusMessage = "Stopped after " + strconv.Itoa(buf.rowCount()) + " rows: " + loadErr.Error()
		t.loadStopped = true
	}
	withTab(t, buildTabView)
	return t, nil
}

// openTabs opens one tab per name (or one for pipe, when it is not nil),
// builds the UI and shows the first tab. An input with nothing to show is an
// error when it is the only one; among several it is skipped and noted in
// the footer, and the notes come back. Loads still running report to their
// tabs from here on.
func openTabs(names []string, pipe io.Reader, sep rune) (skipped []string, err error) {
	for _, name := range names {
		t, err := openTab(name, pipe, sep)
		switch {
		case err == nil:
			tabs = append(tabs, t)
		case errors.Is(err, errEmpty) && len(names) > 1:
			skipped = append(skipped, "Skipped "+filepath.Base(name)+": "+err.Error())
		case len(names) > 1:
			return skipped, fmt.Errorf("%s: %w", name, err)
		default:
			return skipped, err
		}
	}
	if len(tabs) == 0 {
		return skipped, nil
	}
	buildUI()
	showTab(0)
	if len(skipped) > 0 {
		note := strings.Join(skipped, "; ")
		tabs[0].note = note
		drawFooterText(fileNameStr, joinStatus(statusMessage, note), cursorPosStr)
	}
	for _, t := range tabs {
		if t.done != nil {
			t.startLoadHandler()
		}
	}
	return skipped, nil
}

// joinStatus appends note to a footer status, replacing the idle "All Done".
func joinStatus(status, note string) string {
	if status == "" || status == "All Done" {
		return note
	}
	return status + "; " + note
}

// buildUI creates the page container with one page per tab, none shown yet
// (showTab does that), and installs the application-level hooks.
func buildUI() {
	// Keep a handle on the screen so yanks can emit the OSC 52 clipboard escape.
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		screenRef = screen
		return false
	})
	app.SetInputCapture(handleAppKey)
	UI = tview.NewPages()
	for _, t := range tabs {
		UI.AddPage(t.page(), t.mainView, true, false)
	}
}

// loadTickInterval is how often a loading tab in front is refreshed, and
// tabLineRefreshTicks how many of those pass between two refreshes of the tab
// line for a tab loading behind, whose rows nobody sees yet.
const (
	loadTickInterval    = 20 * time.Millisecond
	tabLineRefreshTicks = 25
)

// startLoadHandler follows t's background load. While it runs, the table and
// footer are refreshed whenever t is in front, and the tab line now and then
// while it is behind; when it ends, the outcome goes to t's footer, in front
// or parked. Everything that touches the UI runs on the UI goroutine through
// QueueUpdateDraw.
func (t *tab) startLoadHandler() {
	updates, done := t.updates, t.done
	go func() {
		ticker := time.NewTicker(loadTickInterval)
		defer ticker.Stop()
		behind := 0
		for {
			select {
			case <-updates:
				// Rows arrived; the next tick shows them.
			case err := <-done:
				app.QueueUpdateDraw(func() { t.loadFinished(err) })
				return
			case <-ticker.C:
				if t.inFront.Load() {
					behind = 0
					app.QueueUpdateDraw(t.loadTick)
				} else if behind++; behind%tabLineRefreshTicks == 0 {
					app.QueueUpdateDraw(refreshTabLine)
				}
			}
		}
	}()
}

// loadTick refreshes a loading tab in front: the table shows the rows read so
// far, the cursor stays on the first data row until the user moves it, and
// the footer shows the progress bar (files) or the row count (pipes, gzip).
func (t *tab) loadTick() {
	if t.closed || loaded != t {
		return
	}
	drawBuffer(b, bufferTable)
	if !userMovedCursor {
		row, col := bufferTable.GetSelection()
		if first := firstDataRow(b); row != first {
			bufferTable.Select(first, col)
		}
	}
	base := baseBuffer()
	verb := "Loading"
	if base.streamed() {
		verb = "Indexing"
	}
	if base.progress.TotalBytes.Load() > 0 {
		updateFooterWithStatus(fmt.Sprintf("%s... %s", verb, makeProgressBar(base.progress.GetPercentage(), 15)))
	} else {
		updateFooterWithStatus(verb + "... " + strconv.Itoa(base.rowCount()) + " rows")
	}
}

// loadFinished reports the outcome of t's load in its footer and keeps
// whatever was loaded viewable; the tab need not be in front. The load of a
// closed tab was cancelled: its memory goes back instead.
func (t *tab) loadFinished(err error) {
	t.loadEnded = true
	if t.closed {
		t.release()
		return
	}
	withTab(t, func() {
		rows := strconv.Itoa(baseBuffer().rowCount())
		status := "Loaded " + rows + " rows"
		if baseBuffer().streamed() {
			status = "Indexed " + rows + " rows; streamed from disk, read-only"
		}
		if err != nil {
			status = "Stopped after " + rows + " rows: " + err.Error()
		}
		if t.note != "" {
			status += "; " + t.note
		}
		loadStopped = err != nil
		drawBuffer(b, bufferTable)
		updateFooterWithStatus(status)
	})
	if loaded != t {
		refreshTabLine() // the tab in front shows this one done
	}
}

// requestQuitAll is Ctrl-C: it quits, closing every tab, and asks first when
// tabs have unwritten changes. The dialog lists them; Write writes each tab
// in turn before quitting, and a write that fails leaves that tab in front
// with the reason in its footer. When a tab's changes cannot be written at
// all, the dialog only offers to discard. With one tab open it is q.
func requestQuitAll() {
	if len(tabs) <= 1 {
		requestQuit()
		return
	}
	if UI.HasPage("quitDialog") {
		return
	}
	var lines []string
	blocked := false
	for _, t := range tabs {
		if !t.hasEdits() {
			continue
		}
		withTab(t, func() {
			line := filepath.Base(t.name) + ": " + editSummary()
			if reason := writeBlocker(); reason != "" {
				line += " (cannot be written: " + reason + ")"
				blocked = true
			}
			lines = append(lines, line)
		})
	}
	if len(lines) == 0 {
		stopApp()
		return
	}
	cancelOperator()
	text := fmt.Sprintf("Unwritten changes in %d of %d tabs:\n\n%s\n\n", len(lines), len(tabs), strings.Join(lines, "\n"))
	buttons := []string{"Write", "Discard", "Cancel"}
	if blocked {
		text += "Quit and discard them?  (d discards, c or Esc stays)"
		buttons = []string{"Discard", "Cancel"}
	} else {
		text += "Write them and quit?  (w writes, d discards, c or Esc stays)"
	}
	openQuitDialog(text, buttons, func(label string) {
		switch label {
		case "Write":
			if writeAllTabs() {
				stopApp()
			}
		case "Discard":
			stopApp()
		}
	})
}

// writeAllTabs writes every tab with pending edits, bringing each to the
// front for it, and reports whether all were written; the tab whose write
// failed stays in front with the reason in its footer.
func writeAllTabs() bool {
	for i := 0; i < len(tabs); i++ {
		if !tabs[i].hasEdits() {
			continue
		}
		showTab(i)
		if !writeTable() {
			return false
		}
	}
	return true
}
