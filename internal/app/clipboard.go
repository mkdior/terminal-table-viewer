package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// maxYankBytes caps what a single yank will send to the clipboard.
const maxYankBytes = 50 << 20

// clipToolTimeout bounds one run of the clipboard tool. A tool that never
// finishes (an interop relay that does not pass EOF on, a display that is
// gone) must not keep a yank open forever; a variable so tests can shorten it.
var clipToolTimeout = 10 * time.Second

// clipWaitDelay is how long to wait for the tool's pipes after it has exited:
// xclip and wl-copy fork a child that serves the selection and keeps stderr
// open, and without a bound the run would last until the clipboard changes
// hands.
const clipWaitDelay = time.Second

// maxOSC52Bytes caps the payload sent with the OSC 52 escape; terminals and
// tmux drop very large sequences, so bigger yanks rely on a clipboard tool.
const maxOSC52Bytes = 1 << 20

// screenRef is the tcell screen the application draws on, captured so the
// clipboard code can emit OSC 52 through it.
var screenRef tcell.Screen

// Hooks for tests.
var (
	clipLookPath = exec.LookPath
	clipRun      = runClipboardTool
	clipGetenv   = os.Getenv
	clipGOOS     = runtime.GOOS
	clipIsWSL    = detectWSL
)

// Clipboard settings from the config file.
var (
	clipboardOverride     string // user-supplied command; empty means auto-detect
	clipboardOSC52        = true // emit the OSC 52 escape
	clipboardCopyOnSelect = true // a block selected with the mouse is copied when the button is released
)

// clipTool is a clipboard command that reads the text from stdin.
type clipTool struct {
	label string // shown in the footer
	name  string // executable looked up on PATH
	args  []string
}

// windowsClip copies through cmd.exe so the console code page can be switched
// to UTF-8 first; plain clip.exe would garble anything outside ASCII.
var windowsClip = clipTool{"clip.exe", "cmd.exe", []string{"/c", "chcp 65001>nul & clip"}}

var (
	toolPbcopy   = clipTool{"pbcopy", "pbcopy", nil}
	toolWlCopy   = clipTool{"wl-copy", "wl-copy", nil}
	toolXclip    = clipTool{"xclip", "xclip", []string{"-selection", "clipboard"}}
	toolXsel     = clipTool{"xsel", "xsel", []string{"--clipboard", "--input"}}
	toolTermux   = clipTool{"termux-clipboard-set", "termux-clipboard-set", nil}
	toolClipExe  = clipTool{"clip.exe", "clip.exe", nil}
	allClipTools = []clipTool{toolWlCopy, toolXclip, toolXsel, toolPbcopy, windowsClip, toolClipExe, toolTermux}
)

// clipboardCommand picks the clipboard tool for the running system:
// Windows and WSL copy into the Windows clipboard, macOS uses pbcopy,
// Wayland wl-copy, X11 xclip or xsel, Termux its clipboard helper, and
// anything else the first of those that is installed. A command from the
// config file replaces the detection.
func clipboardCommand() (clipTool, bool) {
	if clipboardOverride != "" {
		fields := strings.Fields(clipboardOverride)
		return clipTool{label: fields[0], name: fields[0], args: fields[1:]}, true
	}
	var preferred []clipTool
	switch {
	case clipGOOS == "windows", clipIsWSL():
		preferred = []clipTool{windowsClip, toolClipExe}
	case clipGOOS == "darwin":
		preferred = []clipTool{toolPbcopy}
	case clipGetenv("TERMUX_VERSION") != "":
		preferred = []clipTool{toolTermux}
	case clipGetenv("WAYLAND_DISPLAY") != "":
		preferred = []clipTool{toolWlCopy}
	case clipGetenv("DISPLAY") != "":
		preferred = []clipTool{toolXclip, toolXsel}
	}
	for _, tool := range append(preferred, allClipTools...) {
		if _, err := clipLookPath(tool.name); err == nil {
			return tool, true
		}
	}
	return clipTool{}, false
}

