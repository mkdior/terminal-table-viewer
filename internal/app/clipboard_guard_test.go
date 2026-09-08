package app

import (
	"errors"
	"os"
	"testing"
)

// TestMain keeps the tests away from the real clipboard: every yank and
// removal copies to it, and a test without a stub must fail to find a tool
// rather than run cmd.exe or xclip on the developer's machine.
func TestMain(m *testing.M) {
	clipLookPath = func(string) (string, error) { return "", errors.New("no clipboard tools in tests") }
	clipGOOS = "linux"
	clipIsWSL = func() bool { return false }
	clipGetenv = func(string) string { return "" }
	os.Exit(m.Run())
}
