package app

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// buildCursorPosStr builds the cursor position string (without filter info now)
func buildCursorPosStr(row, column int) string {
	posStr := "Column Type: " + type2name(b.getColType(column)) + "  |  " + strconv.Itoa(row) + "," + strconv.Itoa(column) + "  "
	if hiddenCols[column] {
		posStr = "hidden: " + columnTitle(column) + "  |  " + posStr
	}
	if marker := previewShow.marker(); marker != "" {
		posStr = marker + "  |  " + posStr
	}
	return posStr
}

// buildFilterInfoStr builds the filter information string for the top strip
// Shows all active filters or current column filter when cursor is on a filtered column
func buildFilterInfoStr(currentColumn int) string {
	if !isFiltered || len(activeFilters) == 0 {
		return "" // No filter active
	}

	// Check if current column has a filter
	if opts, hasFilter := activeFilters[currentColumn]; hasFilter {
		// Get column name if available
		columnName := fmt.Sprintf("Column %d", currentColumn)
		if b.rowFreeze > 0 {
			if name, ok := b.cellAt(0, currentColumn); ok {
				columnName = name
			}
		}

		return fmt.Sprintf("Filter Active: [%s] %s  |  %d filters total  |  Press 'r' to remove this filter", columnName, describeFilter(opts), len(activeFilters))
	}

	// Show summary if cursor is not on a filtered column
	return fmt.Sprintf("%d filters active  |  Navigate to filtered column and press 'r' to remove", len(activeFilters))
}

// describeFilter renders a filter for the footer strip.
func describeFilter(opts FilterOptions) string {
	if isUniqueOperator(opts.Operator) {
		return opts.Operator
	}
	return fmt.Sprintf("%s %q", opts.Operator, opts.Query)
}

// filterOrder lists the filtered columns in the order the filters apply: the
// value filters first, in column order, then the unique filters, so
// duplicates are removed from the rows that match rather than before matching.
func filterOrder(filters map[int]FilterOptions) []int {
	cols := make([]int, 0, len(filters))
	for c := range filters {
		cols = append(cols, c)
	}
	sort.Slice(cols, func(i, j int) bool {
		ui, uj := isUniqueOperator(filters[cols[i]].Operator), isUniqueOperator(filters[cols[j]].Operator)
		if ui != uj {
			return !ui
		}
		return cols[i] < cols[j]
	})
	return cols
}

// applyActiveFilters runs every active filter over base in filterOrder.
func applyActiveFilters(base *Buffer) *Buffer {
	out := base
	for _, c := range filterOrder(activeFilters) {
		out = out.filterByColumn(c, activeFilters[c])
	}
	return out
}

// deriveFilteredView applies the active filters to the unfiltered table and
// hands the view to then: at once for a table in memory, or, for a streamed
// file, after a background pass over it with its progress in the footer and
// Esc to cancel, in which case cancelled runs instead.
func deriveFilteredView(then func(view *Buffer), cancelled func()) {
	base := baseBuffer()
	if !base.streamed() {
		drawFooterText(fileNameStr, "Filtering...", cursorPosStr)
		if app != nil {
			app.ForceDraw()
		}
		then(applyActiveFilters(base))
		return
	}
	cols, filters := filterOrder(activeFilters), maps.Clone(activeFilters)
	runPass("Filtering", func(p *pass) *Buffer { return filterStream(base, cols, filters, p) }, then, cancelled)
}

// searchTable finds the cells of buf matching the query and hands them to
// then: at once for a table in memory, or after a background pass over a
// streamed file.
func searchTable(buf *Buffer, query string, useRegex, caseSensitive bool, then func([]SearchResult)) {
	if !buf.streamed() {
		then(performSearch(buf, query, useRegex, caseSensitive))
		return
	}
	runPass("Searching", func(p *pass) []SearchResult { return searchStream(buf, query, useRegex, caseSensitive, p) }, then, nil)
}

// reapplyFilters re-derives the view after a filter was removed: the
// unfiltered table comes back when none is left, with cleared in the footer,
// else the remaining filters apply and removed says how many rows match. The
// cursor goes to row (clamped) in column. A cancelled pass on a streamed file
// puts the removed filter back.
func reapplyFilters(row, column int, cleared string, removed func(matches int) string, was FilterOptions) {
	if len(activeFilters) == 0 {
		b = originalBuffer
		isFiltered = false
		drawBuffer(b, bufferTable)
		bufferTable.Select(clampRow(row, b), column)
		drawFooterText(fileNameStr, cleared, cursorPosStr)
		return
	}
	deriveFilteredView(func(view *Buffer) {
		b = view
		drawBuffer(b, bufferTable)
		bufferTable.Select(clampRow(row, b), column)
		drawFooterText(fileNameStr, removed(b.rowLen-b.rowFreeze), cursorPosStr)
	}, func() { activeFilters[column] = was })
}

// searchMatchSet mirrors searchResults as a set so cell styling is O(1) per cell.
var searchMatchSet map[SearchResult]struct{}

// setSearchResults replaces the current search results and rebuilds the lookup set.
func setSearchResults(results []SearchResult) {
	searchResults = results
	searchMatchSet = make(map[SearchResult]struct{}, len(results))
	for _, r := range results {
		searchMatchSet[r] = struct{}{}
	}
}

// bufferContent adapts a Buffer to tview's TableContent so the table only
// materialises the cells it actually draws. Header, frozen-column, filter and
// search styling is applied per cell on demand. Cells are cached for the
// duration of one frame: tview asks for each visible cell several times per
// draw, and the cached object keeps the screen position tview records on it.
type bufferContent struct {
	tview.TableContentReadOnly
	b      *Buffer
	cells  map[[2]int]*tview.TableCell
	selRow int // the row under the cursor during this frame; its cells are tinted
}

// currentContent is the content the table is showing; cellPreview uses it to
// find where the selected cell was drawn.
var currentContent *bufferContent

// beginFrame drops the cells cached during the previous draw and notes the
// cursor's row, so the whole row can be tinted and followed across a wide
// table while the selected cell keeps the bright cursor style.
func (c *bufferContent) beginFrame() {
	c.cells = make(map[[2]int]*tview.TableCell, len(c.cells))
	c.selRow = -1
	if bufferTable != nil {
		c.selRow, _ = bufferTable.GetSelection()
	}
}

