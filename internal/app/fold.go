package app

import (
	"fmt"
	"strings"
)

// Hidden columns work like vim's folds: a hidden column collapses to a
// narrow, dimmed marker column so its place stays visible, and zo (or editing
// a cell in it) opens it again. Hiding is a view setting, not an edit: it is
// not written, undone or counted as a pending change, and it follows its
// column when columns are removed or inserted.

// foldMarker is what every cell of a hidden column shows.
const foldMarker = "»"

// hiddenCols holds the hidden columns by index.
var hiddenCols = map[int]bool{}

// foldColumns hides columns c1..c2 of the table; at least one column always
// stays visible.
func foldColumns(c1, c2 int) {
	c1, c2 = orderRange(c1, c2, 0, b.colCount()-1)
	hiding := 0
	for c := c1; c <= c2; c++ {
		if !hiddenCols[c] {
			hiding++
		}
	}
	if hiding == 0 {
		drawFooterText(fileNameStr, "Already hidden", cursorPosStr)
		return
	}
	if len(hiddenCols)+hiding >= b.colCount() {
		drawFooterText(fileNameStr, "Cannot hide every column", cursorPosStr)
		return
	}
	names := make([]string, 0, hiding)
	for c := c1; c <= c2; c++ {
		if !hiddenCols[c] {
			hiddenCols[c] = true
			names = append(names, columnTitle(c))
		}
	}
	redrawFolds()
	drawFooterText(fileNameStr, fmt.Sprintf("Hid %s (%s); %s shows, %s shows all", plural(hiding, "column"), strings.Join(names, ", "), keyHintOr(actUnfoldColumn, "zo"), keyHintOr(actUnfoldAll, "zR")), cursorPosStr)
}

// unfoldColumns shows columns c1..c2 again.
func unfoldColumns(c1, c2 int) {
	c1, c2 = orderRange(c1, c2, 0, b.colCount()-1)
	var names []string
	for c := c1; c <= c2; c++ {
		if hiddenCols[c] {
			delete(hiddenCols, c)
			names = append(names, columnTitle(c))
		}
	}
	if len(names) == 0 {
		drawFooterText(fileNameStr, "Not hidden", cursorPosStr)
		return
	}
	redrawFolds()
	drawFooterText(fileNameStr, "Showing "+plural(len(names), "column")+" ("+strings.Join(names, ", ")+")", cursorPosStr)
}

// toggleFold hides the column under the cursor, or shows it when hidden. On
// a selection it hides when any selected column is visible.
func toggleFold(c1, c2 int) {
	c1, c2 = orderRange(c1, c2, 0, b.colCount()-1)
	for c := c1; c <= c2; c++ {
		if !hiddenCols[c] {
			foldColumns(c1, c2)
			return
		}
	}
	unfoldColumns(c1, c2)
}

// unfoldAll shows every hidden column.
func unfoldAll() {
	if len(hiddenCols) == 0 {
		drawFooterText(fileNameStr, "No hidden columns", cursorPosStr)
		return
	}
	n := len(hiddenCols)
	for c := range hiddenCols {
		delete(hiddenCols, c)
	}
	redrawFolds()
	drawFooterText(fileNameStr, "Showing all columns ("+plural(n, "column")+" was hidden)", cursorPosStr)
}

// redrawFolds refreshes the table and the footer position after folds changed.
func redrawFolds() {
	drawBuffer(b, bufferTable)
	row, col := bufferTable.GetSelection()
	cursorPosStr = buildCursorPosStr(row, col)
	updateCellPreview(row, col)
}

// keyHintOr renders an action's keys for a message, falling back to the
// default spelling when the action is unbound.
func keyHintOr(act action, fallback string) string {
	if k := keys.keysFor(act); k != "" {
		return strings.ReplaceAll(k, " ", "")
	}
	return fallback
}
