//go:build !windows

package app

import (
	"os"
	"syscall"
)

// linkCount returns the number of hard links to the file, 1 when unknown.
func linkCount(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
