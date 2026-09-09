package app

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// pipeSourceName is the file name shown when the table came from stdin.
const pipeSourceName = "From Shell Pipe"

// loadStopped records that the load of the current table ended early; the
// write path refuses an incomplete table. The file as it was when loaded
// lives in the buffer itself (Buffer.source).
var loadStopped bool

// Backup settings from the [backup] section of the config file.
var (
	backupEnabled     = true
	backupDirOverride string
	backupKeep        = defaultBackupKeep
)

// defaultBackupKeep is how many backups of one file are kept by default.
const defaultBackupKeep = 20

// backupDir is where previous versions of written files are kept: the
// configured directory, else $XDG_STATE_HOME/ttv/backup (~/.local/state on
// Unix, the local application data directory on Windows). State, not cache:
// cache directories may be cleaned out. isDefault reports that the directory
// is ours to keep private.
func backupDir() (dir string, isDefault bool, err error) {
	if backupDirOverride != "" {
		return backupDirOverride, false, nil
	}
	// The XDG specification says a relative value is invalid and to be ignored.
	if state := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(state) {
		return filepath.Join(state, "ttv", "backup"), true, nil
	}
	if os.PathSeparator == '\\' {
		local, err := os.UserCacheDir() // %LocalAppData%
		if err != nil {
			return "", false, err
		}
		return filepath.Join(local, "ttv", "backup"), true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Join(home, ".local", "state", "ttv", "backup"), true, nil
}

// writeBlocker explains why the table cannot be written back to the file, or
// returns "" when it can. The table must be the whole file: flags that skip
// lines or hide columns, an incomplete load and NaN padding all disqualify it.
func writeBlocker() string {
	switch {
	case args.FileName == pipeSourceName:
		return "the input came from a pipe; there is no file to write"
	case loading():
		return "still loading"
	case loadStopped:
		return "the load stopped early, so the table is incomplete"
	case args.NLine > 0:
		return "--lines shows only part of the file"
	case args.SkipNum > 0:
		return "--skip-lines left out lines of the file"
	case len(args.SkipSymbol) > 0:
		return "--skip-prefix left out lines of the file"
	case len(args.ShowNum) > 0 || len(args.HideNum) > 0:
		return "--columns and --hide-columns leave out columns of the file"
	case baseBuffer().wasPadded():
		return "ragged rows were padded with NaN on load and would be written that way (use --strict to reject them)"
	case !dirty():
		return "no changes to write"
	}
	return ""
}

// writeTable writes the unfiltered table with its edits back to the file and
// clears the pending edits. It reports success so the quit flow can continue.
func writeTable() bool {
	if reason := writeBlocker(); reason != "" {
		drawFooterText(fileNameStr, "Not written: "+reason, cursorPosStr)
		return false
	}
	base := baseBuffer()
	drawFooterText(fileNameStr, "Writing...", cursorPosStr)
	if app != nil {
		app.ForceDraw()
	}
	backup, err := writeFile(args.FileName, base)
	if err != nil {
		drawFooterText(fileNameStr, "Write failed: "+err.Error(), cursorPosStr)
		return false
	}
	edits = nil
	note := ""
	if backup != "" {
		note = "; previous version in " + tildePath(backup)
	}
	editStatus(fmt.Sprintf("Wrote %d rows x %d columns to %s%s", base.rowLen, base.colLen, filepath.Base(args.FileName), note))
	return true
}

// writeFile replaces the file at name with the rows of buf. The rows go to a
// temporary file next to the real file (symlinks followed) with its
// permissions; a copy of the original is made durable in the backup
// directory; then, after checking once more that the file is still the one
// that was loaded, the temporary file is renamed over it. The original path
// is valid throughout: nothing is moved away before the one atomic rename.
// Older backups are pruned only once the new content is committed. backup is
// the path of the saved copy, "" when backups are off.
func writeFile(name string, buf *Buffer) (backup string, err error) {
	real, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if err := writable(name, real, info, buf.sourceInfo()); err != nil {
		return "", err
	}

	dir := filepath.Dir(real)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(real)+".ttv-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	// The loader decides gzip by the given name's suffix; write the same way.
	if err := writeRows(tmp, buf, strings.HasSuffix(name, ".gz")); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		return "", err
	}

	var prune func()
	if backupEnabled {
		if backup, prune, err = backupOriginal(real); err != nil {
			return "", err
		}
	}
	// Last look before the point of no return: the file must still be the one
	// that was loaded and backed up.
	if again, err := os.Stat(real); err != nil {
		return "", err
	} else if !sameSource(info, again) {
		return "", errors.New("the file changed on disk while it was being written; reload before writing")
	}
	if err := os.Rename(tmpName, real); err != nil {
		return "", err
	}
	committed = true
	syncDir(dir)
	written, _ := os.Stat(real)
	buf.setSource(written)
	if prune != nil {
		prune()
	}
	return backup, nil
}

