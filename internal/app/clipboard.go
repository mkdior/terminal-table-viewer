package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// maxYankBytes caps what a single yank will send to the clipboard.
const maxYankBytes = 50 << 20

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
	clipboardOverride string // user-supplied command; empty means auto-detect
	clipboardOSC52    = true // emit the OSC 52 escape
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

// runClipboardTool pipes text into the tool's stdin.
func runClipboardTool(tool clipTool, text string) error {
	cmd := exec.Command(tool.name, tool.args...)
	cmd.Stdin = strings.NewReader(text)
	if strings.HasSuffix(tool.name, ".exe") && clipIsWSL() {
		// Windows programs cannot use a WSL path as their working directory
		// and warn about it; start them from the Windows drive instead.
		if info, err := os.Stat("/mnt/c/"); err == nil && info.IsDir() {
			cmd.Dir = "/mnt/c/"
		}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", tool.label, msg)
	}
	return nil
}

// copyToClipboard sends text to the system clipboard through every channel
// that can reach it: the OSC 52 escape (which tmux forwards when
// set-clipboard is on) and a clipboard tool when one is installed. It returns
// a short description of the channels used, or an error when none applied.
func copyToClipboard(text string) (string, error) {
	if len(text) > maxYankBytes {
		return "", fmt.Errorf("selection is %s; the limit is %s", formatBytes(int64(len(text))), formatBytes(maxYankBytes))
	}
	var channels []string
	var problems []string

	if tool, ok := clipboardCommand(); ok {
		if err := clipRun(tool, text); err != nil {
			problems = append(problems, err.Error())
		} else {
			channels = append(channels, tool.label)
		}
	} else {
		problems = append(problems, "no clipboard tool found (wl-copy, xclip, xsel, pbcopy, clip.exe or termux-clipboard-set)")
	}
	if clipboardOSC52 && screenRef != nil && len(text) <= maxOSC52Bytes {
		screenRef.SetClipboard([]byte(text))
		if len(channels) == 0 {
			// Unverifiable on its own: the terminal may ignore the escape.
			return "OSC 52 only (" + strings.Join(problems, "; ") + ")", nil
		}
		channels = append(channels, "OSC 52")
	}

	if len(channels) == 0 {
		return "", errors.New(strings.Join(problems, "; "))
	}
	return strings.Join(channels, " + "), nil
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

// yankCells copies a rectangular block of b to the clipboard and reports the
// outcome in the footer. Rows r1..r2 and columns c1..c2 are inclusive.
func yankCells(r1, c1, r2, c2 int) {
	rows := b.cellBlock(r1, c1, r2, c2)
	setRegister(rows)
	text := tsv(rows)
	what := fmt.Sprintf("%d rows x %d columns", len(rows), c2-c1+1)
	if len(rows) == 1 && c1 == c2 {
		what = "1 cell"
	} else if c1 == 0 && c2 == b.colLen-1 {
		what = fmt.Sprintf("%d rows", len(rows))
	}
	channels, err := copyToClipboard(text)
	if err != nil {
		drawFooterText(fileNameStr, "Clipboard failed: "+err.Error(), cursorPosStr)
		return
	}
	drawFooterText(fileNameStr, fmt.Sprintf("Yanked %s (%s) via %s", what, formatBytes(int64(len(text))), channels), cursorPosStr)
}
