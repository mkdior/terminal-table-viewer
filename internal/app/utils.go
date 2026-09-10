package app

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/rivo/uniseg"
)

// fatalError restores the terminal, prints err in red and exits with status 1.
// The UI must be stopped before printing or the message lands on the raw screen.
func fatalError(err error) {
	if err != nil {
		if app != nil {
			app.Stop()
		}
		color.Set(color.FgRed)
		fmt.Fprintln(os.Stderr, err)
		color.Unset()
		if !debug {
			os.Exit(1)
		}
	}
}

// print useful info and force quite app
func usefulInfo(s string) {
	color.Set(color.FgHiYellow)
	fmt.Println(s)
	color.Unset()
}

// I2B  covert int to bool, if i >0:true, else false
func I2B(i int) bool {
	return i > 0
}

// F2S covert float64 to bool
func F2S(i float64) string {
	return strconv.FormatFloat(i, 'f', 4, 64)
}

// S2F covert string to float64
func S2F(i string) float64 {
	s, err := strconv.ParseFloat(i, 64)
	if err != nil {
		fatalError(err)
	}
	return s
}

// I2S covert int to string
func I2S(i int) string {
	return strconv.Itoa(i)
}

// getHelpContent renders the help dialog from the action catalog and the
// active keymap, so remapped keys are shown exactly as bound.
func getHelpContent() string {
	key := func(k string) string { return theme.tag(theme.Accent) + k + "[-]" }
	head := func(h string) string { return "[::b]" + theme.tag(theme.Text) + h + "[-:-:-]" }
	rule := theme.tag(theme.Dim) + strings.Repeat("━", 72) + "[-:-:-]"
	entry := func(k, text string) string {
		pad := 22 - uniseg.StringWidth(k)
		if pad < 2 {
			pad = 2
		}
		return "  " + key(k) + strings.Repeat(" ", pad) + text + "\n"
	}

	var sb strings.Builder
	sb.WriteString(rule + "\n\n" + head("ttv - Terminal Table Viewer") + "\n\n" + rule + "\n\n")

	sb.WriteString(head("Help Navigation") + "\n")
	sb.WriteString(entry("j/k", "Scroll help text"))
	sb.WriteString(entry("gg/G", "Jump to top/bottom"))
	sb.WriteString(entry("Ctrl-d/u", "Page down/up"))
	sb.WriteString(entry("? or q or Esc", "Close help dialog"))
	sb.WriteString("\n")

	var section string
	for _, a := range actionCatalog {
		if a.section != section {
			if section != "" {
				sb.WriteString("\n")
			}
			section = a.section
			sb.WriteString(head(section) + "\n")
		}
		bound := keys.keysFor(a.act)
		if bound == "" {
			bound = "(unbound)"
		}
		sb.WriteString(entry(bound, a.help))
	}
	sb.WriteString("\n")

	sb.WriteString(head("Count prefixes") + "\n")
	sb.WriteString(entry("N + motion", "Repeat a motion N times: 5j, 3l, 2w, 4n"))
	sb.WriteString(entry("NG / Ngg", "Jump to row N"))
	sb.WriteString(entry("N Ctrl-d/u", "Move N rows"))
	sb.WriteString("\n")

	sb.WriteString(head("Mouse") + "\n")
	sb.WriteString(entry("Left click", "Select the cell; on a tab, show it; on a fold marker, open it"))
	sb.WriteString(entry("Drag", "Select a block of cells, copied when the button is released"))
	sb.WriteString(entry("Double click", "Edit the cell, as E does"))
	sb.WriteString(entry("Right click", "Clear the selection; without one, paste over the cell (p)"))
	sb.WriteString(entry("Middle click", "Paste over the cell (p); on a tab, close the tab"))
	sb.WriteString(entry("Scroll wheel", "Move a row, sideways a column; over the tab line, step tabs"))
	sb.WriteString(entry("Click buttons", "Dialogs and forms; \"? help\" in the footer opens this help"))
	sb.WriteString("  A dragged selection stays, as after v: y copies it again, d and x remove\n")
	sb.WriteString("  it, Esc or a click clears it. copy_on_select in [clipboard] turns the\n")
	sb.WriteString("  copy on release off.\n\n")

	sb.WriteString(head("Search and filter") + "\n")
	sb.WriteString("  Search is case-insensitive unless Case Sensitive is checked; Tab moves\n")
	sb.WriteString("  between the field, the checkboxes and the buttons. Regex examples:\n")
	sb.WriteString(entry("^start", "Match at beginning of cell"))
	sb.WriteString(entry("end$", "Match at end of cell"))
	sb.WriteString(entry("\\d+", "Match digits"))
	sb.WriteString(entry("word1|word2", "Match either word"))
	sb.WriteString("  Filters take an operator (contains, equals, starts/ends with, regex,\n")
	sb.WriteString("  >, <, >=, <=) and combine across columns with AND. unique keeps the\n")
	sb.WriteString("  first row per distinct value in the column; unique rows drops rows\n")
	sb.WriteString("  that repeat an earlier row exactly.\n\n")

	sb.WriteString(head("Editing") + "\n")
	sb.WriteString("  Edits are staged like fdisk: nothing touches the file until W writes\n")
	sb.WriteString("  it, and q asks whether to write or discard. d is vim's operator: dd\n")
	sb.WriteString("  removes the row, dj/dG the rows a vertical motion spans, dl/d$/d0 the\n")
	sb.WriteString("  columns a horizontal one spans; in visual mode d removes the selected\n")
	sb.WriteString("  rows (V) or columns (v). x cuts cells (in visual line mode it removes\n")
	sb.WriteString("  the rows, as d does), p pastes the last yank or removal over cells (in\n")
	sb.WriteString("  visual mode i, a and cc or R edit every selected\n")
	sb.WriteString("  cell at once, like vim's block insert). ir/or add a row above/below,\n")
	sb.WriteString("  ic/oc a column left/right, as in sc-im. The cell editor (E, i, a,\n")
	sb.WriteString("  cc, R) is a vim line: h l w b e 0 ^ $ f t ; , motions,\n")
	sb.WriteString("  d c y with motions or text objects (iw aw i\" a( ...), dd cc yy D C Y,\n")
	sb.WriteString("  x X s S r ~ p P, u and Ctrl-r, . to repeat, v for a selection, R to\n")
	sb.WriteString("  replace; i a I A insert. Enter applies the value, Esc cancels.\n")
	sb.WriteString("  A private copy of the file's previous version is kept in the backup\n")
	sb.WriteString("  directory on every write (see --dump-config).\n\n")

	sb.WriteString(head("Tabs") + "\n")
	sb.WriteString("  ttv A.csv B.csv opens one tab per file (vim's -p is accepted too). gt\n")
	sb.WriteString("  and gT switch, Ngt goes to tab N. The tab line above the table marks a\n")
	sb.WriteString("  tab [+] while it has pending edits and shows how far its load is. Every\n")
	sb.WriteString("  file loads at once within one -m budget; closing a tab stops its load\n")
	sb.WriteString("  and frees its memory. Yanks and removals go to one register, so p\n")
	sb.WriteString("  pastes across tabs. q closes the tab in front and quits when it is the\n")
	sb.WriteString("  last one. Quit everything with Ctrl-C: with unwritten changes in several\n")
	sb.WriteString("  tabs the prompt lists them, and w writes them all before quitting.\n\n")

	sb.WriteString(head("Tips") + "\n")
	sb.WriteString("  zc hides a column behind a narrow marker like a closed fold, zo shows\n")
	sb.WriteString("  it again, za toggles, zR shows all; editing a hidden cell opens it.\n")
	sb.WriteString("  Long cells are cut at 50 characters; a cut cell shows its full value\n")
	sb.WriteString("  in a floating box while selected. Keys can be changed in the config\n")
	sb.WriteString("  file; see ttv --dump-config for the defaults.\n")
	sb.WriteString("  A plain file of 1GB or more (--stream-above, or --stream for any file)\n")
	sb.WriteString("  is streamed from disk instead of loaded: the footer says [streamed],\n")
	sb.WriteString("  the table is read-only, search and filters run over the file in the\n")
	sb.WriteString("  background (Esc cancels), and statistics use the first 100000 rows.\n\n")
	sb.WriteString(rule + "\n")
	return sb.String()
}

