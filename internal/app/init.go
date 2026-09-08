package app

import (
	"sync/atomic"
	"time"

	"github.com/rivo/tview"
)

// column data type
const colTypeStr = 0
const colTypeFloat = 1
const colTypeDate = 2

// get column data type name. s: string, n: number, d: date
func type2name(i int) string {
	switch i {
	case colTypeStr:
		return "Str"
	case colTypeFloat:
		return "Num"
	case colTypeDate:
		return "Date"
	default:
		return "Str"
	}
}

var app *tview.Application
var UI *tview.Pages
var b *Buffer
var args Args
var debug bool
var statusMessage string         // Track status message for footer updates
var mainPage *tview.Frame        // Reference to main page for footer updates
var mainView *cellPreview        // Main page plus the floating full-value preview
var bufferTable *tview.Table     // Reference to buffer table
var fileNameStr string           // Store filename for footer
var cursorPosStr string          // Store cursor position for footer
var loadProgress LoadProgress    // Track loading progress
var userMovedCursor bool         // Track if user has moved the cursor
var wrappedColumns map[int]int   // Track which columns are wrapped and their max width
var searchResults []SearchResult // Store search results
var currentSearchIndex int       // Current position in search results
var searchQuery string           // Current search query
var searchModal tview.Primitive  // Search modal dialog
var searchUseRegex bool

var originalBuffer *Buffer              // Store original buffer before filtering
var isFiltered bool                     // Track if filter is active
var activeFilters map[int]FilterOptions // Track active filters: column -> query
var currentCursorColumn int             // Track current cursor column position
var lastGPress time.Time                // Time of the last 'g' press, for the gg chord
var pendingCount int                    // Digits typed so far for a vim-style count prefix (0 = none)
var keys = defaultKeymap()              // Active key bindings

// LoadProgress tracks loading progress. It is written by the loader goroutine
// and read by the UI ticker, so the fields are atomic.
type LoadProgress struct {
	TotalBytes  atomic.Int64
	LoadedBytes atomic.Int64
	IsComplete  atomic.Bool
}

// Reset prepares the tracker for a new load of the given total size (0 when unknown).
func (lp *LoadProgress) Reset(total int64) {
	lp.TotalBytes.Store(total)
	lp.LoadedBytes.Store(0)
	lp.IsComplete.Store(false)
}

// GetPercentage returns the loading percentage (0-100)
func (lp *LoadProgress) GetPercentage() float64 {
	total := lp.TotalBytes.Load()
	if total <= 0 {
		return 0
	}
	percent := float64(lp.LoadedBytes.Load()) * 100.0 / float64(total)
	if percent > 100 {
		percent = 100
	}
	return percent
}

// SearchResult represents a cell that matches search query
type SearchResult struct {
	Row int
	Col int
}

// initialize tview, buffer
func initView() {
	app = tview.NewApplication()
	app.EnableMouse(true) // Enable mouse support
	b = createNewBuffer()
	wrappedColumns = make(map[int]int) // Initialize wrapped columns map
	setSearchResults(nil)
	currentSearchIndex = -1
	searchQuery = ""
	searchUseRegex = false
	originalBuffer = nil // Initialize filter variables
	isFiltered = false
	activeFilters = make(map[int]FilterOptions) // Initialize active filters map
	currentCursorColumn = 0                     // Initialize cursor column
	lastGPress = time.Time{}                    // Initialize vim navigation state
	pendingCount = 0
	pendingChord = nil
	pendingOp, pendingOpCount = "", 0
	visual = visualOff
	edits = nil
}

// stop UI
func stopView() {
	app.Stop()
}

// updateFooterWithStatus updates the footer with a status message
func updateFooterWithStatus(status string) {
	drawFooterText(fileNameStr, status, cursorPosStr)
}

//help page content
