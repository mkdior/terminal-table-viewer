// Package app implements the TTV terminal table viewer: loading, the table
// model with filters and sorting, statistics and the tview user interface.
package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// setupFreezeMode configures row and column freeze settings based on header mode
func setupFreezeMode(b *Buffer) {
	switch args.Header {
	case -1:
		b.rowFreeze, b.colFreeze = 0, 0
	case 0:
		b.rowFreeze, b.colFreeze = 1, 1
	case 1:
		b.rowFreeze, b.colFreeze = 1, 0
	case 2:
		b.rowFreeze, b.colFreeze = 0, 1
	}
}

// loadDataAsync starts async loading and waits for initial data
func loadDataAsync(loader func(*Buffer, chan<- bool, chan<- error), b *Buffer) (chan bool, chan error, error) {
	updateChan := make(chan bool, 10)
	doneChan := make(chan error, 1)

	loader(b, updateChan, doneChan)

	// Wait for initial data or error
	select {
	case <-updateChan:
		// Initial data ready
		return updateChan, doneChan, nil
	case err := <-doneChan:
		// Error during initial loading
		return nil, nil, err
	}
}

// runApp starts the UI application if not in debug mode
func runApp() error {
	if !debug {
		uiRunning.Store(true)
		defer uiRunning.Store(false)
		if err := app.SetRoot(UI, true).SetFocus(UI).Run(); err != nil {
			return err
		}
	}
	return nil
}

// display opens the inputs in tabs and runs the UI: the files named, one tab
// each, or the pipe when stdin is one. label names the kind of input in the
// message for an input with nothing to show, which exits quietly as it
// always has; when only some of several files are empty they are skipped
// with a note in the footer instead.
func display(names []string, pipe io.Reader, sep rune, label string) error {
	skipped, err := openTabs(names, pipe, sep)
	switch {
	case errors.Is(err, errEmpty):
		stopView()
		fmt.Printf("%s is %s\n", label, err)
		os.Exit(0)
	case err != nil:
		return err
	case len(tabs) == 0:
		stopView()
		for _, note := range skipped {
			fmt.Println(note)
		}
		os.Exit(0)
	}
	return runApp()
}

// Execute runs the ttv command line. version is the build version shown by
// --version; the cmd/ttv entrypoint supplies it.
func Execute(version string) {
	initView()
	args.setDefault()
	RootCmd := &cobra.Command{
		Use:     "ttv [FILE...]",
		Version: version,
		Short:   "Terminal table viewer for delimited files in terminal; several files open in tabs",
		Run: func(cmd *cobra.Command, cmdargs []string) {
			if args.Sep == "\\t" {
				args.Sep = "	"
			}
			var sep rune // 0 leaves the separator to detection
			if len([]rune(args.Sep)) > 0 {
				sep = []rune(args.Sep)[0]
			}

			if args.DumpConfig {
				stopView()
				fatalError(dumpConfig(os.Stdout))
				return
			}

			configPath, explicit := defaultConfigPath(), false
			if args.ConfigPath != "" {
				configPath, explicit = args.ConfigPath, true
			}
			cfg, err := loadConfig(configPath, explicit)
			if err == nil {
				err = applyConfig(cfg, args.Theme)
			}
			if err != nil {
				stopView()
				fatalError(err)
			}

			// The memory limit is one budget for every open file (unlimited by default).
			if args.MemoryMB > 0 {
				budget = &memoryBudget{limit: int64(args.MemoryMB) * 1024 * 1024}
			}

			info, err := os.Stdin.Stat()
			fatalError(err)

			//check whether from a console pipe
			if info.Mode()&os.ModeCharDevice != 0 {
				// FILE MODE
				if len(cmdargs) < 1 {
					stopView()
					_ = cmd.Help()
					return
				}

				// Check that every file exists before loading any
				for _, name := range cmdargs {
					if _, err := os.Stat(name); os.IsNotExist(err) {
						stopView()
						fmt.Printf("File not found: %s\n", name)
						os.Exit(1)
					} else if err != nil {
						stopView()
						fmt.Printf("Cannot access file: %s\n", err)
						os.Exit(1)
					}
				}
				fatalError(display(cmdargs, nil, sep, "File"))
			} else {
				// PIPE MODE
				fatalError(display([]string{pipeSourceName}, os.Stdin, sep, "Pipe"))
			}
		},
	}

	RootCmd.Flags().StringVarP(&args.Sep, "separator", "s", "", "Delimiter/separator character (use \\t for tab)")
	RootCmd.Flags().IntVarP(&args.NLine, "lines", "n", 0, "Display only first N lines")
	RootCmd.Flags().StringSliceVar(&args.SkipSymbol, "skip-prefix", []string{}, "Skip lines starting with prefix (comma-separated)")
	RootCmd.Flags().IntVar(&args.SkipNum, "skip-lines", 0, "Skip first N lines")
	RootCmd.Flags().IntSliceVar(&args.ShowNum, "columns", []int{}, "Show only specified columns (comma-separated)")
	RootCmd.Flags().IntSliceVar(&args.HideNum, "hide-columns", []int{}, "Hide specified columns (comma-separated)")
	RootCmd.Flags().IntVarP(&args.Header, "freeze", "f", 0, "Freeze mode: -1=none, 0=row+col, 1=row only, 2=col only")
	RootCmd.Flags().BoolVar(&args.Strict, "strict", false, "Strict mode: fail on missing/inconsistent data")
	RootCmd.Flags().BoolVar(&args.AsyncLoad, "async", true, "Progressive rendering while loading")
	RootCmd.Flags().IntVarP(&args.MemoryMB, "memory", "m", 0, "Memory limit in MB for all open files together (0=unlimited/default, >0=set limit)")
	RootCmd.Flags().BoolVarP(&args.Tabs, "tabs", "p", false, "Open each file in its own tab (always the case with several files; accepted for vim's -p)")
	RootCmd.Flags().StringVar(&args.Theme, "theme", "", "Colour scheme: "+strings.Join(themeNames(), ", ")+" (default from config, else "+defaultThemeName+")")
	RootCmd.Flags().StringVar(&args.ConfigPath, "config", "", "Config file (default ~/.config/ttv/config.toml)")
	RootCmd.Flags().BoolVar(&args.DumpConfig, "dump-config", false, "Print the default config file and exit")
	RootCmd.Flags().SortFlags = false
	err := RootCmd.Execute()
	fatalError(err)
}
