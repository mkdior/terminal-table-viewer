package app

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func stubClipboard(t *testing.T, tools map[string]bool, env map[string]string, goos string, wsl bool) *[]string {
	t.Helper()
	oldLook, oldRun, oldEnv, oldGOOS, oldWSL, oldScreen := clipLookPath, clipRun, clipGetenv, clipGOOS, clipIsWSL, screenRef
	t.Cleanup(func() {
		clipLookPath, clipRun, clipGetenv, clipGOOS, clipIsWSL, screenRef = oldLook, oldRun, oldEnv, oldGOOS, oldWSL, oldScreen
	})
	var ran []string
	clipLookPath = func(name string) (string, error) {
		if tools[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	clipRun = func(_ context.Context, tool clipTool, text string) error {
		ran = append(ran, tool.label+":"+text)
		if tool.name == "broken" {
			return errors.New("broken: boom")
		}
		return nil
	}
	oldOverride, oldOSC := clipboardOverride, clipboardOSC52
	t.Cleanup(func() { clipboardOverride, clipboardOSC52 = oldOverride, oldOSC })
	clipboardOverride, clipboardOSC52 = "", true
	clipGetenv = func(k string) string { return env[k] }
	clipGOOS = goos
	clipIsWSL = func() bool { return wsl }
	screenRef = nil
	return &ran
}

func TestClipboardCommandPreference(t *testing.T) {
	all := map[string]bool{"wl-copy": true, "xclip": true, "xsel": true, "pbcopy": true, "cmd.exe": true, "clip.exe": true, "termux-clipboard-set": true}
	cases := []struct {
		name  string
		tools map[string]bool
		env   map[string]string
		goos  string
		wsl   bool
		want  string // executable name
	}{
		{"windows copies through cmd.exe with a UTF-8 code page", all, nil, "windows", false, "cmd.exe"},
		{"wsl copies through cmd.exe too", all, nil, "linux", true, "cmd.exe"},
		{"wsl without cmd.exe falls back to clip.exe", map[string]bool{"clip.exe": true, "xclip": true}, nil, "linux", true, "clip.exe"},
		{"macOS prefers pbcopy", all, nil, "darwin", false, "pbcopy"},
		{"termux prefers its helper", all, map[string]string{"TERMUX_VERSION": "0.118"}, "android", false, "termux-clipboard-set"},
		{"wayland prefers wl-copy", all, map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "linux", false, "wl-copy"},
		{"x11 prefers xclip", all, map[string]string{"DISPLAY": ":0"}, "linux", false, "xclip"},
		{"x11 without xclip uses xsel", map[string]bool{"xsel": true}, map[string]string{"DISPLAY": ":0"}, "linux", false, "xsel"},
		{"no hints take the first installed", all, nil, "linux", false, "wl-copy"},
	}
	for _, tc := range cases {
		stubClipboard(t, tc.tools, tc.env, tc.goos, tc.wsl)
		tool, ok := clipboardCommand()
		if !ok || tool.name != tc.want {
			t.Errorf("%s: got %q ok=%v, want %q", tc.name, tool.name, ok, tc.want)
		}
	}
	stubClipboard(t, map[string]bool{}, nil, "linux", false)
	if _, ok := clipboardCommand(); ok {
		t.Error("no tools installed must report ok=false")
	}

	stubClipboard(t, map[string]bool{}, nil, "linux", false)
	clipboardOverride = "my-copier --to clipboard"
	tool, ok := clipboardCommand()
	if !ok || tool.name != "my-copier" || len(tool.args) != 2 || tool.args[1] != "clipboard" {
		t.Errorf("config command must replace detection, got %+v ok=%v", tool, ok)
	}
}

// copyNow runs copyToClipboard the way it runs without an event loop, where
// the report arrives before it returns, and hands back what was reported.
func copyNow(t *testing.T, text string) (string, error) {
	t.Helper()
	var channels string
	var reported error
	called := false
	pending, err := copyToClipboard(text, func(c string, e error) { called, channels, reported = true, c, e })
	if err != nil {
		return "", err
	}
	if pending != "" || !called {
		t.Fatalf("without a running application the copy must finish in line: pending=%q reported=%v", pending, called)
	}
	return channels, reported
}

func TestCopyToClipboard(t *testing.T) {
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	channels, err := copyNow(t, "hello")
	if err != nil || channels != "xclip" || len(*ran) != 1 || (*ran)[0] != "xclip:hello" {
		t.Errorf("tool path: channels=%q err=%v ran=%v", channels, err, *ran)
	}

	stubClipboard(t, map[string]bool{}, nil, "linux", false)
	if _, err := copyNow(t, "hello"); err == nil || !strings.Contains(err.Error(), "no clipboard tool") {
		t.Errorf("without a screen or tool the failure must be explained, got %v", err)
	}

	stubClipboard(t, map[string]bool{}, nil, "linux", false)
	screenRef = tcell.NewSimulationScreen("UTF-8")
	channels, err = copyNow(t, "hello")
	if err != nil || !strings.HasPrefix(channels, "OSC 52 only") {
		t.Errorf("OSC 52 alone must succeed but say it is unverified, got %q %v", channels, err)
	}
	clipboardOSC52 = false
	if _, err := copyNow(t, "hello"); err == nil {
		t.Error("with OSC 52 disabled and no tool, copying must fail")
	}

	stubClipboard(t, map[string]bool{}, nil, "linux", false)
	clipboardOverride = "broken"
	if _, err := copyNow(t, "hello"); err == nil || !strings.Contains(err.Error(), "broken: boom") {
		t.Errorf("a failing tool without OSC 52 must be reported, got %v", err)
	}
	screenRef = tcell.NewSimulationScreen("UTF-8")
	if channels, err := copyNow(t, "hello"); err != nil || channels != "OSC 52 only (broken: boom)" {
		t.Errorf("a failing tool next to OSC 52 must leave the escape as the only channel, got %q %v", channels, err)
	}
	clipboardOverride = "xclip"
	if channels, err := copyNow(t, "hello"); err != nil || channels != "xclip + OSC 52" {
		t.Errorf("both channels must be named, got %q %v", channels, err)
	}

	stubClipboard(t, map[string]bool{}, nil, "linux", false)
	if _, err := copyNow(t, strings.Repeat("x", maxYankBytes+1)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("oversized yank must be refused, got %v", err)
	}
}

func TestClipboardToolIsGivenUpOn(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("needs sleep")
	}
	oldTimeout := clipToolTimeout
	t.Cleanup(func() { clipToolTimeout = oldTimeout })
	clipToolTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), clipToolTimeout)
	defer cancel()
	start := time.Now()
	err := runClipboardTool(ctx, clipTool{"slow", "sleep", []string{"5"}}, "x")
	if err == nil || !strings.Contains(err.Error(), "slow: gave up after 100ms") {
		t.Fatalf("a tool that never finishes must be given up on, got %v", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("giving up took %s", took)
	}
}