// wrapText wraps text to fit within maxWidth characters
// Returns the wrapped text with newlines
func wrapText(text string, maxWidth int) string {
	if maxWidth <= 0 || len(text) <= maxWidth {
		return text
	}

	var result []rune
	runes := []rune(text)
	lineStart := 0

	for i := 0; i < len(runes); i++ {
		// Check if we've reached the wrap point
		if i-lineStart >= maxWidth {
			// Find last space before maxWidth for word wrap
			wrapPoint := i
			for j := i; j > lineStart; j-- {
				if runes[j] == ' ' || runes[j] == '\t' || runes[j] == '-' {
					wrapPoint = j + 1
					break
				}
			}

			// If no good wrap point found, hard wrap at maxWidth
			if wrapPoint == i && i > lineStart {
				wrapPoint = lineStart + maxWidth
			}

			// Add the wrapped line
			result = append(result, runes[lineStart:wrapPoint]...)
			result = append(result, '\n')

			// Skip trailing spaces on new line
			for wrapPoint < len(runes) && (runes[wrapPoint] == ' ' || runes[wrapPoint] == '\t') {
				wrapPoint++
			}

			lineStart = wrapPoint
			i = wrapPoint - 1 // -1 because loop will increment
		}
	}

	// Add remaining text
	if lineStart < len(runes) {
		result = append(result, runes[lineStart:]...)
	}

	return string(result)
}

