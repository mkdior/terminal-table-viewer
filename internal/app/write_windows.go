//go:build windows

package app

import "os"

// linkCount returns the number of hard links to the file; Windows stats do
// not expose it, so every file counts as singly linked.
func linkCount(os.FileInfo) uint64 { return 1 }
