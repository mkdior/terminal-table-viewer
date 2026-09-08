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
	sb.WriteString(entry("Left click", "Select cell"))
	sb.WriteString(entry("Scroll wheel", "Move up/down through rows"))
	sb.WriteString(entry("Click buttons", "Interact with dialogs and forms"))
	sb.WriteString("\n")

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
	sb.WriteString("  rows (V) or columns (v). x clears cells, X removes and copies, p pastes\n")
	sb.WriteString("  the last yank over cells (in visual mode i, a and cc edit every selected\n")
	sb.WriteString("  cell at once, like vim's block insert). The cell\n")
	sb.WriteString("  editor (E, i, a, cc) is a vim line: h l w b e 0 ^ $ f t ; , motions,\n")
	sb.WriteString("  d c y with motions or text objects (iw aw i\" a( ...), dd cc yy D C Y,\n")
	sb.WriteString("  x X s S r ~ p P, u and Ctrl-r, . to repeat, v for a selection, R to\n")
	sb.WriteString("  replace; i a I A insert. Enter applies the value, Esc cancels.\n")
	sb.WriteString("  A private copy of the file's previous version is kept in the backup\n")
	sb.WriteString("  directory on every write (see --dump-config).\n\n")

	sb.WriteString(head("Tips") + "\n")
	sb.WriteString("  Long cells are cut at 50 characters; a cut cell shows its full value\n")
	sb.WriteString("  in a floating box while selected. Keys can be changed in the config\n")
	sb.WriteString("  file; see ttv --dump-config for the defaults.\n\n")
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
	defer b.mu.RUnlock()

	// Determine how many rows to sample
	maxSample := sampleSize
	if b.rowLen < maxSample {
		maxSample = b.rowLen
	}

	// Skip header row in analysis if it exists
	startRow := 0
	if b.rowFreeze > 0 {
		startRow = b.rowFreeze
	}

	// Track maximum length found in each column
	maxLengths := make([]int, b.colLen)

	// Sample rows to find maximum content length per column
	for r := startRow; r < maxSample; r++ {
		for c := 0; c < b.colLen; c++ {
			if c < len(b.cont[r]) {
				cellLen := len(b.cont[r][c])
				if cellLen > maxLengths[c] {
					maxLengths[c] = cellLen
				}
			}
		}
	}

	// Enable wrapping for columns that exceed threshold
	for c := 0; c < b.colLen; c++ {
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

	// Compile regex if in regex mode
	var re *regexp.Regexp
	var err error
	if useRegex {
		if !caseSensitive {
			query = "(?i)" + query
		}
		re, err = regexp.Compile(query)
		if err != nil {
			return []SearchResult{}
		}
	} else if !caseSensitive {
		query = strings.ToLower(query)
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
				cellText := b.cont[r][col]

				var matches bool
				if useRegex {
					matches = re.MatchString(cellText)
				} else {
					if caseSensitive {
						matches = strings.Contains(cellText, query)
					} else {
						matches = strings.Contains(strings.ToLower(cellText), query)
					}
				}

				if matches {
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