// detectWSL reports whether the process runs under Windows Subsystem for Linux.
func detectWSL() bool {
	if clipGetenv("WSL_DISTRO_NAME") != "" || clipGetenv("WSL_INTEROP") != "" {
		return true
	}
	version, err := os.ReadFile("/proc/version")
	return err == nil && bytes.Contains(bytes.ToLower(version), []byte("microsoft"))
}

// runClipboardTool pipes text into the tool's stdin, giving up when ctx ends.
func runClipboardTool(ctx context.Context, tool clipTool, text string) error {
	cmd := exec.CommandContext(ctx, tool.name, tool.args...)
	cmd.Stdin = strings.NewReader(text)
	cmd.WaitDelay = clipWaitDelay
	if strings.HasSuffix(tool.name, ".exe") && clipIsWSL() {
		// Windows programs cannot use a WSL path as their working directory
		// and warn about it; start them from the Windows drive instead.
		if info, err := os.Stat("/mnt/c/"); err == nil && info.IsDir() {
			cmd.Dir = "/mnt/c/"
		}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		// ErrWaitDelay: the tool exited well but a child of it kept a pipe open.
		return nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%s: gave up after %s", tool.label, clipToolTimeout)
	case ctx.Err() != nil:
		return ctx.Err()
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("%s: %s", tool.label, msg)
}

// The tool run of the latest copy: a newer copy cancels it, and a result that
// arrives for a superseded copy is dropped.
var (
	clipCancel     context.CancelFunc
	clipGeneration int
)

// runClipboardToolAsync runs the tool for text and hands the result to done on
// the UI goroutine. With the application running the tool runs in the
// background, so a slow or hung child process never freezes the table, and
// started is true; without one (tests, --debug) it runs in line and done has
// been called on return.
func runClipboardToolAsync(tool clipTool, text string, done func(error)) (started bool) {
	if clipCancel != nil {
		clipCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipToolTimeout)
	clipCancel = cancel
	clipGeneration++
	gen := clipGeneration
	if !uiRunning.Load() {
		err := clipRun(ctx, tool, text)
		cancel()
		done(err)
		return false
	}
	go func() {
		err := clipRun(ctx, tool, text)
		cancel()
		if !uiRunning.Load() {
			return
		}
		app.QueueUpdateDraw(func() {
			if gen == clipGeneration {
				done(err)
			}
		})
	}()
	return true
}

// osc52 is the escape that sets the terminal's clipboard to text.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x1b\\"
}

// screenDCSChunk is the most GNU screen passes through in one DCS.
const screenDCSChunk = 256

// muxPassthrough wraps a terminal escape so the multiplexer mux ("tmux" or
// "screen") hands it to the outer terminal instead of eating it, the way
// Claude Code sends its clipboard writes: tmux takes a DCS whose body is the
// escape with every ESC doubled (with allow-passthrough on), GNU screen takes
// the escape in DCS pieces of at most screenDCSChunk bytes. Any other mux
// gets the escape as it is.
func muxPassthrough(seq, mux string) string {
	switch mux {
	case "tmux":
		return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	case "screen":
		var sb strings.Builder
		for len(seq) > 0 {
			n := min(screenDCSChunk, len(seq))
			sb.WriteString("\x1bP" + seq[:n] + "\x1b\\")
			seq = seq[n:]
		}
		return sb.String()
	}
	return seq
}

// clipMux names the terminal multiplexer ttv runs inside, "" for none.
func clipMux() string {
	switch {
	case clipGetenv("TMUX") != "":
		return "tmux"
	case clipGetenv("STY") != "":
		return "screen"
	}
	return ""
}