// drawnCell returns the cell object drawn at row, col in the current frame.
func (c *bufferContent) drawnCell(row, col int) *tview.TableCell {
	return c.cells[[2]int{row, col}]
}

func (c *bufferContent) GetRowCount() int {
	c.b.mu.RLock()
	defer c.b.mu.RUnlock()
	return c.b.rowLen
}

func (c *bufferContent) GetColumnCount() int {
	c.b.mu.RLock()
	defer c.b.mu.RUnlock()
	return c.b.colLen
}

func (c *bufferContent) GetCell(r, col int) *tview.TableCell {
	if cell, ok := c.cells[[2]int{r, col}]; ok {
		return cell
	}
	cell := c.buildCell(r, col)
	if c.cells == nil {
		c.cells = make(map[[2]int]*tview.TableCell)
	}
	c.cells[[2]int{r, col}] = cell
	return cell
}

// buildCell creates the styled cell for row r, column col.
func (c *bufferContent) buildCell(r, col int) *tview.TableCell {
	b := c.b
	b.mu.RLock()
	if r < 0 || col < 0 || r >= b.rowLen || col >= b.colLen {
		b.mu.RUnlock()
		return nil
	}
	rowFreeze, colFreeze := b.rowFreeze, b.colFreeze
	colWidth := 0
	if col < len(b.colWidth) {
		colWidth = b.colWidth[col]
	}
	b.mu.RUnlock()
	cellText, ok := b.cellAt(r, col)
	if !ok {
		return nil
	}

	color := theme.Text
	backgroundColor := theme.Background
	attributes := tcell.AttrNone
	alignment := tview.AlignLeft

	// Check if this is a header row/column (frozen area)
	isHeaderRow := r < rowFreeze && args.Header != -1 && args.Header != 2
	isHeaderCol := col < colFreeze

	if isHeaderRow {
		// Header row: a raised panel, like tmux's mode-style background
		color = theme.Text
		backgroundColor = theme.Panel
		attributes = tcell.AttrBold
		alignment = tview.AlignCenter

		// Add filter indicator if this column has a filter applied
		if isFiltered {
			if _, hasFilter := activeFilters[col]; hasFilter {
				cellText = "* " + cellText + " *"
				backgroundColor = theme.Alert
				color = theme.Background
			}
		}
	} else if isHeaderCol {
		// Frozen column: accent text, like the current window in tmux
		color = theme.Accent
		attributes = tcell.AttrBold
	}
	if !isHeaderRow && r == c.selRow {
		// The cursor's row, subtly, so it can be followed across the table
		backgroundColor = theme.CursorLine
	}

	// Search match highlighting overrides header styling
	if searchQuery != "" {
		if _, hit := searchMatchSet[SearchResult{Row: r, Col: col}]; hit {
			if currentSearchIndex >= 0 && currentSearchIndex < len(searchResults) &&
				searchResults[currentSearchIndex].Row == r &&
				searchResults[currentSearchIndex].Col == col {
				// Current match (also the selected cell): accent block
				backgroundColor = theme.Accent
				color = theme.Background
				attributes = tcell.AttrBold
			} else {
				// Other matches: tmux mode-style highlight
				backgroundColor, color, _ = theme.highlightStyle().Decompose()
				attributes = tcell.AttrNone
			}
		}
	}

	// Cells inside a visual selection (the cursor itself is drawn by tview)
	if inVisual(r, col) {
		backgroundColor = theme.Selection
		color = theme.Text
	}

	// Width-limited columns are truncated with an ellipsis
	maxWidth := 0
	if width, isLimited := wrappedColumns[col]; isLimited {
		maxWidth = width
		cellText = truncateText(cellText, maxWidth)
		if colWidth > maxWidth {
			colWidth = maxWidth
		}
	}
	// A hidden column collapses to a dimmed marker, like a closed fold
	if hiddenCols[col] {
		cellText, colWidth, maxWidth = foldMarker, 1, 1
		color, attributes = theme.Dim, tcell.AttrNone
	}

	// Pad to the column's widest cell so the column keeps the same width
	// whichever rows are on screen; headers are centred within it.
	cellText = padToWidth(cellText, colWidth, isHeaderRow)

	cell := tview.NewTableCell(cellText).
		SetTextColor(color).
		SetBackgroundColor(backgroundColor).
		SetAttributes(attributes).
		SetAlign(alignment).
		SetExpansion(1)

	if maxWidth > 0 {
		cell.SetMaxWidth(maxWidth)
	}
	if isHeaderRow {
		// The frozen header is always on screen, so selecting it would not move
		// the view; keep the cursor on data rows (tview skips these cells too).
		cell.SetSelectable(false)
	}
	return cell
}

// displayColumnWidth is the width tview gives column c: the widest cell, cut
// by a width limit, one cell for a hidden column, and four more on a header
// marked as filtered.
func displayColumnWidth(c int) int {
	if hiddenCols[c] {
		return 1
	}
	w := b.columnWidth(c)
	if limit, ok := wrappedColumns[c]; ok && w > limit {
		w = limit
	}
	if isFiltered {
		if _, ok := activeFilters[c]; ok {
			w += 4
		}
	}
	return w
}

// pinColumnOffset sets the horizontal offset so that a forward layout of the
// table, tw cells wide, shows the selected column in full. tview lays a frame out "ending with
// the selection" only right after Select; the next redraw packs forward from
// the offset, so a column that was cut on the left moves to the right and the
// selected column can end up cut instead, shifting the view between two
// frames with no motion at all. Choosing an offset whose forward layout
// includes the selection keeps every frame the same.
func pinColumnOffset(tw int) {
	if bufferTable == nil || b == nil {
		return
	}
	fixed, cols := b.colFreeze, b.colCount()
	_, sel := bufferTable.GetSelection()
	if tw <= 0 || sel < fixed || sel >= cols {
		return
	}
	rowOff, colOff := bufferTable.GetOffset()
	fixedWidth := 0
	for c := 0; c < fixed && c < cols; c++ {
		fixedWidth += displayColumnWidth(c) + 1
	}
	// includes reports whether column sel is shown in full when the columns
	// from fixed+off onwards are laid out after the fixed ones.
	includes := func(off int) bool {
		used := fixedWidth
		for c := fixed + off; c <= sel; c++ {
			w := displayColumnWidth(c)
			if c == sel {
				return used+w <= tw
			}
			used += w + 1
			if used >= tw {
				return false
			}
		}
		return false
	}
	want := colOff
	if sel < fixed+colOff {
		want = sel - fixed
	} else {
		for want <= sel-fixed && !includes(want) {
			want++
		}
		if want > sel-fixed {
			want = sel - fixed // wider than the screen: start with the selection
		}
	}
	if want != colOff {
		bufferTable.SetOffset(rowOff, want)
	}
}

