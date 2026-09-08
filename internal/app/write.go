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

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// pipeSourceName is the file name shown when the table came from stdin.
const pipeSourceName = "From Shell Pipe"

// Load provenance the write path checks: the file as it was when it was
// opened, and whether the load ended early. sourceStat is written by the
// loader goroutine before IsComplete is published, and read after.
var (
	sourceStat  os.FileInfo
	loadStopped bool
)

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
// cache directories may be cleaned out.
func backupDir() (string, error) {
	if backupDirOverride != "" {
		return backupDirOverride, nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "ttv", "backup"), nil
	}
	if os.PathSeparator == '\\' {
		dir, err := os.UserCacheDir() // %LocalAppData%
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "ttv", "backup"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ttv", "backup"), nil
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
// backup is the path of the saved copy, "" when backups are off.
func writeFile(name string, buf *Buffer) (backup string, err error) {
	real, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if err := writable(name, info); err != nil {
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

	if backupEnabled {
		if backup, err = backupOriginal(real); err != nil {
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
	sourceStat, _ = os.Stat(real)
	return backup, nil
}

// writable reports why the file cannot be replaced: it changed since it was
// loaded, it is not a regular file, it is read-only, or other names are hard
// linked to it (replacing it would leave them with the old content).
func writable(name string, info os.FileInfo) error {
	switch {
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", name)
	case sourceStat != nil && !sameSource(sourceStat, info):
		return errors.New("the file changed on disk since it was loaded; reload before writing")
	case info.Mode().Perm()&0o200 == 0:
		return fmt.Errorf("%s is read-only", name)
	case linkCount(info) > 1:
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

// backupName is the prefix shared by the backups of one file: its base name
// and a short hash of its full path, so same-named files in different
// directories do not mix and the name stays short and portable.
func backupName(real string) string {
	sum := sha256.Sum256([]byte(real))
	return filepath.Base(real) + "." + hex.EncodeToString(sum[:4])
}

// backupOriginal copies the file at real into the backup directory as
// <base>.<path hash>.<timestamp>, created exclusively with mode 0600 and
// fsynced together with the directory, then prunes the oldest backups of the
// same file beyond backupKeep. The directory is created private (0700) and
// must be a real directory, not a symlink.
func backupOriginal(real string) (string, error) {
	dir, err := backupDir()
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	if info, err := os.Lstat(dir); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	} else if !info.IsDir() {
		return "", fmt.Errorf("backup: %s is not a directory", dir)
	}
	prefix := backupName(real) + "."
	stamp := time.Now().Format("20060102-150405")
	var out *os.File
	var path string
	for i := 0; ; i++ {
		path = filepath.Join(dir, prefix+stamp)
		if i > 0 {
			path += fmt.Sprintf("-%d", i)
		}
		out, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) || i > 1000 {
			return "", fmt.Errorf("backup: %w", err)
		}
	}
	if err := copyInto(out, real); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("backup: %w", err)
	}
	syncDir(dir)
	pruneBackups(dir, prefix, path)
	return path, nil
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
// backupKeep 0 keeps everything.
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
	sort.Strings(names) // the timestamp orders them
	for _, n := range names[:max(0, len(names)-backupKeep)] {
		if p := filepath.Join(dir, n); p != keep {
			_ = os.Remove(p)
		}
	}
}

// syncDir flushes a directory's entries to disk; best effort, since not every
// filesystem supports it.
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

// requestQuit quits the application, asking first what to do with pending
// edits: write them, discard them, or stay. Ctrl-C arrives here as well.
func requestQuit() {
	if !dirty() {
		app.Stop()
		return
	}
	if UI == nil || UI.HasPage("quitDialog") {
		return
	}
	text := editSummary() + ".\n\n"
	buttons := []string{"Write", "Discard", "Cancel"}
	if reason := writeBlocker(); reason != "" {
		text += "The changes cannot be written: " + reason + ".\n\nQuit and discard them?"
		buttons = []string{"Discard", "Cancel"}
	} else {
		text += "Write the changes to " + filepath.Base(args.FileName) + "?"
	}
	modal := tview.NewModal().SetText(text).AddButtons(buttons)
	modal.SetBackgroundColor(theme.Panel).SetTextColor(theme.Text)
	modal.SetButtonBackgroundColor(theme.Accent).SetButtonTextColor(theme.Background)
	modal.SetBorderColor(theme.Accent)
	modal.SetDoneFunc(func(_ int, label string) {
		UI.RemovePage("quitDialog")
		app.SetFocus(bufferTable)
		switch label {
		case "Write":
			if writeTable() {
				app.Stop()
			}
		case "Discard":
			app.Stop()
		}
	})
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			UI.RemovePage("quitDialog")
			app.SetFocus(bufferTable)
			return nil
		}
		return event
	})
	UI.AddPage("quitDialog", modal, true, true)
	app.SetFocus(modal)
}