// writable reports why the file cannot be replaced: it changed since it was
// loaded (source is the file as loaded, nil when unknown), it is not a
// regular file, it is read-only, or other names are hard linked to it
// (replacing it would leave them with the old content).
func writable(name, real string, info, source os.FileInfo) error {
	switch {
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", name)
	case source != nil && !sameSource(source, info):
		return errors.New("the file changed on disk since it was loaded; reload before writing")
	case info.Mode().Perm()&0o200 == 0:
		return fmt.Errorf("%s is read-only", name)
	case linkCount(real, info) > 1:
		return fmt.Errorf("%s has other hard links, which would keep the old content", name)
	}
	return nil
}

// sameSource reports whether two stats describe the same unchanged file:
// same identity (device and inode where the platform has them), size and
// modification time.
func sameSource(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// writeRows writes buf to w, gzip-compressed when gz is set, and reports the
// first error, including those surfaced when the compressor is closed.
func writeRows(w io.Writer, buf *Buffer, gz bool) error {
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(w)
		w = zw
	}
	bw := bufio.NewWriterSize(w, 1<<16)
	if err := writeDelimited(bw, buf); err != nil {
		return err
	}
	if zw != nil {
		return zw.Close()
	}
	return nil
}

// writeDelimited writes the rows of buf joined by its separator, one row per
// line with LF endings. A field is quoted only when it contains the
// separator, a quote, CR or LF (RFC 4180); unlike encoding/csv a field with a
// leading space is left alone, so TSV and pipe files keep their look. An
// empty single-column row is written as "" so the loader does not skip it as
// a blank line.
func writeDelimited(w *bufio.Writer, buf *Buffer) error {
	buf.mu.RLock()
	defer buf.mu.RUnlock()
	sep := string(buf.sep)
	for _, row := range buf.cont {
		for i, cell := range row {
			if i > 0 {
				_, _ = w.WriteString(sep)
			}
			switch {
			case strings.Contains(cell, sep) || strings.ContainsAny(cell, "\"\r\n"):
				_ = w.WriteByte('"')
				_, _ = w.WriteString(strings.ReplaceAll(cell, `"`, `""`))
				_ = w.WriteByte('"')
			case cell == "" && len(row) == 1:
				_, _ = w.WriteString(`""`)
			default:
				_, _ = w.WriteString(cell)
			}
		}
		_ = w.WriteByte('\n')
	}
	return w.Flush()
}

// maxBackupBase caps the readable part of a backup name so the suffixes still
// fit a 255-byte file name.
const maxBackupBase = 200

// backupName is the prefix shared by the backups of one file: its base name
// (cut to maxBackupBase bytes) and a 64-bit hash of its absolute path, so
// same-named files in different directories do not mix and the name stays
// portable.
func backupName(real string) string {
	base := filepath.Base(real)
	if len(base) > maxBackupBase {
		base = base[:maxBackupBase]
	}
	sum := sha256.Sum256([]byte(real))
	return base + "." + hex.EncodeToString(sum[:8])
}

// backupTempPrefix names the in-progress copies; it never matches a backup
// prefix, so pruning and recovery ignore them.
const backupTempPrefix = ".ttv-tmp-"

// backupOriginal copies the file at real into the backup directory as
// <base>.<path hash>.<timestamp>: the bytes go to a temporary file first
// (0600, fsynced), which is then linked to a final name that did not exist
// (a counter is appended on a same-second collision), so a crash leaves only a
// temporary file and no half-written backup under a final name. The directory
// is created private (0700), kept so when it is ours, and must be a real
// directory. The returned prune function removes the oldest backups of the
// same file beyond backupKeep; the caller runs it after committing the write.
func backupOriginal(real string) (path string, prune func(), err error) {
	dir, isDefault, err := backupDir()
	if err != nil {
		return "", nil, fmt.Errorf("backup: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("backup: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", nil, fmt.Errorf("backup: %w", err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("backup: %s is not a directory", dir)
	}
	if isDefault && info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", nil, fmt.Errorf("backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, backupTempPrefix+"*")
	if err != nil {
		return "", nil, fmt.Errorf("backup: %w", err)
	}
	tmpName := tmp.Name()
	if err := copyInto(tmp, real); err != nil {
		_ = os.Remove(tmpName)
		return "", nil, fmt.Errorf("backup: %w", err)
	}
	prefix := backupName(real) + "."
	stamp := time.Now().Format("20060102-150405")
	for i := 0; ; i++ {
		path = filepath.Join(dir, prefix+stamp)
		if i > 0 {
			path += fmt.Sprintf("-%03d", i)
		}
		err = linkOrRename(tmpName, path)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) || i >= 999 {
			_ = os.Remove(tmpName)
			return "", nil, fmt.Errorf("backup: %w", err)
		}
	}
	_ = os.Remove(tmpName)
	syncDir(dir)
	return path, func() { pruneBackups(dir, prefix, path) }, nil
}

