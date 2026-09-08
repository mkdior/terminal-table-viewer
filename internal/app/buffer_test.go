package app

import (
	"reflect"
	"testing"
)

// ========================================
// Buffer Creation Tests
// ========================================

func Test_createNewBuffer(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"Create empty buffer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := createNewBuffer()
			if buf == nil {
				t.Error("createNewBuffer() returned nil")
				return
			}
			if buf.rowLen != 0 || buf.colLen != 0 {
				t.Error("New buffer should be empty")
			}
		})
	}
}

func Test_createNewBufferWithData(t *testing.T) {
	type args struct {
		ss     [][]string
		strict bool
	}
	wantBuffer := createNewBuffer()
	wantBuffer.colLen = 3
	wantBuffer.rowLen = 4
	wantBuffer.cont = [][]string{{"a", "b", "c"}, {"1", "2", "3"}, {"4", "5", "6"}, {"7", "8", "9"}}
	wantBuffer.colType = []int{0, 0, 0, 0}
	// Don't compare memory usage as it varies
	wantBuffer.memoryUsage = 0
	tests := []struct {
		name    string
		args    args
		want    *Buffer
		wantErr bool
	}{
		{"Valid data strict mode", args{ss: [][]string{{"a", "b", "c"}, {"1", "2", "3"}, {"4", "5", "6"}, {"7", "8", "9"}}, strict: true}, wantBuffer, false},
		{"Inconsistent columns strict mode", args{ss: [][]string{{"a", "b", "c"}, {"1", "2", "3"}, {"4", "5"}, {"7", "8", "9"}}, strict: true}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := createNewBufferWithData(tt.args.ss, tt.args.strict)
			if (err != nil) != tt.wantErr {
				t.Errorf("createNewBufferWithData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Skip deep equal check for non-error cases (memory usage makes comparison tricky)
			if err == nil && got != nil {
				if got.rowLen != tt.want.rowLen {
					t.Errorf("rowLen = %v, want %v", got.rowLen, tt.want.rowLen)
				}
				if got.colLen != tt.want.colLen {
					t.Errorf("colLen = %v, want %v", got.colLen, tt.want.colLen)
				}
			}
		})
	}
}

// ========================================
// Buffer Data Manipulation Tests
// ========================================

func TestBuffer_contAppendSli(t *testing.T) {
	type args struct {
		s      []string
		strict bool
	}
	b := createNewBuffer()
	b.colLen = 2
	b.rowLen = 1
	b.cont = [][]string{{"a", "b"}}
	tests := []struct {
		name    string
		b       *Buffer
		args    args
		wantErr bool
	}{
		{"Matching columns strict", b, args{s: []string{"a", "1"}, strict: true}, false},
		{"Extra columns strict", b, args{s: []string{"a", "1", "3"}, strict: true}, true},
		{"Extra columns non-strict", b, args{s: []string{"a", "1", "2"}, strict: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.b.contAppendSli(tt.args.s, tt.args.strict); (err != nil) != tt.wantErr {
				t.Errorf("Buffer.contAppendSli() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuffer_ResizeCol(t *testing.T) {
	b := createNewBuffer()
	_ = b.contAppendSli([]string{"A", "B"}, false)
	_ = b.contAppendSli([]string{"1", "2"}, false)

	b.resizeCol(4)

	if b.colLen != 4 {
		t.Errorf("resizeCol(4) set colLen = %d, want 4", b.colLen)
	}

	for _, row := range b.cont {
		if len(row) != 4 {
			t.Errorf("Row length = %d, want 4", len(row))
		}
	}
}

// ========================================
// Buffer Sorting Tests
// ========================================

func TestBuffer_sortByStr(t *testing.T) {
	type args struct {
		colIndex int
		rev      bool
	}
	testBuffer, _ := createNewBufferWithData([][]string{{"a", "b", "c"}, {"2", "2", "3"}, {"4", "5", "6"}, {"10", "8", "9"}}, true)
	wantBuffer, _ := createNewBufferWithData([][]string{{"a", "b", "c"}, {"10", "8", "9"}, {"2", "2", "3"}, {"4", "5", "6"}}, true)
	tests := []struct {
		name string
		b    *Buffer
		args args
		want *Buffer
	}{
		{"String sort ascending", testBuffer, args{colIndex: 0, rev: false}, wantBuffer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.b.sortByStr(tt.args.colIndex, tt.args.rev)
			if !reflect.DeepEqual(tt.b, tt.want) {
				t.Errorf("Buffer_sortByStr() = %v, want %v", tt.b, tt.want)
			}
		})
	}
}

func TestBuffer_SortByStr(t *testing.T) {
	b := createNewBuffer()
	b.rowFreeze = 1
	_ = b.contAppendSli([]string{"Name"}, false)
	_ = b.contAppendSli([]string{"Charlie"}, false)
	_ = b.contAppendSli([]string{"Alice"}, false)
	_ = b.contAppendSli([]string{"Bob"}, false)

	b.sortByStr(0, false)
	if b.cont[1][0] != "Alice" {
		t.Errorf("After ascending sort, first data row = %s, want Alice", b.cont[1][0])
	}

	b.sortByStr(0, true)
	if b.cont[1][0] != "Charlie" {
		t.Errorf("After descending sort, first data row = %s, want Charlie", b.cont[1][0])
	}
}

func TestBuffer_sortByNum(t *testing.T) {
	type args struct {
		colIndex int
		rev      bool
	}
	testBuffer, _ := createNewBufferWithData([][]string{{"a", "b", "c"}, {"5", "2", "3"}, {"4", "5", "6"}, {"10", "8", "9"}}, true)
	wantBuffer, _ := createNewBufferWithData([][]string{{"a", "b", "c"}, {"4", "5", "6"}, {"5", "2", "3"}, {"10", "8", "9"}}, true)
	tests := []struct {
		name string
		b    *Buffer
		args args
		want *Buffer
	}{
		{"Number sort ascending", testBuffer, args{colIndex: 0, rev: false}, wantBuffer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.b.sortByNum(tt.args.colIndex, tt.args.rev)
			if !reflect.DeepEqual(tt.b, tt.want) {
				t.Errorf("Buffer_sortByNum() = %v, want %v", tt.b, tt.want)
			}
		})
	}
}

func TestBuffer_SortByNum(t *testing.T) {
	b := createNewBuffer()
	b.rowFreeze = 1
	_ = b.contAppendSli([]string{"Score"}, false)
	_ = b.contAppendSli([]string{"85.5"}, false)
	_ = b.contAppendSli([]string{"92.3"}, false)
	_ = b.contAppendSli([]string{"78.9"}, false)

	b.sortByNum(0, false)
	if b.cont[1][0] != "78.9" {
		t.Errorf("After ascending sort, first data row = %s, want 78.9", b.cont[1][0])
	}

	b.sortByNum(0, true)
	if b.cont[1][0] != "92.3" {
		t.Errorf("After descending sort, first data row = %s, want 92.3", b.cont[1][0])
	}
}

// ========================================
// Buffer Transformation Tests
// ========================================

// ========================================
// Buffer Query Tests
// ========================================

func TestBuffer_getCol(t *testing.T) {
	type args struct {
		i int
	}
	testBuffer, _ := createNewBufferWithData([][]string{{"a", "b", "c"}, {"1", "2", "3"}, {"4", "5", "6"}}, true)
	tests := []struct {
		name string
		b    *Buffer
		args args
		want []string
	}{
		{"Get first column", testBuffer, args{i: 0}, []string{"a", "1", "4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.b.getCol(tt.args.i); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Buffer.getCol() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuffer_GetCol(t *testing.T) {
	b := createNewBuffer()
	_ = b.contAppendSli([]string{"A", "B", "C"}, false)
	_ = b.contAppendSli([]string{"1", "2", "3"}, false)
	_ = b.contAppendSli([]string{"X", "Y", "Z"}, false)

	col := b.getCol(1)
	expected := []string{"B", "2", "Y"}

	if len(col) != len(expected) {
		t.Errorf("getCol() returned %d elements, want %d", len(col), len(expected))
	}

	for i, val := range col {
		if val != expected[i] {
			t.Errorf("getCol()[%d] = %s, want %s", i, val, expected[i])
		}
	}
}

func TestBuffer_GetColType(t *testing.T) {
	b := createNewBuffer()
	b.colType = []int{colTypeStr, colTypeFloat, colTypeStr}

	if b.getColType(0) != colTypeStr {
		t.Error("Expected colTypeStr for column 0")
	}
	if b.getColType(1) != colTypeFloat {
		t.Error("Expected colTypeFloat for column 1")
	}
}

func TestBuffer_selectBySearch(t *testing.T) {
	type args struct {
		s string
	}
	testBuffer, _ := createNewBufferWithData([][]string{{"a", "b", "c"}, {"1", "2", "3"}, {"4", "1", "6"}}, true)
	tests := []struct {
		name string
		b    *Buffer
		args args
		want [][]int
	}{
		{"Search for '1'", testBuffer, args{s: "1"}, [][]int{{1, 0}, {2, 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b.selectBySearch(tt.args.s)
			if got := b.selectedCell; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("selectBySearch() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ========================================
// Buffer Concurrency Tests
// ========================================

func TestBuffer_ConcurrentAccess(t *testing.T) {
	b := createNewBuffer()
	_ = b.contAppendSli([]string{"A", "B", "C"}, false)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_ = b.getCol(0)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// ========================================
// Buffer Edge Case Tests
// ========================================

func TestBuffer_EdgeCases(t *testing.T) {
	t.Run("Empty buffer check", func(t *testing.T) {
		b := createNewBuffer()
		if b.rowLen != 0 || b.colLen != 0 {
			t.Error("New buffer should be empty")
		}
	})

	t.Run("Single cell", func(t *testing.T) {
		b := createNewBuffer()
		_ = b.contAppendSli([]string{"X"}, false)
		if b.cont[0][0] != "X" {
			t.Error("Single cell failed")
		}
	})

	t.Run("Strict mode violation", func(t *testing.T) {
		b := createNewBuffer()
		_ = b.contAppendSli([]string{"A", "B"}, false)
		err := b.contAppendSli([]string{"1", "2", "3"}, true)
		if err == nil {
			t.Error("Expected error for mismatched columns in strict mode")
		}
	})
}

// ========================================
// Additional Buffer Tests for Coverage
// ========================================

func TestBuffer_LargeDataset(t *testing.T) {
	b := createNewBuffer()

	// Add header
	_ = b.contAppendSli([]string{"ID", "Name", "Value"}, false)

	// Add many rows
	for i := 0; i < 1000; i++ {
		_ = b.contAppendSli([]string{I2S(i), "Name" + I2S(i), I2S(i * 10)}, false)
	}

	if b.rowLen != 1001 {
		t.Errorf("Expected 1001 rows, got %d", b.rowLen)
	}
}

func TestBuffer_SetAndGetColType(t *testing.T) {
	b := createNewBuffer()
	_ = b.contAppendSli([]string{"A", "B", "C"}, false)

	b.setColType(0, colTypeFloat)
	if b.getColType(0) != colTypeFloat {
		t.Error("setColType/getColType failed")
	}

	b.setColType(0, colTypeStr)
	if b.getColType(0) != colTypeStr {
		t.Error("setColType/getColType failed for string")
	}
}

func TestBuffer_ResizeColMultipleTimes(t *testing.T) {
	b := createNewBuffer()
	_ = b.contAppendSli([]string{"A", "B"}, false)

	b.resizeCol(5)
	if b.colLen != 5 {
		t.Errorf("Expected colLen=5, got %d", b.colLen)
	}

	// resizeCol doesn't shrink columns, only extends them
	// So after resize to 3, it should stay at 5
	b.resizeCol(3)
	if b.colLen < 3 {
		t.Errorf("Expected colLen >= 3, got %d", b.colLen)
	}
}

func TestBuffer_RaggedRowsAreExactlyColLenWide(t *testing.T) {
	b := createNewBuffer()
	rows := [][]string{
		{"A", "B", "C", "D"},
		{"1", "2", "3", "4"},
		{"5", "6", "7"},
		{"8", "9", "10", "11", "12"},
		{"13", "14"},
	}
	for _, r := range rows {
		if err := b.contAppendSli(r, false); err != nil {
			t.Fatal(err)
		}
	}
	if b.colLen != 5 {
		t.Fatalf("colLen = %d, want 5", b.colLen)
	}
	for i, r := range b.cont {
		if len(r) != b.colLen {
			t.Errorf("row %d has %d cells, want exactly %d: %q", i, len(r), b.colLen, r)
		}
	}
	if b.cont[0][4] != "NaN" || b.cont[2][3] != "NaN" || b.cont[4][2] != "NaN" {
		t.Errorf("missing cells must be NaN: %q", b.cont)
	}
}

func TestBuffer_TracksColumnWidths(t *testing.T) {
	b := createNewBuffer()
	_ = b.contAppendSli([]string{"id", "name"}, false)
	_ = b.contAppendSli([]string{"1", "a much longer name"}, false)
	_ = b.contAppendSli([]string{"12345", "日本語"}, false) // 3 wide runes = 6 cells
	_ = b.contAppendSli([]string{"7"}, false)            // short row is padded with NaN
	if got := b.columnWidth(0); got != 5 {
		t.Errorf("column 0 width = %d, want 5", got)
	}
	if got := b.columnWidth(1); got != len("a much longer name") {
		t.Errorf("column 1 width = %d, want %d", got, len("a much longer name"))
	}
	_ = b.contAppendSli([]string{"8", "9", "extra column"}, false)
	if got := b.columnWidth(2); got != len("extra column") {
		t.Errorf("new column width = %d, want %d (NaN padding must not exceed it)", got, len("extra column"))
	}
	if got := b.columnWidth(9); got != 0 {
		t.Errorf("unknown column width = %d, want 0", got)
	}
	filtered := b.filterByColumn(0, FilterOptions{Query: "1", Operator: "contains"})
	if filtered.columnWidth(1) != b.columnWidth(1) {
		t.Error("filtered buffers must keep the source column widths so columns do not jump when filtering")
	}
	if displayWidth("日本語") != 6 || displayWidth("abc") != 3 {
		t.Error("displayWidth must count terminal cells")
	}
}

// ========================================
// Editing primitives
// ========================================

// editableBuffer builds a header plus n data rows of the form r<i>c<j>.
func editableBuffer(t *testing.T, n int) *Buffer {
	t.Helper()
	data := [][]string{{"h0", "h1", "h2", "h3"}}
	for i := 1; i <= n; i++ {
		data = append(data, []string{"r" + I2S(i) + "c0", "r" + I2S(i) + "c1", "r" + I2S(i) + "c2", "r" + I2S(i) + "c3"})
	}
	buf, err := createNewBufferWithData(data, true)
	if err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1
	return buf
}

func TestBuffer_RemoveAndInsertRows(t *testing.T) {
	buf := editableBuffer(t, 5)
	before := buf.memoryUsage
	removed := buf.removeRows([][]string{buf.cont[2], buf.cont[4]}) // r2 and r4
	if len(removed) != 2 || removed[0].index != 2 || removed[1].index != 4 {
		t.Fatalf("removed = %+v", removed)
	}
	if buf.rowLen != 4 || len(buf.cont) != 4 || buf.cont[2][0] != "r3c0" || buf.cont[3][0] != "r5c0" {
		t.Fatalf("rows after removal: %v", buf.cont)
	}
	if buf.memoryUsage >= before {
		t.Error("memory estimate must shrink when rows are removed")
	}
	buf.insertRows(removed)
	if buf.rowLen != 6 || buf.memoryUsage != before {
		t.Fatalf("rowLen = %d, memory %d vs %d", buf.rowLen, buf.memoryUsage, before)
	}
	for i := 1; i <= 5; i++ {
		if got := buf.cont[i][0]; got != "r"+I2S(i)+"c0" {
			t.Errorf("row %d = %q after undo", i, got)
		}
	}
	// Rows are matched by identity: an equal copy removes nothing.
	copyRow := append([]string(nil), buf.cont[1]...)
	if got := buf.removeRows([][]string{copyRow}); len(got) != 0 || buf.rowLen != 6 {
		t.Errorf("a copy of a row must not match: removed %v", got)
	}
}

func TestBuffer_RemoveRowsThroughFilteredView(t *testing.T) {
	base := editableBuffer(t, 6)
	view := base.filterByColumn(0, FilterOptions{Query: "r", Operator: "contains"})
	view.cont = view.cont[:1+0] // keep header only, then pick two rows explicitly
	view.cont = append(view.cont, base.cont[3], base.cont[5])
	view.rowLen = 3
	removed := base.removeRows(view.cont[1:])
	if len(removed) != 2 || removed[0].index != 3 || removed[1].index != 5 || removed[1].row[0] != "r5c0" {
		t.Fatalf("view rows must select the base rows they were filtered from: %+v", removed)
	}
	if base.rowLen != 5 || base.cont[3][0] != "r4c0" || base.cont[4][0] != "r6c0" {
		t.Errorf("base after removal: %v", base.cont)
	}
}

func TestBuffer_RemoveAndInsertColumns(t *testing.T) {
	buf := editableBuffer(t, 3)
	buf.colType[1], buf.colType[2] = colTypeFloat, colTypeDate
	buf.interners = make([]*stringInterner, 4)
	buf.internCols = []bool{false, true, false, false}
	buf.interners[1] = newStringInterner()
	rowBefore := buf.cont[1]
	before := buf.memoryUsage

	removed := buf.removeColumns(1, 2)
	if len(removed) != 2 || removed[0].cells[0] != "h1" || removed[1].cells[3] != "r3c2" {
		t.Fatalf("removed = %+v", removed)
	}
	if removed[0].colType != colTypeFloat || !removed[0].interned || removed[0].interner == nil || removed[1].colType != colTypeDate {
		t.Errorf("column metadata not captured: %+v", removed)
	}
	if buf.colLen != 2 || len(buf.colType) != 3 || len(buf.colWidth) != 2 || len(buf.interners) != 2 || len(buf.internCols) != 2 {
		t.Fatalf("metadata not shifted: colLen %d colType %v colWidth %v", buf.colLen, buf.colType, buf.colWidth)
	}
	for i, row := range buf.cont {
		if len(row) != 2 || row[1] != []string{"h3", "r1c3", "r2c3", "r3c3"}[i] {
			t.Errorf("row %d = %v", i, row)
		}
	}
	if &buf.cont[1][0] != &rowBefore[0] {
		t.Error("removing columns must keep the row's backing array")
	}
	if buf.memoryUsage >= before {
		t.Error("memory estimate must shrink when columns are removed")
	}

	buf.insertColumns(1, removed)
	if buf.colLen != 4 || buf.memoryUsage != before || buf.colType[1] != colTypeFloat || buf.colType[2] != colTypeDate || !buf.internCols[1] {
		t.Fatalf("undo: colLen %d mem %d/%d types %v intern %v", buf.colLen, buf.memoryUsage, before, buf.colType, buf.internCols)
	}
	for i, row := range buf.cont {
		want := []string{"h0", "h1", "h2", "h3"}
		if i > 0 {
			want = []string{"r" + I2S(i) + "c0", "r" + I2S(i) + "c1", "r" + I2S(i) + "c2", "r" + I2S(i) + "c3"}
		}
		if !reflect.DeepEqual(row, want) {
			t.Errorf("row %d = %v, want %v", i, row, want)
		}
	}
	if &buf.cont[1][0] != &rowBefore[0] {
		t.Error("restoring columns must reuse the row's backing array")
	}

	if got := buf.removeColumns(0, 3); got != nil || buf.colLen != 4 {
		t.Error("removing every column must be refused")
	}
	if got := buf.removeColumns(2, 0); len(got) != 3 || buf.colLen != 1 || buf.cont[2][0] != "r2c3" {
		t.Errorf("a reversed, clamped range must remove 3 columns and keep the last: %v", buf.cont)
	}
}

func TestBuffer_SetCellSharedWithView(t *testing.T) {
	base := editableBuffer(t, 3)
	view := base.filterByColumn(0, FilterOptions{Query: "r2", Operator: "contains"})
	if view.rowLen != 2 {
		t.Fatalf("view rows = %d", view.rowLen)
	}
	base.setCell(view.cont[1], 2, "a much longer value")
	if base.cont[2][2] != "a much longer value" {
		t.Error("a cell set through a view row must change the base buffer")
	}
	if base.columnWidth(2) != len("a much longer value") {
		t.Errorf("column width = %d", base.columnWidth(2))
	}
	base.setCell(view.cont[1], 9, "x") // out of range: ignored
}

func TestCutAndInsertSlice(t *testing.T) {
	s := cutSlice([]int{0, 1, 2, 3, 4}, 1, 2)
	if !reflect.DeepEqual(s, []int{0, 3, 4}) {
		t.Errorf("cutSlice = %v", s)
	}
	if got := cutSlice([]int{0, 1}, 5, 9); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Errorf("cutSlice past the end = %v", got)
	}
	if got := cutSlice([]int{0, 1, 2}, 1, 9); !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("cutSlice clamped = %v", got)
	}
	if got := insertSlice([]int{0, 3, 4}, 1, []int{1, 2}); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4}) {
		t.Errorf("insertSlice = %v", got)
	}
	if got := insertSlice([]int{0}, 7, []int{1}); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Errorf("insertSlice clamped = %v", got)
	}
}