// firstDataRow returns the first selectable row of b: the row after the
// frozen header, or 0 when no header is frozen. The row count is read under
// the lock: a loader may be appending rows meanwhile.
func firstDataRow(b *Buffer) int {
	return clampInt(b.rowFreeze, 0, b.rowCount()-1)
}

// clampRow keeps a target row within the selectable data rows of b, so any
// motion or count that overshoots lands on the first or last data row.
func clampRow(row int, b *Buffer) int {
	return clampInt(row, firstDataRow(b), b.rowCount()-1)
}

// padToWidth pads text with spaces to width display cells, centred for
// headers and left-aligned otherwise. Wider text is returned unchanged.
func padToWidth(text string, width int, center bool) string {
	gap := width - displayWidth(text)
	if gap <= 0 {
		return text
	}
	if !center {
		return text + strings.Repeat(" ", gap)
	}
	left := gap / 2
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", gap-left)
}

// drawBuffer points the table at b. Cells are produced lazily by bufferContent,
// so this is cheap to call after every state change (load tick, sort, filter, search).
func drawBuffer(b *Buffer, t *tview.Table) {
	currentContent = &bufferContent{b: b, selRow: -1}
	t.SetContent(currentContent)
}

// maxCountPrefix caps a typed count so further digits cannot overflow it.
const maxCountPrefix = 1_000_000_000

// pushCountDigit folds a typed digit into the pending vim-style count prefix.
// It reports false for non-digits and for a leading '0', which keeps its
// "first column" binding.
func pushCountDigit(r rune) bool {
	if r < '0' || r > '9' {
		return false
	}
	if r == '0' && pendingCount == 0 {
		return false
	}
	// Ignore digits that would push the count past the cap; the check happens
	// before the multiplication so it cannot overflow on 32-bit targets.
	if digit := int(r - '0'); pendingCount <= (maxCountPrefix-digit)/10 {
		pendingCount = pendingCount*10 + digit
	}
	return true
}

// takeCount consumes the pending count prefix. raw is 0 when none was typed;
// count is raw, or 1 when none was typed, ready to use as a repeat factor.
func takeCount() (raw, count int) {
	raw = pendingCount
	pendingCount = 0
	if raw < 1 {
		return raw, 1
	}
	return raw, raw
}

// wrapCol maps a column index onto [0, n) so horizontal motion wraps around
// the table: h at the first column lands on the last, l at the last on the first.
func wrapCol(col, n int) int {
	if n <= 0 {
		return 0
	}
	return ((col % n) + n) % n
}

// clampInt limits v to [lo, hi]; hi below lo yields lo.
func clampInt(v, lo, hi int) int {
	if v > hi {
		v = hi
	}
	if v < lo {
		v = lo
	}
	return v
}

// ggTimeout is how long after one 'g' a second 'g' still counts as the gg chord.
const ggTimeout = 500 * time.Millisecond

// secondGPress records a 'g' key press and reports whether it completes a gg
// chord. State lives in a timestamp so no goroutine is needed to expire it.
func secondGPress() bool {
	if !lastGPress.IsZero() && time.Since(lastGPress) < ggTimeout {
		lastGPress = time.Time{}
		return true
	}
	lastGPress = time.Now()
	return false
}

// footerUpdateThrottle spaces out footer rebuilds while the selection changes
// quickly, as during a mouse drag or the progressive load. Visual mode is not
// throttled: its footer text is the size of the selection, which must follow
// every move. A motion typed at the keyboard redraws the footer afterwards in
// any case (see dispatch).
const footerUpdateThrottle = 50 * time.Millisecond

var lastFooterUpdate time.Time

// selectionChanged is the table's selection-changed callback: it tracks the
// cursor for the footer and the preview box, and keeps the visual selection's
// size in the status text.
func selectionChanged(row, column int) {
	if !userMovedCursor && (row != firstDataRow(b) || column != 0) {
		userMovedCursor = true
	}
	currentCursorColumn = column
	cursorPosStr = buildCursorPosStr(row, column)
	updateCellPreview(row, column)
	if visual != visualOff {
		statusMessage = visualStatus()
	}
	now := time.Now()
	if visual != visualOff || now.Sub(lastFooterUpdate) >= footerUpdateThrottle {
		lastFooterUpdate = now
		drawFooterText(fileNameStr, statusMessage, cursorPosStr)
	}
}

// drawFooterText rebuilds the footer (file name, status, cursor position), the
// tab line when several files are open and the filter strip, both above the
// table, and records cstr as the current status message.
func drawFooterText(lstr, cstr, rstr string) {
	if cstr != statusMessage {
		armNotice(cstr)
	}
	statusMessage = cstr
	if mainPage == nil {
		return
	}
	mainPage.Clear()

	if len(tabs) > 1 {
		mainPage.AddText(tabLine(), true, tview.AlignLeft, theme.Dim)
	}

	// Filter info strip at top when a filter is active
	if filterInfoStr := buildFilterInfoStr(currentCursorColumn); filterInfoStr != "" {
		mainPage.AddText(filterInfoStr, true, tview.AlignCenter, theme.Alert)
	}

	mainPage.AddText(lstr, false, tview.AlignLeft, theme.Accent).
		AddText(cstr, false, tview.AlignCenter, theme.Text).
		AddText(footerRight(rstr), false, tview.AlignRight, theme.Dim)
}

