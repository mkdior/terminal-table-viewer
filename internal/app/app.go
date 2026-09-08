// Package app implements the TTV terminal table viewer: loading, the table
// model with filters and sorting, statistics and the tview user interface.
package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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

// validateDataNotEmpty checks if buffer has data rows and exits if empty
func validateDataNotEmpty(b *Buffer, source string) error {
	dataRows := b.rowLen - b.rowFreeze
	if b.rowLen == 0 || dataRows <= 0 {
		stopView()
		if b.rowLen == 0 {
			fmt.Printf("%s is empty (no rows)\n", source)
		} else {
			fmt.Printf("%s is empty (only header, no data rows)\n", source)
		}
		os.Exit(0)
	}
	return nil
}

// startAsyncUpdateHandler manages UI updates during async loading
func startAsyncUpdateHandler(updateChan <-chan bool, doneChan <-chan error) {
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		loadComplete := false
		for !loadComplete {
			select {
			case <-updateChan:
				// Update available - will be handled by ticker
			case err := <-doneChan:
				loadComplete = true
				// The UI is already up: report the outcome in the footer and keep
				// whatever was loaded viewable instead of tearing the screen down.
				status := "Loaded " + strconv.Itoa(b.rowLen) + " rows"
				if err != nil {
					status = "Stopped after " + strconv.Itoa(b.rowLen) + " rows: " + err.Error()
				}
				app.QueueUpdateDraw(func() {
					loadStopped = err != nil
					drawBuffer(b, bufferTable)
					updateFooterWithStatus(status)
				})
			case <-ticker.C:
				// Periodic UI update
				app.QueueUpdateDraw(func() {
					drawBuffer(b, bufferTable)

					// Keep cursor on the first data row if user hasn't moved it
					if !userMovedCursor {
						row, col := bufferTable.GetSelection()
						if first := firstDataRow(b); row != first {
							bufferTable.Select(first, col)
						}
					}

					if loadProgress.TotalBytes.Load() > 0 {
						// Show progress bar for files
						percent := loadProgress.GetPercentage()
						progressBar := makeProgressBar(percent, 15)
						updateFooterWithStatus(fmt.Sprintf("Loading... %s", progressBar))
					} else {
						// Show row count for pipes (no file size)
						updateFooterWithStatus("Loading... " + strconv.Itoa(b.rowLen) + " rows")
					}
				})
			}
		}
	}()
}

// loadDataAsync starts async loading and waits for initial data
func loadDataAsync(loader func(*Buffer, chan<- bool, chan<- error), b *Buffer) (chan bool, chan error, error) {
	userMovedCursor = false // Reset cursor tracking
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
		if err := app.SetRoot(UI, true).SetFocus(UI).Run(); err != nil {
			return err
		}
	}
	return nil
}

// loadAndDisplayAsync handles the complete async loading workflow
func loadAndDisplayAsync(loader func(*Buffer, chan<- bool, chan<- error), source string) error {
	updateChan, doneChan, err := loadDataAsync(loader, b)
	if err != nil {
		return err
	}

	setupFreezeMode(b)
	if err := validateDataNotEmpty(b, source); err != nil {
		return err
	}

	if err := drawUI(b); err != nil {
		return err
	}

	startAsyncUpdateHandler(updateChan, doneChan)
	return runApp()
}

// loadAndDisplaySync handles the complete sync loading workflow
func loadAndDisplaySync(loader func(*Buffer) error, source string) error {
	if err := loader(b); err != nil {
		if !errors.Is(err, errMemoryLimit) {
			return err
		}
		// Rows loaded before the cap stay viewable; say so in the footer.
		statusMessage = "Stopped after " + strconv.Itoa(b.rowLen) + " rows: " + err.Error()
		loadStopped = true
	}

	setupFreezeMode(b)
	if err := validateDataNotEmpty(b, source); err != nil {
		return err
	}

	if err := drawUI(b); err != nil {
		return err
	}

	return runApp()
}

// Execute runs the ttv command line. version is the build version shown by
// --version; the cmd/ttv entrypoint supplies it.
func Execute(version string) {
	initView()
	args.setDefault()
	RootCmd := &cobra.Command{
		Use:     "ttv {File_Name}",
		Version: version,
		Short:   "Terminal table viewer for delimited file in terminal",
		Run: func(cmd *cobra.Command, cmdargs []string) {
			if args.Sep == "\\t" {
				args.Sep = "	"
			}
			if len([]rune(args.Sep)) > 0 {
				b.sep = []rune(args.Sep)[0]
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

			// Configure memory limit
			if args.MemoryMB > 0 {
				b.setMemoryLimit(int64(args.MemoryMB) * 1024 * 1024) // Convert MB to bytes
			}
			// else use default (unlimited - 0)

			info, err := os.Stdin.Stat()
			fatalError(err)

			// Determine if we should use async loading
			useAsync := args.AsyncLoad

			//check whether from a console pipe
			if info.Mode()&os.ModeCharDevice != 0 {
				// FILE MODE
				if len(cmdargs) < 1 {
					stopView()
					_ = cmd.Help()
					return
				}
				//get file name form console
				args.FileName = cmdargs[0]

				// Check if file exists before attempting to load
				if _, err := os.Stat(args.FileName); os.IsNotExist(err) {
					stopView()
					fmt.Printf("File not found: %s\n", args.FileName)
					os.Exit(1)
				} else if err != nil {
					stopView()
					fmt.Printf("Cannot access file: %s\n", err)
					os.Exit(1)
				}

				if useAsync {
					err = loadAndDisplayAsync(func(b *Buffer, updateChan chan<- bool, doneChan chan<- error) {
						go loadFileToBufferAsync(args.FileName, b, updateChan, doneChan)
					}, "File")
					fatalError(err)
				} else {
					err = loadAndDisplaySync(func(b *Buffer) error {
						return loadFileToBuffer(args.FileName, b)
					}, "File")
					fatalError(err)
				}
			} else {
				// PIPE MODE
				args.FileName = pipeSourceName

				if useAsync {
					err = loadAndDisplayAsync(func(b *Buffer, updateChan chan<- bool, doneChan chan<- error) {
						go loadPipeToBufferAsync(os.Stdin, b, updateChan, doneChan)
					}, "Pipe")
					fatalError(err)
				} else {
					err = loadAndDisplaySync(func(b *Buffer) error {
						return loadPipeToBuffer(os.Stdin, b)
					}, "Pipe")
					fatalError(err)
				}
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
	RootCmd.Flags().IntVarP(&args.MemoryMB, "memory", "m", 0, "Memory limit in MB (0=unlimited/default, >0=set limit)")
	RootCmd.Flags().StringVar(&args.Theme, "theme", "", "Colour scheme: "+strings.Join(themeNames(), ", ")+" (default from config, else "+defaultThemeName+")")
	RootCmd.Flags().StringVar(&args.ConfigPath, "config", "", "Config file (default ~/.config/ttv/config.toml)")
	RootCmd.Flags().BoolVar(&args.DumpConfig, "dump-config", false, "Print the default config file and exit")
	RootCmd.Flags().SortFlags = false
	err := RootCmd.Execute()
	fatalError(err)
}