// linkOrRename gives tmp the name final without replacing an existing file:
// a hard link fails with ErrExist when the name is taken; on filesystems
// without links it falls back to a rename after checking the name is free.
func linkOrRename(tmp, final string) error {
	err := os.Link(tmp, final)
	if err == nil || errors.Is(err, os.ErrExist) {
		return err
	}
	if _, statErr := os.Lstat(final); statErr == nil {
		return os.ErrExist
	}
	return os.Rename(tmp, final)
}

// copyInto copies the file at src into out, fsyncs and closes it.
func copyInto(out *os.File, src string) error {
	in, err := os.Open(src)
	if err != nil {
		_ = out.Close()
		return err
	}
	defer func() { _ = in.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// pruneBackups removes the oldest backups sharing prefix so that at most
// backupKeep remain; keep is the backup just written and is never removed.
// backupKeep 0 keeps everything. Names sort chronologically: a fixed-width
// timestamp followed by a zero-padded collision counter.
func pruneBackups(dir, prefix, keep string) {
	if backupKeep <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names[:max(0, len(names)-backupKeep)] {
		if p := filepath.Join(dir, n); p != keep {
			_ = os.Remove(p)
		}
	}
}

// syncDir flushes a directory's entries to disk. Best effort: directory fsync
// is not supported everywhere (9p under WSL returns an error), and a write
// that succeeded must not be reported as failed because of it.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// tildePath shortens a path under the home directory to ~/... for the footer.
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}

// expandPath makes a config path absolute, expanding a leading ~.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}

// handleAppKey is the application-level input capture. Ctrl-C would stop the
// application behind the keymap's back; it goes through the quit flow instead
// so pending edits get their write/discard prompt, in every open tab, and in
// the cell editor it acts as Escape, as it does in vim's insert mode.
func handleAppKey(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() != tcell.KeyCtrlC {
		return event
	}
	if cellEdit != nil {
		cellEdit.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		return nil
	}
	requestQuitAll()
	return nil
}

// stopApp ends the application; a variable so tests can observe quitting.
var stopApp = func() { app.Stop() }

// requestQuit is q: it closes the tab in front, asking first what to do with
// its pending edits: write them, discard them, or stay. Closing the last tab
// quits; Ctrl-C with one tab open arrives here as well.
func requestQuit() {
	if len(tabs) > 1 {
		confirmChanges(" before closing its tab", "Close the tab and discard them?", closeCurrentTab)
		return
	}
	confirmChanges("", "Quit and discard them?", stopApp)
}

// confirmChanges runs leave at once when the table in front has no pending
// edits, and otherwise asks first: write them (when they can be written) and
// leave, discard them and leave, or stay. when completes the question "Write
// the changes to <file>...?", and discardQuestion is asked instead when the
// changes cannot be written. Staying puts the focus back where it was, so a
// dialog that was open keeps working.
func confirmChanges(when, discardQuestion string, leave func()) {
	if !dirty() {
		leave()
		return
	}
	if UI == nil || UI.HasPage("quitDialog") {
		return
	}
	cancelOperator()
	text := editSummary() + ".\n\n"
	buttons := []string{"Write", "Discard", "Cancel"}
	if reason := writeBlocker(); reason != "" {
		text += "The changes cannot be written: " + reason + ".\n\n" + discardQuestion + "  (d discards, c or Esc stays)"
		buttons = []string{"Discard", "Cancel"}
	} else {
		text += "Write the changes to " + filepath.Base(args.FileName) + when + "?  (w writes, d discards, c or Esc stays)"
	}
	openQuitDialog(text, buttons, func(label string) {
		switch label {
		case "Write":
			if writeTable() {
				leave()
			}
		case "Discard":
			leave()
		}
	})
}

// openQuitDialog shows a write/discard/stay modal with text and buttons over
// the page in front. The first letter of a button presses it, Esc stays, and
// a choice dismisses the dialog, putting the focus back where it was, before
// choose runs with the button's label.
func openQuitDialog(text string, buttons []string, choose func(label string)) {
	previous := app.GetFocus()
	if previous == nil {
		previous = bufferTable
	}
	modal := tview.NewModal().SetText(text).AddButtons(buttons)
	modal.SetBackgroundColor(theme.Panel).SetTextColor(theme.Text)
	// The focused button is the bright one, as in the other dialogs; the
	// others sit on the panel colour so the choice about to be made is plain.
	modal.SetButtonBackgroundColor(theme.Background).SetButtonTextColor(theme.Text)
	modal.SetButtonActivatedStyle(theme.selectedStyle())
	modal.SetBorderColor(theme.Accent)
	dismiss := func() {
		UI.RemovePage("quitDialog")
		app.SetFocus(previous)
	}
	pick := func(label string) {
		dismiss()
		choose(label)
	}
	modal.SetDoneFunc(func(_ int, label string) { pick(label) })
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			dismiss()
			return nil
		}
		if event.Key() == tcell.KeyRune {
			for _, label := range buttons {
				if event.Rune() == unicode.ToLower([]rune(label)[0]) {
					pick(label)
					return nil
				}
			}
		}
		return event
	})
	UI.AddPage("quitDialog", modal, true, true)
	app.SetFocus(modal)
}
