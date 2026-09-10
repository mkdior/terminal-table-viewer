// Command ttv is a fast table viewer for delimited files in the terminal.
package main

import "github.com/mkdior/terminal-table-viewer/internal/app"

// version is the release version; overridden at build time with
// -ldflags "-X main.version=...".
var version = "0.25.0-dev.3"

func main() {
	app.Execute(version)
}
