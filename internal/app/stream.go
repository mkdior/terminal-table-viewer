package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Streaming: a file too large to hold in memory is read from disk as it is
// shown. A background scan indexes the file, recording the byte offset of one
// row in every indexStride, so any row is found by reading forward from the
// mark before it; the rows between two marks are parsed together as a block
// and a few blocks are kept in a cache, so scrolling reads each block once.
// Whatever walks the whole table (a filter, a search) reads the file again,
// block by block on every CPU, in the background, with its progress in the
// footer. What needs the whole table in memory (sorting, editing, writing) is
// refused with a note. Lines are rows, exactly as for the in-memory loader:
// blank lines and the --skip flags apply, and a quoted field does not span
// lines. A filtered view of a streamed table is a list of the row numbers
// that matched, read through the same index.

const (
	indexStride       = 1024    // rows per index mark, and per parsed block
	streamCacheBlocks = 32      // parsed blocks kept for the table on screen
	streamSampleRows  = 1000    // rows read up front for the column count, widths and types
	streamReadBuffer  = 1 << 20 // read buffer of the indexer
	maxStreamMatches  = 10000   // search results kept for a streamed table
	maxStreamYankRows = 100000  // rows one yank may read from a streamed table
	statsSampleRows   = 100000  // rows statistics are computed over for a streamed table
)

// defaultStreamAbove is the file size from which a plain file is streamed
// rather than loaded, unless the config or --stream-above say otherwise.
const defaultStreamAbove = 1 << 30

// streamAbove is the size from which plain files are streamed; 0 turns the
// automatic choice off (--stream still forces it).
var streamAbove int64 = defaultStreamAbove

// lineRules turn a line of the file into a row the way the in-memory loader
// does: the same separator, --skip-prefix, --columns projection and NaN
// padding, so a streamed table looks like a loaded one.
type lineRules struct {
	sep      rune
	prefixes []string
	visCol   []int // columns kept by --columns/--hide-columns; nil for all
	colLen   int   // rows shorter than this are padded with NaN
}

// skip reports whether the line is not a row: blank, or with a skipped prefix.
func (lr *lineRules) skip(line string) bool {
	return line == "" || skipLine(line, lr.prefixes)
}

// parse splits a line into its cells.
func (lr *lineRules) parse(line string) []string {
	fields, err := lineCSVParseFast(line, lr.sep)
	if err != nil {
		fields = strings.Split(line, string(lr.sep))
	}
	if lr.visCol != nil {
		fields = projectColumns(fields, lr.visCol)
	}
	for len(fields) < lr.colLen {
		fields = append(fields, "NaN")
	}
	return fields
}

// streamBlock is the parsed rows of one index block.
type streamBlock struct {
	rows    [][]string
	end     int64 // the indexed end the block was read up to, for a partial block
	partial bool  // the last block while the index is still growing
	used    uint64
}

// rowStream reads the rows of a file through its index; a view (a filtered
// table) maps its rows onto the rows of the file's stream.
type rowStream struct {
	file  *os.File
	rules lineRules
	size  int64

	mu       sync.RWMutex
	marks    []int64 // byte offset of the first row of each block
	count    int     // rows indexed so far
	end      int64   // offset just past the last indexed row
	complete bool

	cmu   sync.Mutex
	cache map[int]*streamBlock
	ticks uint64 // for the cache's least-recently-used eviction

	// A view: the file's stream and the file rows shown, in order.
	root *rowStream
	sel  []int
}

// view makes a stream over the given rows of the file (the header first,
// when there is one).
func (s *rowStream) view(sel []int) *rowStream {
	root := s
	if s.root != nil {
		root = s.root
	}
	return &rowStream{root: root, sel: sel, count: len(sel), complete: true}
}

// rows returns how many rows the stream has (so far).
func (s *rowStream) rows() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// row returns row r, or nil when there is no such row yet.
func (s *rowStream) row(r int) []string {
	if s.root != nil {
		if r < 0 || r >= len(s.sel) {
			return nil
		}
		return s.root.row(s.sel[r])
	}
	if r < 0 {
		return nil
	}
	k := r / indexStride
	blk := s.block(k)
	if blk == nil || r-k*indexStride >= len(blk.rows) {
		return nil
	}
	return blk.rows[r-k*indexStride]
}

