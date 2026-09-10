package app

import (
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestNoticesFadeToTheIdleText(t *testing.T) {
	setupEditTable(t)
	stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	for _, text := range []string{"-- VISUAL --  2 rows x 2 columns", "-- INSERT --  Esc for normal mode", "Loading... [###] 40%", "Indexing... [#] 3%", "Filtering... 37%  |  Esc cancels", "Sorting..."} {
		if !persistentStatus(text) {
			t.Errorf("%q must not fade", text)
		}
	}
	for _, text := range []string{"Yanked 1 cell (2 B, 2 characters) via xclip", "Removed 1 row; copied via xclip  |  pending: 1 row removed  |  W write, u undo", "Filtered: 11 rows match (1 filters active, r to reset)", "No matches found"} {
		if persistentStatus(text) {
			t.Errorf("%q is a notice and fades", text)
		}
	}

	// The idle text: All Done on a clean table, the pending edits otherwise,
	// the selection's size while one stands.
	if idleStatus() != "All Done" {
		t.Errorf("idle = %q", idleStatus())
	}
	press(t, "y")
	notice := statusMessage
	if !strings.HasPrefix(notice, "Yanked 1 cell") {
		t.Fatalf("status %q", notice)
	}
	expireNotice(nil, "some other notice")
	if statusMessage != notice {
		t.Error("a notice that is no longer shown is left alone")
	}
	expireNotice(nil, notice)
	if statusMessage != "All Done" {
		t.Errorf("after the fade: %q", statusMessage)
	}
	press(t, "d d")
	notice = statusMessage
	expireNotice(nil, notice)
	if statusMessage != "pending: 1 row removed  |  W write, u undo" {
		t.Errorf("with edits pending the footer settles on them: %q", statusMessage)
	}
	press(t, "u V j") // a selection stands; a mouse release copies over it and leaves it
	drawFooterText(fileNameStr, "Yanked 2 rows (23 B, 23 characters) via xclip", cursorPosStr)
	expireNotice(nil, statusMessage)
	if statusMessage != visualStatus() || !strings.Contains(statusMessage, "-- VISUAL LINE --") {
		t.Errorf("a notice over a standing selection fades to its size: %q", statusMessage)
	}
	press(t, "esc")

	// The timer only runs in a running application, and never for the idle
	// text, a mode or a progress text.
	oldTTL := noticeTTL
	t.Cleanup(func() { noticeTTL = oldTTL })
	noticeTTL = time.Millisecond
	armNotice("Yanked 1 cell") // no application: nothing happens, and nothing panics
	noticeTTL = 0
	drawFooterText(fileNameStr, "Yanked 1 cell", cursorPosStr)
	if statusMessage != "Yanked 1 cell" {
		t.Errorf("notice_seconds 0 keeps the notice: %q", statusMessage)
	}
}

func TestNoticesFadeInTheTabTheyBelongTo(t *testing.T) {
	setupTabs(t, csvRows("A", 2), csvRows("B", 2))
	stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	press(t, "y")
	notice := statusMessage
	first := tabs[0]
	press(t, "g t")
	expireNotice(first, notice)
	if first.statusMessage != "All Done" || statusMessage != "All Done" || current != 1 {
		t.Errorf("the parked tab's notice fades in place: parked %q front %q current %d", first.statusMessage, statusMessage, current)
	}
	press(t, "y")
	front := statusMessage
	first.closed = true
	expireNotice(first, front)
	if statusMessage != front {
		t.Error("a closed tab's timer touches nothing")
	}
	first.closed = false
}

func TestFooterNoticeConfig(t *testing.T) {
	resetKeysAndTheme(t)
	oldTTL := noticeTTL
	t.Cleanup(func() { noticeTTL = oldTTL })
	var cfg Config
	if _, err := toml.Decode("[footer]\nnotice_seconds = 2\n", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cfg, ""); err != nil || noticeTTL != 2*time.Second {
		t.Errorf("notice_seconds = 2: ttl %v err %v", noticeTTL, err)
	}
	if err := applyConfig(Config{}, ""); err != nil || noticeTTL != defaultNoticeSeconds*time.Second {
		t.Errorf("default: ttl %v err %v", noticeTTL, err)
	}
	if _, err := toml.Decode("[footer]\nnotice_seconds = -1\n", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cfg, ""); err == nil || !strings.Contains(err.Error(), "notice_seconds must be 0 or more") {
		t.Errorf("a negative time is rejected: %v", err)
	}
}