func TestClipboardToolDoesNotWaitForItsChildren(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	// xclip and wl-copy exit at once but fork a child that keeps the pipes
	// open while it serves the selection; the run must not wait for it.
	start := time.Now()
	err := runClipboardTool(context.Background(), clipTool{"forker", "sh", []string{"-c", "sleep 5 & exit 0"}}, "x")
	if err != nil {
		t.Fatalf("a tool that exited well must count as a success, got %v", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the run waited %s for the forked child", took)
	}
}

func TestTSV(t *testing.T) {
	if got := tsv([][]string{{"only"}}); got != "only" {
		t.Errorf("single cell = %q", got)
	}
	if got := tsv([][]string{{"a", "b"}, {"c", "d"}}); got != "a\tb\nc\td" {
		t.Errorf("block = %q", got)
	}
}

func TestCellBlock(t *testing.T) {
	buf, _ := createNewBufferWithData([][]string{{"h1", "h2", "h3"}, {"a", "b", "c"}, {"d", "e", "f"}}, true)
	got := buf.cellBlock(2, 2, 1, 0) // reversed corners are normalised
	want := [][]string{{"a", "b", "c"}, {"d", "e", "f"}}
	if len(got) != 2 || strings.Join(got[0], ",") != strings.Join(want[0], ",") || strings.Join(got[1], ",") != strings.Join(want[1], ",") {
		t.Errorf("cellBlock = %v, want %v", got, want)
	}
	if got := buf.cellBlock(1, 1, 1, 1); len(got) != 1 || got[0][0] != "b" {
		t.Errorf("single cell block = %v", got)
	}
	if got := buf.cellBlock(0, 5, 0, 9); len(got) != 1 || len(got[0]) != 1 || got[0][0] != "h3" {
		t.Errorf("out-of-range columns must clamp, got %v", got)
	}
}
