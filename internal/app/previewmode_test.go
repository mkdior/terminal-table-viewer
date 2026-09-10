package app

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestKPeeksAtTheCellAndZKTogglesTheBox(t *testing.T) {
	smallUI(t)
	oldShow, oldBefore := previewShow, previewBefore
	t.Cleanup(func() { previewShow, previewBefore = oldShow, oldBefore })
	previewShow, previewBefore = previewCut, previewCut
	b.cont[2][1] = "red; green; blue" // cut by the limit below; the other cells fit
	wrappedColumns[1] = 5
	drawBuffer(b, bufferTable)

	// A short value shows no box on its own; K shows it until the cursor moves.
	bufferTable.Select(1, 0)
	if mainView.text != "" {
		t.Fatalf("a short value shows nothing on its own: %q", mainView.text)
	}
	press(t, "K")
	if mainView.text != "a1" || !mainView.peek || !strings.HasPrefix(statusMessage, "Showing the full value of h1; a move or Esc closes it") {
		t.Errorf("K: text %q peek %v status %q", mainView.text, mainView.peek, statusMessage)
	}
	press(t, "j")
	if mainView.text != "" || mainView.peek {
		t.Errorf("a move closes the peek: text %q peek %v", mainView.text, mainView.peek)
	}
	press(t, "K esc")
	if mainView.text != "" || mainView.peek || statusMessage != "Full value closed" {
		t.Errorf("Esc closes the peek: text %q status %q", mainView.text, statusMessage)
	}
	b.cont[2][0] = ""
	press(t, "K")
	if mainView.text != "" || statusMessage != "Nothing to show: the cell is empty" {
		t.Errorf("K on an empty cell: text %q status %q", mainView.text, statusMessage)
	}

	// zK hides the automatic box and shows it again; a peek works meanwhile.
	bufferTable.Select(2, 1)
	if mainView.text != "red; green; blue" {
		t.Fatalf("the cut value shows on its own: %q", mainView.text)
	}
	press(t, "z K")
	if previewShow != previewOff || mainView.text != "" || !strings.HasPrefix(statusMessage, "Full-value box: hidden (zK toggles, K shows a cell once)") || !strings.HasPrefix(cursorPosStr, "box: hidden  |  ") {
		t.Errorf("zK: mode %v text %q status %q pos %q", previewShow, mainView.text, statusMessage, cursorPosStr)
	}
	press(t, "K")
	if mainView.text != "red; green; blue" {
		t.Errorf("K peeks while the box is off: %q", mainView.text)
	}
	press(t, "z K")
	if previewShow != previewCut || mainView.text != "red; green; blue" || !strings.HasPrefix(statusMessage, "Full-value box: shown for cut values") || strings.HasPrefix(cursorPosStr, "box:") {
		t.Errorf("zK again: mode %v text %q status %q pos %q", previewShow, mainView.text, statusMessage, cursorPosStr)
	}

	// With show = all the box comes back to all, and every value gets a box.
	previewShow, previewBefore = previewAll, previewAll
	press(t, "z K z K")
	if previewShow != previewAll {
		t.Errorf("zK zK from all: %v", previewShow)
	}
	bufferTable.Select(1, 0)
	if mainView.text != "a1" || !strings.HasPrefix(cursorPosStr, "box: every cell  |  ") {
		t.Errorf("all: text %q pos %q", mainView.text, cursorPosStr)
	}
	if got := keys.keysFor(actPeek); got != "K" {
		t.Errorf("peek keys = %q", got)
	}
	if got := keys.keysFor(actTogglePreview); got != "z K" {
		t.Errorf("toggle_preview keys = %q", got)
	}
}

func TestPreviewShowConfig(t *testing.T) {
	resetKeysAndTheme(t)
	oldShow, oldBefore := previewShow, previewBefore
	t.Cleanup(func() { previewShow, previewBefore = oldShow, oldBefore })
	for spelling, want := range map[string]previewMode{"": previewCut, "cut": previewCut, "All": previewAll, " off ": previewOff} {
		var cfg Config
		if _, err := toml.Decode("[preview]\nshow = \""+spelling+"\"\n", &cfg); err != nil {
			t.Fatal(err)
		}
		if err := applyConfig(cfg, ""); err != nil || previewShow != want || previewBefore != want {
			t.Errorf("show = %q: mode %v err %v, want %v", spelling, previewShow, err, want)
		}
	}
	var cfg Config
	if _, err := toml.Decode("[preview]\nshow = \"sometimes\"\n", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cfg, ""); err == nil || !strings.Contains(err.Error(), `unknown preview mode "sometimes"`) {
		t.Errorf("a bad mode is rejected: %v", err)
	}
}
