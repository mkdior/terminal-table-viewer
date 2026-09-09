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
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// setupWriteTable prepares an editable table backed by a real file in a temp
// directory, with backups going to a temp directory as well. It returns the
// file path.
func setupWriteTable(t *testing.T, content string) string {
	t.Helper()
	setupEditTable(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	oldEnabled, oldDir, oldKeep, oldStopped := backupEnabled, backupDirOverride, backupKeep, loadStopped
	oldArgs, oldUI, oldApp := args, UI, app
	t.Cleanup(func() {
		backupEnabled, backupDirOverride, backupKeep, loadStopped = oldEnabled, oldDir, oldKeep, oldStopped
		args, UI, app = oldArgs, oldUI, oldApp
	})
	backupEnabled, backupDirOverride, backupKeep, loadStopped = true, filepath.Join(dir, "backups"), defaultBackupKeep, false
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

func TestWriteKeepsABackupCopy(t *testing.T) {
	path := setupWriteTable(t, "a,b\n1,2\n3,4\n")
	press(t, "d d W")
	entries, err := os.ReadDir(backupDirOverride)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups: %v %v", entries, err)
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "data.csv.") || !strings.Contains(name, "."+time.Now().Format("20060102")) {
		t.Errorf("backup name = %q", name)
	}
	backup := filepath.Join(backupDirOverride, name)
	if got := readFile(t, backup); got != "a,b\n1,2\n3,4\n" {
		t.Errorf("backup content = %q", got)
	}
	if info, _ := os.Stat(backup); info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(backupDirOverride); info.Mode().Perm() != 0o700 {
		t.Errorf("backup dir mode = %v, want 0700", info.Mode().Perm())
	}
	if !strings.Contains(statusMessage, "previous version in ") || !strings.Contains(statusMessage, name) {
		t.Errorf("status must name the backup: %q", statusMessage)
	}

	// Two writes in the same second get distinct names; pruning keeps the newest.
	backupKeep = 2
	press(t, "l d l W")
	press(t, "u") // nothing pending? undo stack was cleared by the write
	if statusMessage != "Already at oldest change" {
		t.Errorf("the undo history is cleared by a write, got %q", statusMessage)
	}
	edits = append(edits, edit{cells: []cellChange{{b.cont[1], 0, "1"}}}) // fake a pending edit
	press(t, "W")
	entries, _ = os.ReadDir(backupDirOverride)
	if len(entries) != 2 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("keep=2 must leave two backups, got %v", names)
	}

	// Disabled: no backup, and the write still happens.
	backupEnabled = false
	edits = append(edits, edit{cells: []cellChange{{b.cont[1], 0, "1"}}})
	press(t, "W")
	if entries, _ = os.ReadDir(backupDirOverride); len(entries) != 2 || strings.Contains(statusMessage, "previous version") {
		t.Errorf("no backup expected when disabled: %d entries, %q", len(entries), statusMessage)
	}
	_ = path
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
		{"loading", func() { b.progress.IsComplete.Store(false) }, "still loading"},
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
		b.progress.IsComplete.Store(true)
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
	if entries, _ := os.ReadDir(backupDirOverride); len(entries) != 0 {
		t.Error("no backup may be made for a refused write")
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
	oldEnabled, oldDir, oldName := backupEnabled, backupDirOverride, args.FileName
	t.Cleanup(func() {
		backupEnabled, backupDirOverride, args.FileName = oldEnabled, oldDir, oldName
	})
	backupEnabled, backupDirOverride = true, filepath.Join(dir, "backups")
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
	entries, _ := os.ReadDir(backupDirOverride)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "real.dat.") {
		t.Errorf("the backup is named after the real file: %v", entries)
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

func TestBackupNameAndPaths(t *testing.T) {
	a, b2 := backupName("/x/data.csv"), backupName("/y/data.csv")
	if !strings.HasPrefix(a, "data.csv.") || a == b2 || len(a) != len("data.csv.")+16 {
		t.Errorf("backupName: %q %q", a, b2)
	}
	long := strings.Repeat("n", 240) + ".csv"
	if got := backupName("/x/" + long); len(got) != maxBackupBase+1+16 {
		t.Errorf("a long base name must be cut: %d", len(got))
	}
	if strings.HasPrefix(backupTempPrefix, "data.csv") {
		t.Error("temp names must never look like backups")
	}
	home, _ := os.UserHomeDir()
	if got := tildePath(filepath.Join(home, "x", "y")); got != filepath.Join("~", "x", "y") {
		t.Errorf("tildePath = %q", got)
	}
	if got := tildePath("/elsewhere/z"); got != "/elsewhere/z" {
		t.Errorf("tildePath outside home = %q", got)
	}
	if got, err := expandPath("~/b"); err != nil || got != filepath.Join(home, "b") {
		t.Errorf("expandPath(~/b) = %q %v", got, err)
	}
	if got, err := expandPath("rel"); err != nil || !filepath.IsAbs(got) {
		t.Errorf("expandPath(rel) = %q %v", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "/tmp/state")
	backupDirOverride = ""
	if dir, isDefault, _ := backupDir(); dir != filepath.Join("/tmp/state", "ttv", "backup") || !isDefault {
		t.Errorf("backupDir = %q default=%v", dir, isDefault)
	}
	t.Setenv("XDG_STATE_HOME", "relative/state") // invalid per the XDG spec: ignored
	if dir, _, _ := backupDir(); strings.Contains(dir, "relative") {
		t.Errorf("a relative XDG_STATE_HOME must be ignored, got %q", dir)
	}
	backupDirOverride = "/custom/dir"
	if dir, isDefault, _ := backupDir(); dir != "/custom/dir" || isDefault {
		t.Errorf("configured dir = %q default=%v", dir, isDefault)
	}
}

func TestBackupLeavesTempFilesAloneAndPrunesAfterCommit(t *testing.T) {
	setupWriteTable(t, "a,b\n1,2\n3,4\n")
	backupKeep = 1
	if err := os.MkdirAll(backupDirOverride, 0o700); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(backupDirOverride, backupTempPrefix+"leftover")
	if err := os.WriteFile(stray, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	press(t, "d d W")
	press(t, "l d l W")
	entries, _ := os.ReadDir(backupDirOverride)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || !strings.Contains(strings.Join(names, " "), backupTempPrefix+"leftover") {
		t.Errorf("keep=1 leaves the newest backup and never touches temp files: %v", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "data.csv.") && !strings.Contains(n, time.Now().Format("20060102")) {
			t.Errorf("backup name %q", n)
		}
	}
	// The directory is private even when it existed with looser permissions.
	if err := os.Chmod(backupDirOverride, 0o755); err != nil {
		t.Fatal(err)
	}
	backupDirOverride = "" // default location: the leaf is ours to tighten
	t.Setenv("XDG_STATE_HOME", filepath.Dir(filepath.Dir(filepath.Dir(stray))))
	dir, _, _ := backupDir()
	_ = os.MkdirAll(dir, 0o755)
	edits = append(edits, edit{cells: []cellChange{{b.cont[1], 0, "1"}}})
	press(t, "W")
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the default backup dir must be made private: %v %v", info, err)
	}
}

// pressModal sends keys to the quit dialog the way a user does: Tab moves
// between the buttons (Write, Discard, Cancel from the left), Enter presses.
func pressModal(t *testing.T, keys ...tcell.Key) {
	t.Helper()
	_, front := UI.GetFrontPage()
	modal, ok := front.(*tview.Modal)
	if !ok {
		t.Fatalf("front page is %T, not a modal", front)
	}
	modal.Focus(func(tview.Primitive) {})
	for _, k := range keys {
		modal.InputHandler()(tcell.NewEventKey(k, 0, tcell.ModNone), func(p tview.Primitive) { p.Focus(func(tview.Primitive) {}) })
	}
}

func TestQuitDialogButtons(t *testing.T) {
	path := setupWriteTable(t, "a,b\n1,2\n3,4\n")
	stops := 0
	oldStop := stopApp
	stopApp = func() { stops++ }
	t.Cleanup(func() { stopApp = oldStop })

	press(t, "d d q")
	if !UI.HasPage("quitDialog") {
		t.Fatal("dialog expected")
	}
	pressModal(t, tcell.KeyTab, tcell.KeyTab, tcell.KeyEnter) // third button: Cancel
	if UI.HasPage("quitDialog") || stops != 0 || !dirty() {
		t.Errorf("Cancel must close the dialog and stay: page=%v stops=%d dirty=%v", UI.HasPage("quitDialog"), stops, dirty())
	}
	if app.GetFocus() != bufferTable {
		t.Errorf("focus back on the table, got %T", app.GetFocus())
	}

	press(t, "q")
	pressModal(t, tcell.KeyTab, tcell.KeyEnter) // second button: Discard
	if UI.HasPage("quitDialog") || stops != 1 || readFile(t, path) != "a,b\n1,2\n3,4\n" {
		t.Errorf("Discard must quit without writing: stops=%d file %q", stops, readFile(t, path))
	}

	press(t, "q")
	pressModal(t, tcell.KeyEnter) // first button: Write
	if stops != 2 || dirty() || readFile(t, path) != "a,b\n3,4\n" {
		t.Errorf("Write must write and quit: stops=%d dirty=%v file %q", stops, dirty(), readFile(t, path))
	}

	press(t, "q") // clean table: quits at once
	if stops != 3 {
		t.Errorf("q on a clean table quits: stops=%d", stops)
	}
}