// Footer notices. A status that reports what an action did (a yank, a
// removal, a filter's result) is a notice: it stays noticeTTL, then the
// footer settles on the idle text. Mode indicators ("-- VISUAL --", the
// editor's modes) and progress texts ("Loading...", "Filtering... 37%") are
// not notices: they stay until the mode or the work ends. The timer is armed
// only when the text changes, so redrawing the footer after a motion does not
// keep a notice alive, and it clears the notice only if the footer still
// shows it, so a newer status is never wiped. [footer] notice_seconds sets
// the time; 0 keeps every status until the next one.
var noticeTTL = defaultNoticeSeconds * time.Second

// defaultNoticeSeconds is how long a notice stays by default.
const defaultNoticeSeconds = 5

// persistentStatus reports whether a status is a mode indicator or a
// progress text, which the fade leaves alone.
func persistentStatus(text string) bool {
	return strings.HasPrefix(text, "-- ") || strings.Contains(text, "...")
}

// idleStatus is what the footer shows when nothing is being reported: the
// visual selection's size while one stands, the edits pending when there
// are some, else the table's "All Done".
func idleStatus() string {
	if visual != visualOff {
		return visualStatus()
	}
	if pending := pendingStatus(); pending != "" {
		return pending
	}
	return "All Done"
}

// armNotice starts the fade of a status that has just replaced another, for
// the tab in front, unless it is a mode indicator, a progress text or already
// the idle text. Timers only run in a running application, as the chord
// timer does.
func armNotice(text string) {
	if noticeTTL <= 0 || !uiRunning.Load() || persistentStatus(text) || text == idleStatus() {
		return
	}
	owner := loaded
	time.AfterFunc(noticeTTL, func() {
		if !uiRunning.Load() {
			return
		}
		app.QueueUpdateDraw(func() { expireNotice(owner, text) })
	})
}

// expireNotice takes the notice text off the footer of owner (nil for the
// table without tabs) if the footer still shows it, and puts the idle text
// there; a tab closed meanwhile is left alone.
func expireNotice(owner *tab, text string) {
	if owner != nil && owner.closed {
		return
	}
	withTab(owner, func() {
		if statusMessage == text {
			drawFooterText(fileNameStr, idleStatus(), cursorPosStr)
		}
	})
}

// pageRows returns the number of data rows the table can show, at least 1.
func pageRows(t *tview.Table) int {
	_, _, _, height := t.GetInnerRect()
	if rows := height - b.rowFreeze; rows > 1 {
		return rows
	}
	return 1
}

// halfPageRows returns half the number of rows the table can show, at least 1.
func halfPageRows(t *tview.Table) int {
	if half := pageRows(t) / 2; half > 1 {
		return half
	}
	return 1
}

// add stats data to stats table
func drawStats(s statsSummary, t *tview.Table) {
	t.Clear()
	summaryData := s.getSummaryData()
	rows, cols := len(summaryData), len(summaryData[0])

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			color := theme.Text
			backgroundColor := theme.Background

			// Alternate row colors for better readability
			if r%2 == 1 {
				backgroundColor = theme.Stripe
			}

			// Highlight stat labels with the accent color
			if c == 0 {
				color = theme.Accent
			}

			t.SetCell(r, c,
				tview.NewTableCell(summaryData[r][c]).
					SetTextColor(color).
					SetBackgroundColor(backgroundColor).
					SetAlign(tview.AlignLeft))
		}
	}
}

// buildTabView creates the widgets of the table in b: the table itself, the
// frame with the footer, and the floating preview, all in the package
// variables (a tab parks them; buildUI puts the view on a page). The footer
// starts with the status in statusMessage, "All Done" when there is none.
func buildTabView() {
	//bufferTable init with modern styling
	bufferTable = tview.NewTable()
	bufferTable.SetSelectable(true, true)
	bufferTable.SetBorders(false)
	bufferTable.SetSeparator(tview.Borders.Vertical) // Add subtle vertical separators
	bufferTable.SetBordersColor(theme.Border)
	bufferTable.SetBackgroundColor(theme.Background)
	bufferTable.SetFixed(b.rowFreeze, b.colFreeze)
	bufferTable.Select(firstDataRow(b), 0)
	bufferTable.SetSelectedStyle(theme.selectedStyle())

	// Auto-detect and wrap long columns (sample first 100 rows, threshold 50 characters)
	detectAndWrapLongColumns(b, 100, 50)

	drawBuffer(b, bufferTable)

	//main page init with modern styling
	cursorPosStr = buildCursorPosStr(firstDataRow(b), 0) //footer right
	if statusMessage == "" {
		statusMessage = "All Done"
	}
	fileNameStr = footerFileName() //footer left

	mainPage = tview.NewFrame(bufferTable).
		SetBorders(0, 0, 0, 0, 0, 0)
	mainPage.SetBackgroundColor(theme.Background)
	mainView = newCellPreview(mainPage)

	// The footer, with the filter strip above the table when a filter is active
	drawFooterText(fileNameStr, statusMessage, cursorPosStr)

	bufferTable.SetSelectionChangedFunc(selectionChanged)

	//bufferTable HotKey Event: every key goes through the keymap
	bufferTable.SetInputCapture(handleTableKey)
	bufferTable.SetMouseCapture(handleTableMouse)
}

// cellUnderPointer returns the row and column of the table cell drawn at
// screen position x, y in the current frame; ok is false where no cell is:
// the blank area below the last row or right of the last column, and the
// frame around the table. The separator right of a cell counts as the cell.
func cellUnderPointer(x, y int) (row, col int, ok bool) {
	if currentContent == nil {
		return 0, 0, false
	}
	for pos, cell := range currentContent.cells {
		if cell == nil {
			continue
		}
		cx, cy, cw := cell.GetLastPosition()
		if cw > 0 && y == cy && x >= cx && x <= cx+cw {
			return pos[0], pos[1], true
		}
	}
	return 0, 0, false
}

// mouseDrag is the state of a selection made with the mouse, after Claude
// Code's: the primary button went down on a cell, and once the pointer moves
// with it held the cells from that anchor to the pointer are selected through
// visual mode, exactly as v and the motions would select them.
var mouseDrag struct {
	pressed  bool // the button is down on a data cell
	row, col int  // the cell it went down on: the anchor
	dragging bool // the pointer has moved since
}