// truncateText cuts text to maxWidth terminal cells and appends an ellipsis.
// Width is measured in display cells, as tview draws it, so wide characters
// count double and the visible result matches the limit.
func truncateText(text string, maxWidth int) string {
	if maxWidth <= 0 || uniseg.StringWidth(text) <= maxWidth {
		return text
	}
	// Reserve 3 cells for the ellipsis
	if maxWidth <= 3 {
		return cutToWidth(text, maxWidth)
	}
	return cutToWidth(text, maxWidth-3) + "..."
}

// cutToWidth returns the longest prefix of text, on grapheme boundaries,
// whose display width does not exceed width.
func cutToWidth(text string, width int) string {
	used, end := 0, 0
	gr := uniseg.NewGraphemes(text)
	for gr.Next() {
		if used+gr.Width() > width {
			break
		}
		used += gr.Width()
		_, end = gr.Positions()
	}
	return text[:end]
}

// getColumnMaxWidth determines the maximum width for a column
func getColumnMaxWidth(colIndex int) int {
	// Default wrap width (50 characters for long columns)
	defaultWidth := 50

	// Check if custom width is set
	if width, exists := wrappedColumns[colIndex]; exists {
		return width
	}

	return defaultWidth
}

// detectAndWrapLongColumns automatically enables wrapping for columns with long content
// Analyzes first N rows to detect if columns have text longer than threshold
func detectAndWrapLongColumns(b *Buffer, sampleSize int, threshold int) {
	b.mu.RLock()
	rowLen, colLen, startRow := b.rowLen, b.colLen, b.rowFreeze // the header row is not sampled
	b.mu.RUnlock()

	// Determine how many rows to sample
	maxSample := min(sampleSize, rowLen)

	// Track maximum length found in each column
	maxLengths := make([]int, colLen)

	// Sample rows to find maximum content length per column (through row, so
	// a streamed table is sampled too)
	for r := startRow; r < maxSample; r++ {
		row := b.row(r)
		for c := 0; c < colLen && c < len(row); c++ {
			if cellLen := len(row[c]); cellLen > maxLengths[c] {
				maxLengths[c] = cellLen
			}
		}
	}

	// Enable wrapping for columns that exceed threshold
	for c := 0; c < colLen; c++ {
		if maxLengths[c] > threshold {
			// Only set if not already manually configured
			if _, exists := wrappedColumns[c]; !exists {
				wrappedColumns[c] = getColumnMaxWidth(c)
			}
		}
	}
}

// performSearch searches for a query string in the buffer and stores results
// Supports both plain text and regex search modes with parallel column scanning
func performSearch(b *Buffer, query string, useRegex bool, caseSensitive bool) []SearchResult {
	b.mu.RLock()
	defer b.mu.RUnlock()

	match, ok := searchMatcher(query, useRegex, caseSensitive)
	if !ok {
		return []SearchResult{}
	}

	// Parallel search across columns for better performance
	resultChan := make(chan []SearchResult, b.colLen)
	var wg sync.WaitGroup

	for c := 0; c < b.colLen; c++ {
		wg.Add(1)
		go func(col int) {
			defer wg.Done()
			var colResults []SearchResult

			for r := 0; r < b.rowLen; r++ {
				if match(b.cont[r][col]) {
					colResults = append(colResults, SearchResult{Row: r, Col: col})
				}
			}

			resultChan <- colResults
		}(c)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results from all columns
	var results []SearchResult
	for colResults := range resultChan {
		results = append(results, colResults...)
	}

	return results
}

// searchMatcher builds the cell test of a search: a regex (compiled once, case
// folded unless caseSensitive) or a substring test. ok is false for a regex
// that does not compile.
func searchMatcher(query string, useRegex, caseSensitive bool) (match func(string) bool, ok bool) {
	if useRegex {
		if !caseSensitive {
			query = "(?i)" + query
		}
		re, err := regexp.Compile(query)
		if err != nil {
			return nil, false
		}
		return re.MatchString, true
	}
	if caseSensitive {
		return func(cell string) bool { return strings.Contains(cell, query) }, true
	}
	query = strings.ToLower(query)
	return func(cell string) bool { return strings.Contains(strings.ToLower(cell), query) }, true
}

// toLower converts a string to lowercase using optimized stdlib
func toLower(s string) string {
	return strings.ToLower(s)
}

// makeProgressBar creates a visual progress bar
// percent should be between 0 and 100
// width is the number of characters for the bar
func makeProgressBar(percent float64, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := int(float64(width) * percent / 100.0)
	empty := width - filled

	bar := "["
	for i := 0; i < filled; i++ {
		bar += "█"
	}
	for i := 0; i < empty; i++ {
		bar += "░"
	}
	bar += fmt.Sprintf("] %.1f%%", percent)

	return bar
}
