//go:build windows

package app

import (
	"os"

	"golang.org/x/sys/windows"
)

// linkCount returns the number of hard links to the file at path, 1 when it
// cannot be determined. os.FileInfo does not carry it on Windows, so the file
// is opened and asked.
func linkCount(path string, _ os.FileInfo) uint64 {
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer func() { _ = f.Close() }()
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &fi); err != nil {
		return 1
	}
	return uint64(fi.NumberOfLinks)
}