// block returns block k from the cache, reading it from the file on a miss
// or when it was partial and the index has grown since.
func (s *rowStream) block(k int) *streamBlock {
	s.cmu.Lock()
	blk, ok := s.cache[k]
	if ok && blk.partial {
		s.mu.RLock()
		grown := s.end > blk.end || s.complete
		s.mu.RUnlock()
		ok = !grown
	}
	if ok {
		s.ticks++
		blk.used = s.ticks
		s.cmu.Unlock()
		return blk
	}
	s.cmu.Unlock()

	rows, end, partial, ok := s.readBlock(k)
	if !ok {
		return nil
	}
	blk = &streamBlock{rows: rows, end: end, partial: partial}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if s.cache == nil {
		s.cache = make(map[int]*streamBlock, streamCacheBlocks)
	}
	for len(s.cache) >= streamCacheBlocks {
		oldest, oldestUse := -1, ^uint64(0)
		for i, c := range s.cache {
			if c.used < oldestUse {
				oldest, oldestUse = i, c.used
			}
		}
		delete(s.cache, oldest)
	}
	s.ticks++
	blk.used = s.ticks
	s.cache[k] = blk
	return blk
}

// span returns the byte range of block k: from its mark to the next one, or
// to the indexed end for the last block, which is partial while the index
// still grows. ok is false for a block that has no mark yet.
func (s *rowStream) span(k int) (start, stop int64, partial, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if k < 0 || k >= len(s.marks) {
		return 0, 0, false, false
	}
	start = s.marks[k]
	if k+1 < len(s.marks) {
		return start, s.marks[k+1], false, true
	}
	return start, s.end, !s.complete, true
}

// readBlock reads and parses the rows of block k from the file.
func (s *rowStream) readBlock(k int) (rows [][]string, end int64, partial, ok bool) {
	start, stop, partial, ok := s.span(k)
	if !ok || stop <= start {
		return nil, stop, partial, ok
	}
	return s.parseRange(start, stop, nil), stop, partial, true
}

// parseRange reads the lines between two offsets and parses those that are
// rows. With want set, only the rows at those positions within the range
// (counting rows from 0) are parsed; the others come back nil.
func (s *rowStream) parseRange(start, stop int64, want map[int]bool) [][]string {
	data := make([]byte, stop-start)
	n, err := s.file.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	data = data[:n]
	rows := make([][]string, 0, indexStride)
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		text := strings.TrimSuffix(string(line), "\r")
		if s.rules.skip(text) {
			continue
		}
		if want != nil && !want[len(rows)] {
			rows = append(rows, nil)
			continue
		}
		rows = append(rows, s.rules.parse(text))
	}
	return rows
}

// blocks returns how many blocks the index has (so far).
func (s *rowStream) blocks() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.marks)
}

// close releases the file; the stream must not be read after it.
func (s *rowStream) close() {
	if s.root == nil && s.file != nil {
		_ = s.file.Close()
	}
}

// openStream opens name for streaming into b: it reads the first rows to
// settle the separator, the column count, the widths and the types the way
// the loader does, and leaves the index to be built by index.
func openStream(name string, b *Buffer) (*rowStream, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New(name + " is a directory")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	s := &rowStream{file: file, size: info.Size()}
	s.rules.prefixes = args.SkipSymbol

	// The head: the first rows that survive the skip rules.
	reader := bufio.NewReaderSize(file, streamReadBuffer)
	skipRemaining := args.SkipNum
	var head []string
	for len(head) < streamSampleRows {
		line, err := readLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			_ = file.Close()
			return nil, err
		}
		if line == "" {
			continue
		}
		if skipRemaining > 0 {
			skipRemaining--
			continue
		}
		if skipLine(line, s.rules.prefixes) {
			continue
		}
		head = append(head, line)
	}
	sep := b.sep
	if sep == 0 {
		sample := head
		if len(sample) > separatorSampleLines {
			sample = sample[:separatorSampleLines]
		}
		sep = detectSeparator(name, sample)
	}
	if sep == 0 {
		_ = file.Close()
		return nil, errors.New(errSeparatorNotDetected)
	}
	s.rules.sep = sep

	b.mu.Lock()
	defer b.mu.Unlock()
	b.sep = sep
	b.setSourceUnsafe(info)
	for i, line := range head {
		fields, err := lineCSVParseFast(line, sep)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if i == 0 && (len(args.ShowNum) != 0 || len(args.HideNum) != 0) {
			if s.rules.visCol, err = getVisCol(args.ShowNum, args.HideNum, len(fields)); err != nil {
				_ = file.Close()
				return nil, err
			}
		}
		if s.rules.visCol != nil {
			fields = projectColumns(fields, s.rules.visCol)
		}
		if len(fields) > b.colLen {
			b.resizeColUnsafe(len(fields))
		}
		for c, cell := range fields {
			b.trackWidthUnsafe(c, cell)
		}
	}
	s.rules.colLen = b.colLen
	b.stream = s
	return s, nil
}