// dragTarget returns the cell a drag should extend to for pointer position
// x, y: the row and column drawn there, and where nothing is drawn (past
// the edges of the table, the blank area below the last row) one row or
// column further than the cursor in that direction, so a drag that leaves
// the table scrolls it a step per move. The header row stands for the first
// data row.
func dragTarget(x, y int) (row, col int) {
	row, col = bufferTable.GetSelection()
	tx, ty, tw, _ := bufferTable.GetInnerRect()
	switch r, ok := rowAt(y); {
	case ok:
		row = max(r, firstDataRow(b))
	case y < ty+b.rowFreeze:
		row--
	default:
		row++
	}
	switch c, ok := columnAt(x); {
	case ok:
		col = c
	case x < tx:
		col--
	case x >= tx+tw:
		col++
	}
	return clampRow(row, b), clampInt(col, 0, b.colCount()-1)
}

// rowAt returns the table row drawn on screen row y this frame.
func rowAt(y int) (row int, ok bool) {
	if currentContent == nil {
		return 0, false
	}
	for pos, cell := range currentContent.cells {
		if cell == nil {
			continue
		}
		if _, cy, cw := cell.GetLastPosition(); cw > 0 && cy == y {
			return pos[0], true
		}
	}
	return 0, false
}

// columnAt returns the table column drawn across screen column x this frame
// (the separator right of a cell counts as the cell).
func columnAt(x int) (col int, ok bool) {
	if currentContent == nil {
		return 0, false
	}
	for pos, cell := range currentContent.cells {
		if cell == nil {
			continue
		}
		if cx, _, cw := cell.GetLastPosition(); cw > 0 && x >= cx && x <= cx+cw {
			return pos[1], true
		}
	}
	return 0, false
}

// handleTableMouse is the table's mouse capture. Nothing reaches the table
// while a cell is being edited, so the editor stays on its cell instead of
// the table scrolling away underneath it. A click that lands on no data cell
// (the blank area below the last row or right of the last column, the frozen
// header) is swallowed: tview would select the cell it computes there, row -1
// past the end, and the next draw would clamp that to the first row and
// scroll to it, so the click that merely brings the terminal back to the
// front lost the cursor. Otherwise: a click selects the cell (and clears a
// standing selection); a double click edits it; a drag selects a block and,
// with copy_on_select, copies it on release, leaving the selection standing
// as v would, so y, d, x and p still act on it and Esc or a click clears it;
// a right click clears the selection or, without one, pastes over the cell;
// a middle click pastes; the wheel moves a row, or a column sideways.
func handleTableMouse(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
	if cellEdit != nil {
		return action, nil
	}
	x, y := event.Position()
	switch action {
	case tview.MouseMove:
		if !mouseDrag.pressed || event.Buttons()&tcell.ButtonPrimary == 0 {
			return action, event // hover, or a move with another button
		}
		if !mouseDrag.dragging {
			mouseDrag.dragging = true
			userMovedCursor = true
			visual = visualOff // a new drag replaces a standing selection
			bufferTable.Select(mouseDrag.row, mouseDrag.col)
			startVisual(visualBlock)
		}
		// From the anchor on the first move, then from the cursor.
		row, col := dragTarget(x, y)
		if r, c := bufferTable.GetSelection(); r != row || c != col {
			bufferTable.Select(row, col)
		}
		return tview.MouseConsumed, nil
	case tview.MouseLeftUp:
		dragged := mouseDrag.dragging
		mouseDrag.pressed, mouseDrag.dragging = false, false
		if !dragged {
			return action, event // a click follows
		}
		if clipboardCopyOnSelect {
			r1, c1, r2, c2 := visualRect()
			yankCells(r1, c1, r2, c2)
		}
		return tview.MouseConsumed, nil
	case tview.MouseLeftDown, tview.MouseLeftClick, tview.MouseLeftDoubleClick,
		tview.MouseMiddleClick, tview.MouseRightClick:
		row, col, ok := cellUnderPointer(x, y)
		if !ok || row < b.rowFreeze {
			return tview.MouseConsumed, nil
		}
		switch action {
		case tview.MouseLeftDown:
			mouseDrag.pressed, mouseDrag.row, mouseDrag.col, mouseDrag.dragging = true, row, col, false
		case tview.MouseLeftClick:
			userMovedCursor = true
			if visual != visualOff {
				exitVisual("All Done")
			}
			if hiddenCols[col] { // a click on the fold marker opens the fold
				unfoldColumns(col, col)
			}
		case tview.MouseLeftDoubleClick:
			// The spreadsheet habit: a double click edits the cell.
			userMovedCursor = true
			bufferTable.Select(row, col)
			startCellEdit(actEdit)
			return tview.MouseConsumed, nil
		case tview.MouseRightClick:
			if visual != visualOff {
				exitVisual("All Done")
				return tview.MouseConsumed, nil
			}
			bufferTable.Select(row, col)
			pasteCells(row, col, row, col)
			return tview.MouseConsumed, nil
		case tview.MouseMiddleClick:
			bufferTable.Select(row, col)
			pasteCells(row, col, row, col)
			return tview.MouseConsumed, nil
		}
	case tview.MouseScrollUp:
		row, col := bufferTable.GetSelection()
		if row > firstDataRow(b) {
			bufferTable.Select(row-1, col)
		}
	case tview.MouseScrollDown:
		row, col := bufferTable.GetSelection()
		if row < b.rowCount()-1 {
			bufferTable.Select(row+1, col)
		}
	case tview.MouseScrollLeft, tview.MouseScrollRight:
		row, col := bufferTable.GetSelection()
		step := 1
		if action == tview.MouseScrollLeft {
			step = -1
		}
		bufferTable.Select(row, clampInt(col+step, 0, b.colCount()-1))
	}
	return action, event
}

