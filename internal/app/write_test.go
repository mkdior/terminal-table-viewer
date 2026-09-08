package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivo/tview"
)

// setupWriteTable prepares an editable table backed by a real file in a temp
// directory. It returns the file path.
func setupWriteTable(t *testing.T, content string) string {
	t.Helper()
	setupEditTable(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	oldStat, oldStopped := sourceStat, loadStopped
	oldArgs, oldUI, oldApp := args, UI, app
	t.Cleanup(func() {
		sourceStat, loadStopped = oldStat, oldStopped
		args, UI, app = oldArgs, oldUI, oldApp
	})
	loadStopped = false
	args.setDefault()
	args.FileName = path
	buf := createNewBuffer()
	if err := loadFileToBuffer(path, buf); err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1
	b = buf
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 0)
	UI = tview.NewPages()
	app = tview.NewApplication()
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriteDelimitedQuoting(t *testing.T) {
	buf := createNewBuffer()
	buf.sep = ','
	rows := [][]string{
		{"name", "note", "size"},
		{"a,b", `5" nails`, " lead"},
		{"plain", "", "x\ny"},
	}
	for _, r := range rows {
		if err := buf.contAppendSli(r, true); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := writeDelimited(bufio.NewWriter(&out), buf); err != nil {
		t.Fatal(err)
	}
	want := "name,note,size\n\"a,b\",\"5\"\" nails\", lead\nplain,,\"x\ny\"\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
	// A TSV keeps leading spaces unquoted and quotes only tabs.
	tsvBuf := createNewBuffer()
	tsvBuf.sep = '\t'
	_ = tsvBuf.contAppendSli([]string{" a", "b\tc"}, false)
	out.Reset()
	_ = writeDelimited(bufio.NewWriter(&out), tsvBuf)
	if out.String() != " a\t\"b\tc\"\n" {
		t.Errorf("tsv: %q", out.String())
	}
	// An empty single-column row must not become a blank line.
	one := createNewBuffer()
	one.sep = ','
	_ = one.contAppendSli([]string{"v"}, false)
	_ = one.contAppendSli([]string{""}, false)
	out.Reset()
	_ = writeDelimited(bufio.NewWriter(&out), one)
	if out.String() != "v\n\"\"\n" {
		t.Errorf("single column: %q", out.String())
	}
}

func TestWriteRoundTripsThroughTheLoader(t *testing.T) {
	path := setupWriteTable(t, "name,note\nx,\"a,b\"\ny, lead\n")
	press(t, "j d d") // remove y
	if !dirty() {
		t.Fatal("expected a pending edit")
	}
	press(t, "W")
	if dirty() || !strings.HasPrefix(statusMessage, "Wrote 2 rows x 2 columns to data.csv") {
		t.Fatalf("status %q dirty=%v", statusMessage, dirty())
	}
	if got := readFile(t, path); got != "name,note\nx,\"a,b\"\n" {
		t.Errorf("file = %q", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
	reloaded := createNewBuffer()
	if err := loadFileToBuffer(path, reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.rowLen != 2 || reloaded.cont[1][1] != "a,b" {
		t.Errorf("reloaded %v", reloaded.cont)
	}
	if !strings.Contains(fileNameStr, "data.csv  |") || strings.Contains(fileNameStr, "[+]") {
		t.Errorf("dirty marker must clear after a write: %q", fileNameStr)
	}
	// Writing twice: the second W is refused without new edits.
	press(t, "W")
	if statusMessage != "Not written: no changes to write" {
		t.Errorf("status %q", statusMessage)
	}
	press(t, "d l W") // remove the note column and write again
	if got := readFile(t, path); got != "note\n\"a,b\"\n" {
		t.Errorf("second write: %q", got)
	}
}

func TestWriteRefusals(t *testing.T) {
	path := setupWriteTable(t, "a,b\n1,2\n3,4\n")
	press(t, "W")
	if statusMessage != "Not written: no changes to write" {
		t.Errorf("clean table: %q", statusMessage)
	}
	press(t, "d d")
	original := readFile(t, path)
	cases := []struct {
		name  string
		setup func()
		want  string
	}{
		{"pipe", func() { args.FileName = pipeSourceName }, "the input came from a pipe"},
		{"loading", func() { loadProgress.IsComplete.Store(false) }, "still loading"},
		{"stopped", func() { loadStopped = true }, "the load stopped early"},
		{"lines", func() { args.NLine = 1 }, "--lines"},
		{"skip-lines", func() { args.SkipNum = 1 }, "--skip-lines"},
		{"skip-prefix", func() { args.SkipSymbol = []string{"#"} }, "--skip-prefix"},
		{"columns", func() { args.ShowNum = []int{1} }, "--columns"},
		{"padded", func() { b.padded = true }, "padded with NaN"},
	}
	for _, tc := range cases {
		saved, savedName, savedStopped := args, args.FileName, loadStopped
		tc.setup()
		press(t, "W")
		if !strings.HasPrefix(statusMessage, "Not written: ") || !strings.Contains(statusMessage, tc.want) {
			t.Errorf("%s: status %q", tc.name, statusMessage)
		}
		args, args.FileName, loadStopped = saved, savedName, savedStopped
		loadProgress.IsComplete.Store(true)
		b.padded = false
	}
	if readFile(t, path) != original {
		t.Error("a refused write must not touch the file")
	}

	// The file changed on disk since it was loaded.
	if err := os.WriteFile(path, []byte("a,b\n9,9\n8,8\n7,7\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	press(t, "W")
	if !strings.Contains(statusMessage, "changed on disk") || dirty() != true {
		t.Errorf("changed file: %q", statusMessage)
	}
	if readFile(t, path) != "a,b\n9,9\n8,8\n7,7\n" {
		t.Error("the changed file must be left alone")
	}

	// Read-only files are refused too.
	path = setupWriteTable(t, "a,b\n1,2\n3,4\n")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	press(t, "d d W")
	if !strings.Contains(statusMessage, "read-only") {
		t.Errorf("read-only: %q", statusMessage)
	}
}

func TestWriteGzipAndSymlink(t *testing.T) {
	setupEditTable(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "real.dat")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("a,b\n1,2\n3,4\n"))
	_ = zw.Close()
	if err := os.WriteFile(target, gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "data.csv.gz")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	oldStat, oldName := sourceStat, args.FileName
	t.Cleanup(func() { sourceStat, args.FileName = oldStat, oldName })
	args.FileName = link
	buf := createNewBuffer()
	if err := loadFileToBuffer(link, buf); err != nil {
		t.Fatal(err)
	}
	buf.rowFreeze = 1
	b = buf
	drawBuffer(b, bufferTable)
	bufferTable.Select(1, 0)

	press(t, "d d W")
	if !strings.HasPrefix(statusMessage, "Wrote 2 rows") {
		t.Fatalf("status %q", statusMessage)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink must survive; the target is replaced")
	}
	f, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("the written target must stay gzip (the loaded name ends in .gz): %v", err)
	}
	data, _ := io.ReadAll(zr)
	if string(data) != "a,b\n3,4\n" {
		t.Errorf("content %q", data)
	}
}

func TestQuitPromptsWhenDirty(t *testing.T) {
	setupWriteTable(t, "a,b\n1,2\n3,4\n")
	press(t, "q")
	if UI.HasPage("quitDialog") {
		t.Error("a clean table must quit without a prompt")
	}
	press(t, "d d q")
	if !UI.HasPage("quitDialog") {
		t.Fatal("pending edits must open the quit dialog")
	}
	press(t, "q") // a second request while the dialog is up is ignored
	if !UI.HasPage("quitDialog") {
		t.Error("the dialog must stay")
	}
	UI.RemovePage("quitDialog")
	// When the write is blocked, the dialog only offers to discard.
	args.FileName = pipeSourceName
	requestQuit()
	if !UI.HasPage("quitDialog") {
		t.Error("blocked write still prompts before discarding")
	}
}