// readLine reads one line without its line ending. A line longer than the
// loader's limit is an error, as it is for the loader.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if len(line) > maxScanTokenSize {
		return "", fmt.Errorf("a line exceeds the %d byte line limit", maxScanTokenSize)
	}
	if err != nil && (len(line) == 0 || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

// indexPublishRows is how many rows are indexed between two updates of the
// published row count.
const indexPublishRows = 256

// index scans the whole file for rows, recording a mark per block and
// publishing the row count to b as it goes. When updates is not nil the
// first rows are signalled once (the UI waits for that), then every
// updateInterval rows without blocking. Progress goes to b's load progress,
// and the buffer's stop flag ends the scan early.
func (s *rowStream) index(b *Buffer, updates chan<- bool) error {
	b.progress.Reset(s.size)
	reader := bufio.NewReaderSize(io.NewSectionReader(s.file, 0, s.size), streamReadBuffer)
	var offset int64
	skipRemaining := args.SkipNum
	count, batch := 0, 0
	signalled := false
	var marks []int64
	publish := func(final bool) {
		s.mu.Lock()
		s.marks = append(s.marks, marks...)
		marks = marks[:0]
		s.count, s.end = count, offset
		s.complete = final
		s.mu.Unlock()
		b.mu.Lock()
		b.rowLen = count
		b.mu.Unlock()
		b.progress.LoadedBytes.Store(offset)
	}
	for {
		if b.stopLoad.Load() {
			return errLoadCancelled
		}
		if args.NLine > 0 && count >= args.NLine {
			break
		}
		raw, err := reader.ReadString('\n')
		if len(raw) > maxScanTokenSize {
			return fmt.Errorf("a line exceeds the %d byte line limit", maxScanTokenSize)
		}
		if err != nil && (len(raw) == 0 || !errors.Is(err, io.EOF)) {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		lineStart := offset
		offset += int64(len(raw))
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		switch {
		case line == "":
			continue
		case skipRemaining > 0:
			skipRemaining--
			continue
		case skipLine(line, s.rules.prefixes):
			continue
		}
		if count%indexStride == 0 {
			marks = append(marks, lineStart)
		}
		count++
		batch++
		if count%indexPublishRows == 0 {
			publish(false)
		}
		if updates != nil {
			if !signalled && count >= separatorSampleLines {
				publish(false)
				updates <- true
				signalled = true
			} else if signalled && batch >= updateInterval {
				select {
				case updates <- true:
					batch = 0
				default:
				}
			}
		}
		if err != nil { // EOF after a last line without a newline
			break
		}
	}
	publish(true)
	if updates != nil && !signalled {
		updates <- true
	}
	b.detectAllColumnTypes()
	b.progress.IsComplete.Store(true)
	return nil
}

// loadStream opens and indexes name synchronously.
func loadStream(name string, b *Buffer) error {
	s, err := openStream(name, b)
	if err != nil {
		return err
	}
	return s.index(b, nil)
}

// loadStreamAsync opens and indexes name for progressive display, with the
// same channels as loadFileToBufferAsync.
func loadStreamAsync(name string, b *Buffer, updateChan chan<- bool, doneChan chan<- error) {
	s, err := openStream(name, b)
	if err != nil {
		doneChan <- err
		return
	}
	doneChan <- s.index(b, updateChan)
}

// shouldStream decides whether a plain file (not a pipe, not gzip, which
// cannot be read at an offset) is streamed: always with --stream, else from
// the streamAbove size on.
func shouldStream(name string, pipe io.Reader) bool {
	if pipe != nil || strings.HasSuffix(name, ".gz") {
		return false
	}
	if args.Stream {
		return true
	}
	if streamAbove <= 0 {
		return false
	}
	info, err := os.Stat(name)
	return err == nil && info.Size() >= streamAbove
}

// parseSize reads a size such as "512M", "1GB", "2g" or "0" (bytes when no
// unit is given).
func parseSize(s string) (int64, error) {
	v := strings.ToUpper(strings.TrimSpace(s))
	v = strings.TrimSuffix(v, "B")
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"T", 1 << 40}} {
		if strings.HasSuffix(v, u.suffix) {
			v, mult = strings.TrimSuffix(v, u.suffix), u.mult
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(n * float64(mult)), nil
}

// A pass is a background walk over a streamed table (a filter, a search)
// with its progress in the footer; Esc cancels it. One runs at a time.
type pass struct {
	label     string
	cancelled atomic.Bool
	done      atomic.Int64
	total     atomic.Int64
}

// currentPass is the pass running, nil when none.
var currentPass *pass

// cancelPass stops the running pass, if any, and reports whether there was one.
func cancelPass() bool {
	if currentPass == nil {
		return false
	}
	currentPass.cancelled.Store(true)
	return true
}

// passRefresh is how often the footer shows a pass's progress.
const passRefresh = 100 * time.Millisecond

// passRunning reports whether a pass is running and, if so, says in the
// footer that Esc cancels it; callers that would change the table refuse.
func passRunning() bool {
	if currentPass == nil {
		return false
	}
	drawFooterText(fileNameStr, "Still "+strings.ToLower(currentPass.label)+"; Esc cancels it", cursorPosStr)
	return true
}

// runPass runs work in the background and hands its result to then on the
// UI goroutine, in the tab the pass was started from, unless the pass was
// cancelled, in which case cancelled runs instead and the footer says so.
// Without a running application (tests) it all happens at once. A pass
// already running refuses a second one.
func runPass[T any](label string, work func(p *pass) T, then func(T), cancelled func()) {
	if passRunning() {
		return
	}
	p := &pass{label: label}
	currentPass = p
	owner := loaded
	inOwner := func(fn func()) {
		if owner == nil {
			fn()
			return
		}
		if !owner.closed {
			withTab(owner, fn)
		}
	}
	finish := func(result T) {
		currentPass = nil
		inOwner(func() {
			if p.cancelled.Load() {
				drawFooterText(fileNameStr, label+" cancelled", cursorPosStr)
				if cancelled != nil {
					cancelled()
				}
				return
			}
			then(result)
		})
	}
	if !uiRunning.Load() {
		finish(work(p))
		return
	}
	drawFooterText(fileNameStr, label+"...  |  Esc cancels", cursorPosStr)
	go func() {
		ticker := time.NewTicker(passRefresh)
		defer ticker.Stop()
		out := make(chan T, 1)
		go func() { out <- work(p) }()
		for {
			select {
			case result := <-out:
				app.QueueUpdateDraw(func() { finish(result) })
				return
			case <-ticker.C:
				app.QueueUpdateDraw(func() {
					if currentPass != p || (owner != nil && loaded != owner) {
						return
					}
					done, total := p.done.Load(), p.total.Load()
					status := label + "...  |  Esc cancels"
					if total > 0 {
						status = fmt.Sprintf("%s... %d%%  |  Esc cancels", label, 100*done/total)
					}
					drawFooterText(fileNameStr, status, cursorPosStr)
				})
			}
		}
	}()
}

// blockJob is one block of the stream to walk: its number and, for a view,
// the positions within the block of the rows shown (nil for all rows).
type blockJob struct {
	k    int
	want map[int]bool
	rows []int // the file row numbers of the wanted rows, in order (views)
}

// jobs lists the blocks to walk: every complete block for the file, or, for
// a view, the blocks holding its rows.
func (s *rowStream) jobs() []blockJob {
	if s.root == nil {
		n := s.blocks()
		out := make([]blockJob, n)
		for k := range out {
			out[k] = blockJob{k: k}
		}
		return out
	}
	var out []blockJob
	for _, r := range s.sel {
		k := r / indexStride
		if len(out) == 0 || out[len(out)-1].k != k {
			out = append(out, blockJob{k: k, want: map[int]bool{}})
		}
		job := &out[len(out)-1]
		job.want[r-k*indexStride] = true
		job.rows = append(job.rows, r)
	}
	return out
}

// walk reads the blocks of the stream on every CPU and calls fn for each
// with the file row number of its first row and its rows (nil for rows not
// shown by a view), collecting fn's results in block order. It reports
// progress to p and stops early when p is cancelled.
func walk[T any](s *rowStream, p *pass, fn func(first int, rows [][]string) T) []T {
	root := s
	if s.root != nil {
		root = s.root
	}
	jobs := s.jobs()
	results := make([]T, len(jobs))
	p.total.Store(int64(len(jobs)))
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < max(1, runtime.NumCPU()); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(jobs) || p.cancelled.Load() {
					return
				}
				job := jobs[i]
				start, stop, _, ok := root.span(job.k)
				if ok && stop > start {
					results[i] = fn(job.k*indexStride, root.parseRange(start, stop, job.want))
				}
				p.done.Add(1)
			}
		}()
	}
	wg.Wait()
	return results
}