// copyToClipboard sends text to the system clipboard through every channel
// that can reach it: the OSC 52 escape, written at once, and a clipboard tool
// when one is installed, run in the background (see runClipboardToolAsync).
// Inside tmux or screen the escape also goes out wrapped for the multiplexer
// to pass on (muxPassthrough), and once more plainly, so it reaches the outer
// terminal whether the multiplexer forwards clipboard writes itself
// (set-clipboard on) or only passes escapes through (allow-passthrough on).
// An error comes back at once when no channel can take the text at all.
// Otherwise the outcome reaches report on the UI goroutine, with the channels
// used or an error when none applied: at once when there is no tool to wait
// for, else when the tool has finished or been given up on, in which case
// pending names the tool still running.
func copyToClipboard(text string, report func(channels string, err error)) (pending string, err error) {
	if len(text) > maxYankBytes {
		return "", fmt.Errorf("selection is %s; the limit is %s", formatBytes(int64(len(text))), formatBytes(maxYankBytes))
	}
	osc := clipboardOSC52 && screenRef != nil && len(text) <= maxOSC52Bytes
	if osc {
		screenRef.SetClipboard([]byte(text))
		if mux := clipMux(); mux != "" {
			if tty, ok := screenRef.Tty(); ok {
				seq := osc52(text)
				_, _ = io.WriteString(tty, seq+muxPassthrough(seq, mux))
			}
		}
	}
	tool, ok := clipboardCommand()
	if !ok {
		const problem = "no clipboard tool found (wl-copy, xclip, xsel, pbcopy, clip.exe or termux-clipboard-set)"
		if !osc {
			return "", errors.New(problem)
		}
		// Unverifiable on its own: the terminal may ignore the escape.
		report("OSC 52 only ("+problem+")", nil)
		return "", nil
	}
	done := func(err error) {
		switch {
		case err == nil && osc:
			report(tool.label+" + OSC 52", nil)
		case err == nil:
			report(tool.label, nil)
		case osc:
			report("OSC 52 only ("+err.Error()+")", nil)
		default:
			report("", err)
		}
	}
	if runClipboardToolAsync(tool, text, done) {
		return tool.label, nil
	}
	return "", nil
}

// tsv joins cells with tabs and rows with newlines; a single cell is returned as is.
func tsv(rows [][]string) string {
	if len(rows) == 1 && len(rows[0]) == 1 {
		return rows[0][0]
	}
	var sb strings.Builder
	for i, row := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.Join(row, "\t"))
	}
	return sb.String()
}

// yankCells copies a rectangular block of b to the register and the clipboard
// and reports the outcome in the footer, with the size of the text in bytes
// and characters. Rows r1..r2 and columns c1..c2 are inclusive. A block of
// empty cells is not copied: there is nothing to put in the clipboard, and
// the register keeps what it has. The yank itself is done at once; while the
// clipboard tool is still running the footer says so, then names the
// channels that took the text.
func yankCells(r1, c1, r2, c2 int) {
	if n := max(r1, r2) - min(r1, r2) + 1; n > maxStreamYankRows && b.streamed() {
		// Every row would be read from disk and held; a yank that size belongs
		// to a shell tool.
		drawFooterText(fileNameStr, fmt.Sprintf("Yank of %d rows refused: a streamed table yanks at most %d rows at a time", n, maxStreamYankRows), cursorPosStr)
		return
	}
	rows := b.cellBlock(r1, c1, r2, c2)
	text := tsv(rows)
	if strings.Trim(text, "\t\n") == "" {
		drawFooterText(fileNameStr, "Nothing to copy", cursorPosStr)
		return
	}
	setRegister(rows)
	what := fmt.Sprintf("%d rows x %d columns", len(rows), c2-c1+1)
	if len(rows) == 1 && c1 == c2 {
		what = "1 cell"
	} else if c1 == 0 && c2 == b.colCount()-1 {
		what = fmt.Sprintf("%d rows", len(rows))
	}
	yanked := fmt.Sprintf("Yanked %s (%s, %d characters)", what, formatBytes(int64(len(text))), utf8.RuneCountInString(text))
	pending, err := copyToClipboard(text, func(channels string, err error) {
		if err != nil {
			drawFooterText(fileNameStr, yanked+"; clipboard failed: "+err.Error(), cursorPosStr)
			return
		}
		drawFooterText(fileNameStr, yanked+" via "+channels, cursorPosStr)
	})
	switch {
	case err != nil:
		drawFooterText(fileNameStr, yanked+"; clipboard failed: "+err.Error(), cursorPosStr)
	case pending != "":
		drawFooterText(fileNameStr, yanked+"; "+pending+" running", cursorPosStr)
	}
}