// openSearchDialog shows the search form and runs the search on Enter.
func openSearchDialog() {
	if passRunning() {
		return
	}
	// Create search form
	form := tview.NewForm()
	form.AddInputField("Search:", "", 40, nil, nil)
	form.AddCheckbox("Use Regex:", searchUseRegex, func(checked bool) {
		searchUseRegex = checked
	})
	form.AddCheckbox("Case Sensitive:", false, nil)

	// Define search execution function to avoid duplication
	executeSearch := func() {
		query := form.GetFormItem(0).(*tview.InputField).GetText()
		useRegex := form.GetFormItem(1).(*tview.Checkbox).IsChecked()
		caseSensitive := form.GetFormItem(2).(*tview.Checkbox).IsChecked()
		UI.HidePage("searchModal")
		app.SetFocus(bufferTable)
		if query == "" {
			return
		}
		searchUseRegex = useRegex
		searched := b
		searchTable(searched, query, useRegex, caseSensitive, func(results []SearchResult) {
			if b != searched {
				drawFooterText(fileNameStr, "The table changed during the search; search again", cursorPosStr)
				return
			}
			searchQuery = query
			setSearchResults(results)
			if len(searchResults) > 0 {
				currentSearchIndex = 0
				bufferTable.Select(searchResults[0].Row, searchResults[0].Col)
				drawBuffer(b, bufferTable)
				searchMode := "matches"
				if useRegex {
					searchMode = "regex matches"
				}
				found := fmt.Sprintf("Found %d %s (1/%d)", len(searchResults), searchMode, len(searchResults))
				if b.streamed() && len(searchResults) == maxStreamMatches {
					found = fmt.Sprintf("Found the first %d %s (1/%d)", len(searchResults), searchMode, len(searchResults))
				}
				drawFooterText(fileNameStr, found, cursorPosStr)
			} else {
				currentSearchIndex = -1
				if useRegex {
					drawFooterText(fileNameStr, "Invalid regex or no matches found", cursorPosStr)
				} else {
					drawFooterText(fileNameStr, "No matches found", cursorPosStr)
				}
			}
		})
	}
	form.AddButton("Search", executeSearch)
	form.AddButton("Cancel", func() {
		UI.HidePage("searchModal")
		app.SetFocus(bufferTable)
	})
	form.SetButtonsAlign(tview.AlignCenter)
	form.SetBorder(true)
	title := " Search - Tab to navigate, Enter to search, Esc to cancel "
	form.SetTitle(title)
	form.SetTitleAlign(tview.AlignCenter)
	styleForm(form)

	// Handle Escape and Enter keys on form
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			UI.HidePage("searchModal")
			app.SetFocus(bufferTable)
			return nil
		}
		if event.Key() == tcell.KeyEnter {
			if itemIndex, _ := form.GetFocusedItemIndex(); itemIndex >= 0 {
				item := form.GetFormItem(itemIndex)
				if checkbox, ok := item.(*tview.Checkbox); ok {
					checkbox.SetChecked(!checkbox.IsChecked())
					return nil
				}
			}
			executeSearch()
			return nil
		}
		return event
	})

	// Create centered modal overlay
	searchModal = tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 11, 1, true).
			AddItem(nil, 0, 1, false), 60, 1, true).
		AddItem(nil, 0, 1, false)

	UI.AddPage("searchModal", searchModal, true, true)
	UI.ShowPage("searchModal")
	app.SetFocus(form)
}

// openFilterDialog shows the filter form for the selected column.
func openFilterDialog() {
	if passRunning() {
		return
	}
	_, column := bufferTable.GetSelection()

	// Create filter form
	filterForm := tview.NewForm()

	// Operator selection
	operators := []string{"contains", "equals", "starts with", "ends with", "regex", ">", "<", ">=", "<=", opUnique, opUniqueRows}
	selectedOperatorIndex := 0

	// Value input
	query := ""
	caseSensitive := false

	if opts, exists := activeFilters[column]; exists {
		query = opts.Query
		caseSensitive = opts.CaseSensitive
		for i, op := range operators {
			if op == opts.Operator {
				selectedOperatorIndex = i
				break
			}
		}
	}

	filterForm.AddDropDown("Operator:", operators, selectedOperatorIndex, func(option string, optionIndex int) {
		selectedOperatorIndex = optionIndex
	})
	filterForm.AddInputField("Value:", query, 40, nil, nil)
	filterForm.AddCheckbox("Case Sensitive:", caseSensitive, func(checked bool) {
		caseSensitive = checked
	})

	applyFilter := func() {
		query = filterForm.GetFormItem(1).(*tview.InputField).GetText()
		operator := operators[selectedOperatorIndex]
		UI.HidePage("filterModal")
		app.SetFocus(bufferTable)

		// Unique filters need no value; for the others an empty value removes the filter.
		if query != "" || isUniqueOperator(operator) {
			// Add or update filter for this column
			was, had := activeFilters[column]
			activeFilters[column] = FilterOptions{
				Query:         query,
				Operator:      operator,
				CaseSensitive: caseSensitive,
			}

			// Apply all filters starting from original buffer
			if originalBuffer == nil {
				originalBuffer = b // Save original buffer first time
			}

			deriveFilteredView(func(filteredBuffer *Buffer) {
				// Update display with filtered data
				if filteredBuffer.rowLen <= filteredBuffer.rowFreeze {
					drawFooterText(fileNameStr, "No rows match filters", cursorPosStr)
					// Remove this filter since it results in no data
					delete(activeFilters, column)
				} else {
					// Replace current buffer with filtered buffer
					b = filteredBuffer
					isFiltered = true

					drawBuffer(b, bufferTable)
					bufferTable.Select(firstDataRow(b), column) // Stay at same column, go to first data row
					matchCount := b.rowLen - b.rowFreeze
					drawFooterText(fileNameStr,
						fmt.Sprintf("Filtered: %d rows match (%d filters active, r to reset)", matchCount, len(activeFilters)),
						cursorPosStr)
				}
			}, func() {
				// The pass was cancelled: the filter is as it was before.
				if had {
					activeFilters[column] = was
				} else {
					delete(activeFilters, column)
				}
			})
		} else if was, exists := activeFilters[column]; exists {
			// Empty query means remove filter for this column
			delete(activeFilters, column)
			reapplyFilters(0, column, "All filters cleared - showing all rows", func(matches int) string {
				return fmt.Sprintf("Filter removed: %d rows match (%d filters active)", matches, len(activeFilters))
			}, was)
		}
	}

	filterForm.AddButton("Filter", applyFilter)
	filterForm.AddButton("Cancel", func() {
		UI.HidePage("filterModal")
		app.SetFocus(bufferTable)
	})
	filterForm.SetButtonsAlign(tview.AlignCenter)
	filterForm.SetBorder(true)

	filterTitle := fmt.Sprintf(" Filter Column %d - Enter to filter, Esc to cancel ", column)
	if _, exists := activeFilters[column]; exists {
		filterTitle = fmt.Sprintf(" Edit Filter for Column %d (empty value to remove) - Enter to apply, Esc to cancel ", column)
	}
	filterForm.SetTitle(filterTitle)
	filterForm.SetTitleAlign(tview.AlignCenter)
	styleForm(filterForm)

	// Handle Escape and Enter keys on form
	filterForm.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			UI.HidePage("filterModal")
			app.SetFocus(bufferTable)
			return nil
		}
		if event.Key() == tcell.KeyEnter {
			// If the dropdown has focus, let it handle the Enter key.
			if itemIndex, _ := filterForm.GetFocusedItemIndex(); itemIndex >= 0 {
				if item := filterForm.GetFormItem(itemIndex); item != nil {
					if _, ok := item.(*tview.DropDown); ok {
						return event
					}
					if checkbox, ok := item.(*tview.Checkbox); ok {
						checkbox.SetChecked(!checkbox.IsChecked())
						return nil
					}
				}
			}
			// if dropdown is open, pass enter to it
			if _, ok := app.GetFocus().(*tview.List); ok {
				return event
			}
			applyFilter()
			return nil
		}
		return event
	})

	// Create centered modal overlay
	filterModal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(filterForm, 13, 1, true).
			AddItem(nil, 0, 1, false), 80, 1, true).
		AddItem(nil, 0, 1, false)

	UI.AddPage("filterModal", filterModal, true, true)
	UI.ShowPage("filterModal")
	app.SetFocus(filterForm)
}