// filterStream applies filters (value filters first, then the unique ones,
// as applyActiveFilters orders them) to a streamed table and returns the
// view: a stream over the rows that matched, header first.
func filterStream(base *Buffer, cols []int, filters map[int]FilterOptions, p *pass) *Buffer {
	s := base.stream
	base.mu.RLock()
	rowFreeze := base.rowFreeze
	compiled := make([]compiledFilter, 0, len(cols))
	var uniques []int
	for _, c := range cols {
		if isUniqueOperator(filters[c].Operator) {
			uniques = append(uniques, c)
			continue
		}
		colType := colTypeStr
		if c < len(base.colType) {
			colType = base.colType[c]
		}
		compiled = append(compiled, compileFilter(filters[c], colType))
	}
	base.mu.RUnlock()

	type hit struct {
		row  int
		keys []string // one per unique filter, for the ordered dedupe after
	}
	blocks := walk(s, p, func(first int, rows [][]string) []hit {
		var hits []hit
		for i, row := range rows {
			r := first + i
			if row == nil || r < rowFreeze {
				continue
			}
			matched := true
			for j, f := range compiled {
				c := cols[j]
				if c >= len(row) || !f.match(row[c]) {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			h := hit{row: r}
			for _, c := range uniques {
				h.keys = append(h.keys, uniqueKey(row, c, filters[c]))
			}
			hits = append(hits, h)
		}
		return hits
	})
	// A view's blocks carry file row numbers; its own numbering is the order
	// of sel, which walk preserved, so the file numbers are the result.
	seen := make([]map[string]struct{}, len(uniques))
	for i := range seen {
		seen[i] = map[string]struct{}{}
	}
	sel := make([]int, 0, 1024)
	if rowFreeze > 0 {
		sel = append(sel, 0)
	}
	for _, hits := range blocks {
	next:
		for _, h := range hits {
			for i := range uniques {
				if _, dup := seen[i][h.keys[i]]; dup {
					continue next
				}
			}
			for i := range uniques {
				seen[i][h.keys[i]] = struct{}{}
			}
			sel = append(sel, h.row)
		}
	}
	return base.streamView(sel)
}

// streamView makes the buffer of a view over sel, sharing the table's
// metadata (types, widths, frozen rows and columns).
func (b *Buffer) streamView(sel []int) *Buffer {
	b.mu.RLock()
	defer b.mu.RUnlock()
	view := createNewBuffer()
	view.sep = b.sep
	view.colLen = b.colLen
	view.rowFreeze = b.rowFreeze
	view.colFreeze = b.colFreeze
	view.colType = append([]int(nil), b.colType...)
	view.colWidth = append([]int(nil), b.colWidth...)
	view.stream = b.stream.view(sel)
	view.rowLen = len(sel)
	view.progress.IsComplete.Store(true)
	return view
}

// searchStream finds the cells of a streamed table matching the query, in
// row order, keeping at most maxStreamMatches.
func searchStream(b *Buffer, query string, useRegex, caseSensitive bool, p *pass) []SearchResult {
	match, ok := searchMatcher(query, useRegex, caseSensitive)
	if !ok {
		return nil
	}
	blocks := walk(b.stream, p, func(first int, rows [][]string) []SearchResult {
		var out []SearchResult
		for i, row := range rows {
			if row == nil {
				continue
			}
			for c, cell := range row {
				if match(cell) {
					out = append(out, SearchResult{Row: first + i, Col: c})
				}
			}
		}
		return out
	})
	var results []SearchResult
	for _, part := range blocks {
		results = append(results, part...)
		if len(results) >= maxStreamMatches {
			return results[:maxStreamMatches]
		}
	}
	// A view numbers its rows by position in sel, not by file row.
	if s := b.stream; s.root != nil {
		pos := make(map[int]int, len(s.sel))
		for i, r := range s.sel {
			pos[r] = i
		}
		for i := range results {
			results[i].Row = pos[results[i].Row]
		}
	}
	return results
}

// column returns the first limit values of column c of a streamed table (the
// header included, as getCol does), for statistics.
func (s *rowStream) column(c, limit int) []string {
	n := min(s.rows(), limit)
	out := make([]string, 0, n)
	for r := 0; r < n; r++ {
		row := s.row(r)
		if c < len(row) {
			out = append(out, row[c])
		} else {
			out = append(out, "")
		}
	}
	return out
}
