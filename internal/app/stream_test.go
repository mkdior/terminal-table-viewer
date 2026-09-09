package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivo/tview"
)

// streamFile writes a CSV of n data rows (id,name,value with a few quirks:
// CRLF endings on some lines, blank lines, comment lines, a short row) to a
// temp file and returns its path.
func streamFile(t *testing.T, n int) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("# a comment line above the header\n")
	sb.WriteString("id,name,value\n")
	for i := 1; i <= n; i++ {
		switch {
		case i%500 == 0:
			sb.WriteString("\n") // a blank line is not a row
		case i%700 == 0:
			sb.WriteString("# a comment between rows\n")
		}
		if i == 4244 {
			fmt.Fprintf(&sb, "%d,short\n", i) // a short row is padded
		} else if i%3 == 0 {
			fmt.Fprintf(&sb, "%d,name%d,%d\r\n", i, i, i*10)
		} else {
			fmt.Fprintf(&sb, "%d,name%d,%d\n", i, i, i*10)
		}
	}
	path := filepath.Join(t.TempDir(), "stream.csv")
	if err := os.WriteFile(path, []byte(sb.String()), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

// streamed loads path through the streaming path into a fresh buffer with
// the comment prefix skipped.
func streamed(t *testing.T, path string) *Buffer {
	t.Helper()
	oldArgs := args
	t.Cleanup(func() { args = oldArgs })
	args.setDefault()
	args.SkipSymbol = []string{"#"}
	buf := createNewBuffer()
	buf.rowFreeze, buf.colFreeze = 1, 1
	if err := loadStream(path, buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

func TestStreamIndexesRowsAcrossBlocks(t *testing.T) {
	const n = 5000
	buf := streamed(t, streamFile(t, n))
	if !buf.streamed() || buf.rowLen != n+1 || buf.colLen != 3 || !buf.progress.IsComplete.Load() {
		t.Fatalf("streamed %v rows %d cols %d complete %v", buf.streamed(), buf.rowLen, buf.colLen, buf.progress.IsComplete.Load())
	}
	if got := buf.stream.blocks(); got != (n+1+indexStride-1)/indexStride {
		t.Errorf("blocks = %d", got)
	}
	if got := strings.Join(buf.row(0), ","); got != "id,name,value" {
		t.Errorf("header %q", got)
	}
	for _, r := range []int{1, 2, 3, indexStride - 1, indexStride, indexStride + 1, 2500, 4241, 4243, n} {
		want := fmt.Sprintf("%d,name%d,%d", r, r, r*10)
		if got := strings.Join(buf.row(r), ","); got != want {
			t.Errorf("row %d = %q, want %q", r, got, want)
		}
	}
	if got := strings.Join(buf.row(4244), ","); got != "4244,short,NaN" {
		t.Errorf("a short row is padded: %q", got)
	}
	if buf.row(n+1) != nil || buf.row(-1) != nil {
		t.Error("rows past the end are nil")
	}
	if cell, ok := buf.cellAt(7, 1); !ok || cell != "name7" {
		t.Errorf("cellAt = %q %v", cell, ok)
	}
	if buf.getColType(0) != colTypeFloat || buf.getColType(1) != colTypeStr || buf.getColType(2) != colTypeFloat {
		t.Errorf("types %v", buf.colType[:3])
	}
	if w := buf.columnWidth(1); w != len("name999") { // the head holds 999 data rows
		t.Errorf("widths come from the head sample: %d", w)
	}
	if block := buf.cellBlock(1023, 0, 1025, 2); len(block) != 3 || block[1][0] != "1024" || block[2][2] != "10250" {
		t.Errorf("cellBlock across a block boundary: %v", block)
	}
	if col := buf.getCol(2); len(col) != n+1 || col[0] != "value" || col[n] != fmt.Sprint(n*10) {
		t.Errorf("getCol: %d values, %q %q", len(col), col[0], col[len(col)-1])
	}
	if len(buf.stream.cache) > streamCacheBlocks {
		t.Errorf("cache holds %d blocks", len(buf.stream.cache))
	}
}

func TestStreamHonoursSkipLinesLimitAndColumns(t *testing.T) {
	path := streamFile(t, 300)
	oldArgs := args
	t.Cleanup(func() { args = oldArgs })
	args.setDefault()
	args.SkipNum = 1 // the comment line, so no prefix is needed
	args.NLine = 50
	args.ShowNum = []int{1, 3}
	buf := createNewBuffer()
	buf.rowFreeze = 1
	if err := loadStream(path, buf); err != nil {
		t.Fatal(err)
	}
	if buf.rowLen != 50 || buf.colLen != 2 {
		t.Fatalf("rows %d cols %d", buf.rowLen, buf.colLen)
	}
	if got := strings.Join(buf.row(0), ","); got != "id,value" {
		t.Errorf("projected header %q", got)
	}
	if got := strings.Join(buf.row(49), ","); got != "49,490" {
		t.Errorf("last row %q", got)
	}
}

func TestStreamFilterSearchAndViews(t *testing.T) {
	const n = 3000
	buf := streamed(t, streamFile(t, n))
	p := &pass{}
	// A value filter over the file, then a filter on the view it made.
	view := filterStream(buf, []int{1}, map[int]FilterOptions{1: {Query: "name1", Operator: "starts with"}}, p)
	want := 0
	for i := 1; i <= n; i++ {
		if strings.HasPrefix(fmt.Sprintf("name%d", i), "name1") {
			want++
		}
	}
	if view.rowLen != want+1 || !view.streamed() || view.row(0)[0] != "id" || view.row(1)[0] != "1" || view.row(2)[0] != "10" {
		t.Fatalf("view rows %d (want %d), first rows %v %v", view.rowLen, want+1, view.row(1), view.row(2))
	}
	if p.done.Load() != p.total.Load() || p.total.Load() != int64(buf.stream.blocks()) {
		t.Errorf("progress %d/%d", p.done.Load(), p.total.Load())
	}
	// value >= 1000 within the view keeps ids 100..199 and 1000..1999.
	narrower := filterStream(view, []int{2}, map[int]FilterOptions{2: {Query: "1000", Operator: ">="}}, &pass{})
	if narrower.rowLen != 1101 || narrower.row(1)[0] != "100" || narrower.row(narrower.rowLen - 1)[0] != "1999" {
		t.Errorf("chained view: %d rows, %v .. %v", narrower.rowLen, narrower.row(1), narrower.row(narrower.rowLen-1))
	}
	// Unique keeps the first row per key, after the value filters.
	uniq := filterStream(buf, []int{1, 2}, map[int]FilterOptions{
		1: {Query: "name", Operator: "contains"},
		2: {Operator: opUnique},
	}, &pass{})
	if uniq.rowLen != n+1 { // every value is distinct
		t.Errorf("unique over distinct values keeps all: %d", uniq.rowLen)
	}
	same := filterStream(buf, []int{1, 2}, map[int]FilterOptions{
		1: {Query: "name1", Operator: "starts with"},
		2: {Query: "^1[0-9]?0$", Operator: "regex"},
	}, &pass{})
	if same.rowLen != 12 || same.row(1)[0] != "1" || same.row(11)[0] != "19" { // values 10 and 100..190: ids 1 and 10..19, and the header
		t.Errorf("two value filters: %d rows %v %v", same.rowLen, same.row(1), same.row(11))
	}
	dup := filterStream(buf, []int{0}, map[int]FilterOptions{0: {Operator: opUniqueRows}}, &pass{})
	if dup.rowLen != n+1 {
		t.Errorf("unique rows: %d", dup.rowLen)
	}
	// Search over the file and over a view, in row order, with view numbering.
	hits := searchStream(buf, "name2999", false, false, &pass{})
	if len(hits) != 1 || hits[0] != (SearchResult{Row: 2999, Col: 1}) {
		t.Errorf("search hits %v", hits)
	}
	hits = searchStream(view, "^name1[0-9]$", true, false, &pass{})
	if len(hits) != 10 || hits[0].Row != 2 || hits[0].Col != 1 || view.row(hits[0].Row)[0] != "10" || view.row(hits[9].Row)[0] != "19" {
		t.Errorf("search over a view: %v", hits)
	}
	// A cancelled pass stops early.
	cancelled := &pass{}
	cancelled.cancelled.Store(true)
	if out := filterStream(buf, []int{1}, map[int]FilterOptions{1: {Query: "name", Operator: "contains"}}, cancelled); out.rowLen > 1 || cancelled.done.Load() != 0 {
		t.Errorf("cancelled pass produced %d rows after %d blocks", out.rowLen, cancelled.done.Load())
	}
}

func TestStreamCancelStopsIndexing(t *testing.T) {
	path := streamFile(t, 3000)
	oldArgs := args
	t.Cleanup(func() { args = oldArgs })
	args.setDefault()
	args.SkipSymbol = []string{"#"}
	buf := createNewBuffer()
	buf.stopLoad.Store(true)
	err := loadStream(path, buf)
	if !errors.Is(err, errLoadCancelled) || buf.rowLen != 0 || buf.progress.IsComplete.Load() {
		t.Errorf("err %v rows %d complete %v", err, buf.rowLen, buf.progress.IsComplete.Load())
	}
	buf.stream.close()
}

func TestParseSizeAndShouldStream(t *testing.T) {
	cases := map[string]int64{"0": 0, "512": 512, "512M": 512 << 20, "1G": 1 << 30, "1GB": 1 << 30, "2g": 2 << 30, "1.5k": 1536, " 3 MB ": 3 << 20}
	for in, want := range cases {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "-1", "1X", "MB"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) must fail", bad)
		}
	}
	path := streamFile(t, 100)
	oldArgs, oldAbove := args, streamAbove
	t.Cleanup(func() { args, streamAbove = oldArgs, oldAbove })
	args.setDefault()
	streamAbove = 1
	if !shouldStream(path, nil) || shouldStream(path+".gz", nil) || shouldStream(pipeSourceName, strings.NewReader("")) {
		t.Error("a plain file over the size streams; gzip and pipes never do")
	}
	streamAbove = 1 << 40
	if shouldStream(path, nil) {
		t.Error("a small file loads")
	}
	streamAbove = 0
	args.Stream = true
	if !shouldStream(path, nil) {
		t.Error("--stream forces it")
	}
	args.Stream = false
	if shouldStream(path, nil) {
		t.Error("0 turns the automatic choice off")
	}
}

// setupStreamTab opens path as the only tab, streamed and loaded
// synchronously, with an application and UI container.
func setupStreamTab(t *testing.T, path string) {
	t.Helper()
	setupEditTable(t)
	oldArgs, oldUI, oldApp, oldIDs, oldAbove, oldPass := args, UI, app, tabIDs, streamAbove, currentPass
	t.Cleanup(func() {
		args, UI, app, tabIDs, streamAbove, currentPass = oldArgs, oldUI, oldApp, oldIDs, oldAbove, oldPass
		tabs, current, loaded = nil, -1, nil
		visual, cellEdit = visualOff, nil
	})
	args.setDefault()
	args.AsyncLoad, args.Stream = false, true
	args.SkipSymbol = []string{"#"}
	app = tview.NewApplication()
	tabIDs = 0
	if _, err := openTabs([]string{path}, nil, 0); err != nil {
		t.Fatal(err)
	}
}

func TestStreamedTableIsReadOnlyInTheUI(t *testing.T) {
	setupStreamTab(t, streamFile(t, 2500))
	if !b.streamed() || b.rowLen != 2501 || !strings.Contains(fileNameStr, "stream.csv [streamed]") {
		t.Fatalf("streamed %v rows %d footer %q", b.streamed(), b.rowLen, fileNameStr)
	}
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	for _, k := range []string{"d d", "x", "s", "S", "E", "i r", "p", "W"} {
		press(t, k)
		if dirty() || b.rowLen != 2501 || cellEdit != nil {
			t.Errorf("%q must not change a streamed table: dirty %v rows %d editor %v", k, dirty(), b.rowLen, cellEdit)
		}
		if k != "W" && !strings.HasPrefix(statusMessage, "Read-only: stream.csv is streamed from disk") {
			t.Errorf("%q: status %q", k, statusMessage)
		}
	}
	if statusMessage != "Not written: the file is streamed from disk and read-only" {
		t.Errorf("W: %q", statusMessage)
	}
	press(t, "G y") // yanks read through the index
	if got := (*ran)[len(*ran)-1]; got != "xclip:2500" {
		t.Errorf("yank %q", got)
	}
	press(t, "g g V G y")
	if got := (*ran)[len(*ran)-1]; !strings.HasPrefix(got, "xclip:1\tname1\t10\n") || !strings.HasSuffix(got, "2500\tname2500\t25000") {
		t.Errorf("row yank through the index: %.40q...", got)
	}
	if r, _ := bufferTable.GetSelection(); r != 2500 {
		t.Errorf("cursor %d", r)
	}
	press(t, "t") // types are metadata and may change
	if b.getColType(0) != colTypeDate {
		t.Errorf("type toggle: %s", type2name(b.getColType(0)))
	}
	press(t, "t t")
}

func TestStreamedFilterAndSearchThroughTheUI(t *testing.T) {
	setupStreamTab(t, streamFile(t, 2500))
	// Filters run as a pass (synchronous here, without a running application).
	originalBuffer = b
	activeFilters[1] = FilterOptions{Query: "name24", Operator: "starts with"}
	var applied *Buffer
	deriveFilteredView(func(view *Buffer) { applied = view }, nil)
	// name24, name240..name249 and name2400..name2499: 111 rows and the header.
	if applied == nil || applied.rowLen != 112 || applied.row(1)[0] != "24" || applied.row(11)[0] != "249" || applied.row(111)[0] != "2499" {
		t.Fatalf("filtered view: %d rows", applied.rowLen)
	}
	b, isFiltered = applied, true
	drawBuffer(b, bufferTable)
	// Removing the filter through r restores the streamed table.
	bufferTable.Select(1, 1)
	press(t, "r")
	if b != originalBuffer || isFiltered || len(activeFilters) != 0 || statusMessage != "All filters cleared - showing all rows" {
		t.Errorf("r: filtered %v filters %v status %q", isFiltered, activeFilters, statusMessage)
	}
	// Search through the shared helper.
	var found []SearchResult
	searchTable(b, "name777", false, false, func(results []SearchResult) { found = results })
	if len(found) != 1 || found[0].Row != 777 {
		t.Errorf("search %v", found)
	}
	// While a pass runs, another is refused and Esc cancels the running one.
	p := &pass{label: "Filtering"}
	currentPass = p
	if !passRunning() || !strings.Contains(statusMessage, "Still filtering; Esc cancels it") {
		t.Errorf("a second pass is refused: %q", statusMessage)
	}
	press(t, "f")
	if UI.HasPage("filterModal") {
		t.Error("the filter dialog must not open during a pass")
	}
	press(t, "esc")
	if !p.cancelled.Load() {
		t.Error("Esc cancels the running pass")
	}
	currentPass = nil
	// A pass that ends cancelled runs the cancelled hook instead of then.
	ran := ""
	runPass("Filtering", func(p *pass) int { p.cancelled.Store(true); return 1 }, func(int) { ran = "then" }, func() { ran = "cancelled" })
	if ran != "cancelled" || statusMessage != "Filtering cancelled" || currentPass != nil {
		t.Errorf("cancelled pass: ran %q status %q", ran, statusMessage)
	}
}

func TestStreamedTabLoadsAsyncAndReportsIndexing(t *testing.T) {
	setupEditTable(t)
	path := streamFile(t, 4000)
	oldArgs, oldUI, oldApp, oldIDs := args, UI, app, tabIDs
	t.Cleanup(func() {
		args, UI, app, tabIDs = oldArgs, oldUI, oldApp, oldIDs
		tabs, current, loaded = nil, -1, nil
	})
	args.setDefault()
	args.Stream = true
	args.SkipSymbol = []string{"#"}
	app = tview.NewApplication()
	tabIDs = 0
	st, err := openTab(path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.done == nil || !st.base.streamed() || st.base.rowCount() < separatorSampleLines {
		t.Fatalf("async stream: done %v streamed %v rows %d", st.done != nil, st.base.streamed(), st.base.rowCount())
	}
	if err := <-st.done; err != nil {
		t.Fatal(err)
	}
	tabs = []*tab{st}
	buildUI()
	showTab(0)
	st.loadTick()
	if !strings.HasPrefix(statusMessage, "Indexing... [") {
		t.Errorf("tick status %q", statusMessage)
	}
	st.loadFinished(nil)
	if statusMessage != "Indexed 4001 rows; streamed from disk, read-only" || b.rowLen != 4001 {
		t.Errorf("finished status %q rows %d", statusMessage, b.rowLen)
	}
	if got := strings.Join(b.row(4000), ","); got != "4000,name4000,40000" {
		t.Errorf("last row %q", got)
	}
	st.closed = true
	st.release()
	if buf := b.row(1); buf != nil && len(b.stream.cache) == 0 {
		t.Error("after release the file is closed; nothing more is read")
	}
}