// removeCurrentFilter drops the filter on the selected column and reapplies the rest.
func removeCurrentFilter() {
	if isFiltered && originalBuffer != nil {
		row, column := bufferTable.GetSelection()

		// Check if current column has a filter
		if was, hasFilter := activeFilters[column]; hasFilter {
			if passRunning() {
				return
			}
			// Remove filter for this column and reapply the remaining ones
			delete(activeFilters, column)
			reapplyFilters(row, column, "All filters cleared - showing all rows", func(matches int) string {
				return fmt.Sprintf("Filter removed from current column: %d rows match (%d filters active)", matches, len(activeFilters))
			}, was)
		} else if len(activeFilters) > 0 {
			// Current column doesn't have a filter, but others do
			drawFooterText(fileNameStr, "Current column has no filter - navigate to filtered column to remove", cursorPosStr)
		}
	}
}

// showCurrentColumnStats opens the statistics dialog for the selected column.
func showCurrentColumnStats() {
	_, column := bufferTable.GetSelection()
	drawFooterText(fileNameStr, "Calculating statistics...", cursorPosStr)
	app.ForceDraw()

	// Use the current buffer (which is filtered if filters are active)
	// This ensures stats are calculated only on visible/filtered data
	currentBuffer := b

	var statsS statsSummary
	summaryArray := currentBuffer.getCol(column)
	columnName := "Column " + I2S(column)

	// Get column name from header if available
	if currentBuffer.rowFreeze > 0 && len(summaryArray) > 0 {
		if name, ok := currentBuffer.cellAt(0, column); ok {
			columnName = name
		}
		summaryArray = summaryArray[1:]
	}
	// A streamed table is sampled: reading every row would take as long as a
	// pass and hold every value in memory.
	if currentBuffer.streamed() && currentBuffer.rowCount() > statsSampleRows {
		columnName += fmt.Sprintf(" (first %d rows)", statsSampleRows)
	}

	// Determine statistics type
	if currentBuffer.getColType(column) == colTypeFloat {
		statsS = &ContinuousStats{}
	} else {
		statsS = &DiscreteStats{}
	}
	statsS.summary(summaryArray)

	// Show statistics as a modal dialog with filter indication
	showStatsDialog(statsS, columnName, currentBuffer.getColType(column))
	drawFooterText(fileNameStr, "All Done", cursorPosStr)
}

// toggleColumnType cycles the selected column through String, Number and Date.
func toggleColumnType() {
	row, column := bufferTable.GetSelection()
	currentType := b.getColType(column)

	// Cycle through types: Str -> Num -> Date -> Str
	var newType int
	switch currentType {
	case colTypeStr:
		newType = colTypeFloat
	case colTypeFloat:
		newType = colTypeDate
	case colTypeDate:
		newType = colTypeStr
	default:
		newType = colTypeStr
	}

	b.setColType(column, newType)
	cursorPosStr = buildCursorPosStr(row, column)
	drawFooterText(fileNameStr, statusMessage, cursorPosStr)
}

// toggleColumnWidth switches the width limit of the selected column on or off.
func toggleColumnWidth() {
	_, column := bufferTable.GetSelection()

	if _, isWrapped := wrappedColumns[column]; isWrapped {
		// Unwrap: remove from wrapped columns
		delete(wrappedColumns, column)
		drawFooterText(fileNameStr, "Column width limit removed", cursorPosStr)
	} else {
		// Wrap: add to wrapped columns with default width
		width := getColumnMaxWidth(column)
		wrappedColumns[column] = width
		drawFooterText(fileNameStr, fmt.Sprintf("Column width limited to %d chars", width), cursorPosStr)
	}

	// Redraw the table with updated wrapping
	drawBuffer(b, bufferTable)
	updateCellPreview(bufferTable.GetSelection())
}

