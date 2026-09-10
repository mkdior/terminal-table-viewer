package app

import (
	"strings"
	"testing"
)

func TestYankReportsCharactersAndSkipsEmptyBlocks(t *testing.T) {
	setupEditTable(t)
	ran := stubClipboard(t, map[string]bool{"xclip": true}, map[string]string{"DISPLAY": ":0"}, "linux", false)
	t.Cleanup(func() { tableRegister, lineRegister = nil, nil })
	press(t, "y")
	if !strings.HasPrefix(statusMessage, "Yanked 1 cell (2 B, 2 characters)") || len(*ran) != 1 {
		t.Errorf("status %q copies %d", statusMessage, len(*ran))
	}
	press(t, "V j y")
	if !strings.Contains(statusMessage, "Yanked 2 rows (23 B, 23 characters)") {
		t.Errorf("status %q", statusMessage)
	}
	// A multi-byte value counts its characters, not its bytes.
	press(t, "E S ü enter")
	press(t, "y")
	if !strings.Contains(statusMessage, "(2 B, 1 characters)") {
		t.Errorf("status %q", statusMessage)
	}
	press(t, "u")
	// A block of empty cells is nothing to copy; the register is left alone.
	press(t, "g g 0 j x") // cut a2: the register holds it, the cell is empty
	before := len(*ran)
	press(t, "y")
	if statusMessage != "Nothing to copy" || len(*ran) != before || string(lineRegister) != "a2" {
		t.Errorf("status %q copies %d register %q", statusMessage, len(*ran)-before, string(lineRegister))
	}
	press(t, "v l y") // a block with one empty cell still copies
	if !strings.HasPrefix(statusMessage, "Yanked 1 rows x 2 columns") || (*ran)[len(*ran)-1] != "xclip:\tb2" {
		t.Errorf("status %q copied %q", statusMessage, (*ran)[len(*ran)-1])
	}
}

func TestMuxPassthroughWrapsTheEscape(t *testing.T) {
	seq := osc52("hi")
	if seq != "\x1b]52;c;aGk=\x1b\\" {
		t.Fatalf("osc52 = %q", seq)
	}
	if got := muxPassthrough(seq, "tmux"); got != "\x1bPtmux;\x1b\x1b]52;c;aGk=\x1b\x1b\\\x1b\\" {
		t.Errorf("tmux passthrough = %q", got)
	}
	// screen gets the escape in DCS pieces of at most screenDCSChunk bytes,
	// in order, nothing left over.
	long := osc52(strings.Repeat("x", 600))
	out, pieces := muxPassthrough(long, "screen"), 0
	for pos := 0; pos < len(long); pieces++ {
		n := min(screenDCSChunk, len(long)-pos)
		want := "\x1bP" + long[pos:pos+n] + "\x1b\\"
		if !strings.HasPrefix(out, want) {
			t.Fatalf("screen piece %d differs: %q...", pieces, out[:min(len(out), 20)])
		}
		out, pos = out[len(want):], pos+n
	}
	if out != "" || pieces != (len(long)+screenDCSChunk-1)/screenDCSChunk {
		t.Errorf("screen passthrough: %d pieces, %d bytes left over", pieces, len(out))
	}
	if muxPassthrough(seq, "") != seq || muxPassthrough(seq, "zellij") != seq {
		t.Error("no multiplexer, no wrapping")
	}
	stubClipboard(t, map[string]bool{}, map[string]string{"TMUX": "/tmp/tmux-1000/default,1,0"}, "linux", false)
	if clipMux() != "tmux" {
		t.Errorf("clipMux = %q", clipMux())
	}
	stubClipboard(t, map[string]bool{}, map[string]string{"STY": "1.pts-0.host"}, "linux", false)
	if clipMux() != "screen" {
		t.Errorf("clipMux = %q", clipMux())
	}
	stubClipboard(t, map[string]bool{}, map[string]string{}, "linux", false)
	if clipMux() != "" {
		t.Errorf("clipMux = %q", clipMux())
	}
}