// showHelpDialog displays the help content as a centered modal dialog
func showHelpDialog() {
	// Create help content text view
	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetText(getHelpContent()).
		SetTextAlign(tview.AlignLeft).
		SetWordWrap(true)

	// Make help text scrollable with modern styling
	helpText.SetScrollable(true)
	helpText.SetBorder(true)
	helpText.SetTitle(" Help - Press ? or q or Esc to close ")
	helpText.SetTitleAlign(tview.AlignCenter)
	helpText.SetBorderColor(theme.Accent)
	helpText.SetBackgroundColor(theme.Background)
	helpText.SetTextColor(theme.Text)

	// Handle key events to close dialog
	helpText.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape ||
			(event.Key() == tcell.KeyRune && (event.Rune() == '?' || event.Rune() == 'q')) {
			UI.RemovePage("helpDialog")
			app.SetFocus(bufferTable)
			return nil
		}
		// Allow j/k navigation in help
		if event.Key() == tcell.KeyRune && event.Rune() == 'j' {
			row, col := helpText.GetScrollOffset()
			helpText.ScrollTo(row+1, col)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'k' {
			row, col := helpText.GetScrollOffset()
			if row > 0 {
				helpText.ScrollTo(row-1, col)
			}
			return nil
		}
		// gg - go to top
		if event.Key() == tcell.KeyRune && event.Rune() == 'g' {
			if secondGPress() {
				helpText.ScrollToBeginning()
			}
			return nil
		}
		// G - go to bottom
		if event.Key() == tcell.KeyRune && event.Rune() == 'G' {
			helpText.ScrollToEnd()
			return nil
		}
		// Ctrl-d/u for page scrolling
		if event.Key() == tcell.KeyCtrlD {
			row, col := helpText.GetScrollOffset()
			helpText.ScrollTo(row+10, col)
			return nil
		}
		if event.Key() == tcell.KeyCtrlU {
			row, col := helpText.GetScrollOffset()
			if row > 10 {
				helpText.ScrollTo(row-10, col)
			} else {
				helpText.ScrollTo(0, col)
			}
			return nil
		}
		return event
	})

	// Create a centered modal with the help text
	// Modal dimensions: 80% width, 85% height
	helpModal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(helpText, 0, 85, true).
			AddItem(nil, 0, 1, false), 0, 80, true).
		AddItem(nil, 0, 1, false)

	// Add and show the help dialog
	UI.AddPage("helpDialog", helpModal, true, true)
	app.SetFocus(helpText)
}

// showStatsDialog displays column statistics as a centered modal dialog
func showStatsDialog(statsS statsSummary, columnName string, colType int) {
	// Create stats table
	statsTable := tview.NewTable()
	statsTable.SetSelectable(true, true)
	statsTable.SetBorders(false)
	statsTable.Select(0, 0)

	// Draw statistics
	drawStats(statsS, statsTable)

	// Create border with title and modern styling
	statsTable.SetBorder(true)
	statsTable.SetBorderColor(theme.Accent)
	statsTable.SetBackgroundColor(theme.Background)
	statsTable.SetSelectedStyle(theme.highlightStyle())

	typeName := type2name(colType)
	title := fmt.Sprintf(" Statistics: %s [%s] ", columnName, typeName)

	// Add filter indicator if data is filtered
	if isFiltered && len(activeFilters) > 0 {
		title = fmt.Sprintf(" Statistics: %s [%s] (Filtered Data - %d filters active) ", columnName, typeName, len(activeFilters))
	}

	statsTable.SetTitle(title)
	statsTable.SetTitleAlign(tview.AlignCenter)

	// Create plot view
	plotView := tview.NewTextView().
		SetDynamicColors(true).
		SetText(statsS.getPlot()).
		SetTextAlign(tview.AlignLeft)
	plotView.SetBorder(true)
	plotView.SetTitle(" Visual Distribution ")
	plotView.SetTitleAlign(tview.AlignCenter)
	plotView.SetBorderColor(theme.Dim)
	plotView.SetBackgroundColor(theme.Background)
	plotView.SetTextColor(theme.Text)

	// Create a flex layout with stats on left and plot on right
	statsContent := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(statsTable, 0, 1, true).
		AddItem(plotView, 0, 1, false)

	// Handle key events
	statsContent.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Escape or q - close dialog
		if event.Key() == tcell.KeyEscape || (event.Key() == tcell.KeyRune && event.Rune() == 'q') {
			UI.RemovePage("statsDialog")
			app.SetFocus(bufferTable)
			return nil
		}

		// gg - go to top
		if event.Key() == tcell.KeyRune && event.Rune() == 'g' {
			if secondGPress() {
				_, column := statsTable.GetSelection()
				statsTable.Select(0, column)
				statsTable.ScrollToBeginning()
			}
			return nil
		}

		// G - go to bottom
		if event.Key() == tcell.KeyRune && event.Rune() == 'G' {
			_, column := statsTable.GetSelection()
			statsTable.Select(statsTable.GetRowCount()-1, column)
			statsTable.ScrollToEnd()
			return nil
		}

		// j/k navigation
		if event.Key() == tcell.KeyRune && event.Rune() == 'j' {
			row, col := statsTable.GetSelection()
			if row < statsTable.GetRowCount()-1 {
				statsTable.Select(row+1, col)
			}
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'k' {
			row, col := statsTable.GetSelection()
			if row > 0 {
				statsTable.Select(row-1, col)
			}
			return nil
		}

		// Ctrl-d/u for page scrolling
		if event.Key() == tcell.KeyCtrlD {
			row, col := statsTable.GetSelection()
			newRow := row + 10
			if newRow >= statsTable.GetRowCount() {
				newRow = statsTable.GetRowCount() - 1
			}
			statsTable.Select(newRow, col)
			return nil
		}
		if event.Key() == tcell.KeyCtrlU {
			row, col := statsTable.GetSelection()
			newRow := row - 10
			if newRow < 0 {
				newRow = 0
			}
			statsTable.Select(newRow, col)
			return nil
		}

		return event
	})

	// Create a centered modal with the stats content
	// Modal dimensions: 80% width, 80% height
	statsModal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(statsContent, 0, 80, true).
			AddItem(tview.NewTextView().
				SetText("Press q or Esc to close").
				SetTextAlign(tview.AlignCenter).
				SetTextColor(theme.Dim), 1, 0, false).
			AddItem(nil, 0, 1, false), 0, 80, true).
		AddItem(nil, 0, 1, false)

	// Add and show the stats dialog
	UI.AddPage("statsDialog", statsModal, true, true)
	app.SetFocus(statsContent)
}
