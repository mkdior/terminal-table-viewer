# ttv: Terminal Table Viewer (TTV)

**A fast, feature-rich CSV/TSV/delimited file viewer for the command line**

[![test](https://github.com/mkdior/terminal-table-viewer/actions/workflows/test.yml/badge.svg)](https://github.com/mkdior/terminal-table-viewer/actions/workflows/test.yml)
[![GitHub license](https://img.shields.io/github/license/mkdior/terminal-table-viewer.svg)](https://github.com/mkdior/terminal-table-viewer/blob/main/LICENSE)
[![GitHub release](https://img.shields.io/github/release/mkdior/terminal-table-viewer.svg)](https://github.com/mkdior/terminal-table-viewer/releases)

TTV continues the work originally created by Xiuqiang (Stephen) Chen
([@codechenx](https://github.com/codechenx)). This repository continues that
work after the original project went unmaintained. See [Credits](#credits).

## Table of Contents

- [Features](#features)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Command Line Flags](#command-line-flags)
- [Key Bindings](#key-bindings)
- [Features in Detail](#features-in-detail)
- [Advanced Examples](#advanced-examples)
- [Large Files](#large-files)
- [Development](#development)
- [Credits](#credits)
- [License](#license)

## Features

TTV brings spreadsheet-like functionality to your terminal with vim-inspired
controls.

- **Spreadsheet interface**: navigate tabular data with frozen headers; the
  row under the cursor is tinted so it can be followed across wide tables
- **Smart parsing**: detects the delimiter (comma, tab, pipe, semicolon, or
  anything consistent) and tolerates ragged rows
- **Progressive loading**: the table appears immediately and fills in while a
  large file streams in
- **Gzip support**: reads compressed files directly
- **Search**: plain text or regex, with highlighting and next/previous
  navigation
- **Filtering**: per-column filters with text, regex, numeric and date
  operators, combined across columns, plus unique filters that drop
  duplicate values or rows
- **Sorting**: by any column, with string, number and date ordering
- **Column width limits**: cap wide columns so the rest of the table stays
  readable
- **Statistics and plots**: per-column statistics with an ASCII histogram or
  frequency chart
- **Vim keybindings**: h/j/k/l, gg/G, 0/$, Ctrl-d/Ctrl-u, count prefixes
  such as `5j` or `12G`, visual mode over cells, and `y`/`Y` to copy cells or
  rows to the clipboard; every key is remappable in a config file
- **Editing**: changes are staged like in fdisk and written with `W`; `dd`
  and `d` with a motion remove rows or columns, visual `d` removes the
  selection, `x` clears cells, `X` cuts to the clipboard, `E` opens a vim
  line editor on the cell, `u` undoes; every write keeps a backup of the
  previous version
- **Mouse support**: click to select, scroll to move, click buttons in dialogs
- **Pipe support**: reads from stdin for use in shell pipelines

## Installation

### Install script (Linux/macOS)

Downloads the latest release for your platform into the current directory:

```bash
curl -sSL https://raw.githubusercontent.com/mkdior/terminal-table-viewer/main/install.sh | bash
sudo mv ttv /usr/local/bin/
```

### Manual download

Every tagged release on the
[releases page](https://github.com/mkdior/terminal-table-viewer/releases) ships a
single static binary per platform, built by the `release` GitHub Actions
workflow:

- Archives named `ttv_<version>_<OS>_<arch>.tar.gz` (`.zip` on Windows) for
  Linux (x86_64, arm64, armv7, i386), macOS (Intel, Apple Silicon) and
  Windows (x86_64, i386), each containing the `ttv` binary, LICENSE and
  README
- `.deb` and `.rpm` packages for Linux
- `checksums.txt` with SHA-256 sums of every asset

Pick the archive for your system, extract it and put `ttv` somewhere on your
`PATH`. For example, on Linux x86_64 (adjust the version and platform):

```bash
VERSION=0.9.0
curl -LO https://github.com/mkdior/terminal-table-viewer/releases/download/v${VERSION}/ttv_${VERSION}_Linux_x86_64.tar.gz
tar -xzf ttv_${VERSION}_Linux_x86_64.tar.gz ttv

# system-wide
sudo install -m 755 ttv /usr/local/bin/ttv

# or just for your user (make sure ~/.local/bin is on your PATH)
install -D -m 755 ttv ~/.local/bin/ttv
```

Platform strings: `Linux_x86_64`, `Linux_arm64`, `Linux_armv7`,
`Linux_i386`, `Darwin_x86_64`, `Darwin_arm64`, `Windows_x86_64.zip`,
`Windows_i386.zip`.

On macOS, Gatekeeper may block an unsigned binary the first time; run
`xattr -d com.apple.quarantine ttv` before installing it.

Packages:

```bash
sudo dpkg -i ttv_*.deb      # Debian/Ubuntu
sudo rpm -i ttv-*.rpm       # Fedora/CentOS/RHEL
```

### Go install

```bash
go install github.com/mkdior/terminal-table-viewer/cmd/ttv@latest
```

### Build from source

Requires Go 1.25 or later:

```bash
git clone https://github.com/mkdior/terminal-table-viewer.git
cd terminal-table-viewer
make build        # produces ./ttv with the version stamped from git
```

## Quick Start

```bash
ttv data.csv                       # view a CSV file
ttv data.tsv                       # view a TSV file
cat data.csv | ttv                 # read from stdin
ps aux | ttv                       # any whitespace-delimited output
ttv data.txt -s "|"                # custom delimiter
ttv data.csv --columns 1,3,5       # only some columns
ttv file.vcf --skip-prefix "##"    # skip metadata lines
```

## Command Line Flags

Syntax: `ttv [FILE] [flags]`

- `-s`, `--separator <char>`: the delimiter; use `\t` for tab. By default it
  is detected from the first lines, with `.csv` and `.tsv` suffixes as a hint.
- `-n`, `--lines <N>`: load only the first N lines.
- `--skip-prefix <p1,p2,...>`: skip lines starting with any of the prefixes.
- `--skip-lines <N>`: skip the first N lines.
- `--columns <1,3,...>`: show only these columns (1-based).
- `--hide-columns <2,4,...>`: hide these columns (1-based; cannot be combined
  with `--columns`).
- `-f`, `--freeze <mode>`: `-1` none, `0` header row and first column
  (default), `1` header row only, `2` first column only.
- `--strict`: fail when a row has a different number of columns than the
  header.
- `--async` (default `true`): render progressively while loading;
  `--async=false` loads everything first and prints progress to the terminal.
- `-m`, `--memory <MB>`: stop loading when the estimated memory use reaches
  the limit (`0`, the default, means unlimited); the rows loaded so far stay
  viewable and the footer says why loading stopped.
- `--theme <name>`: a built-in colour scheme (the list is in `--help`); the
  default is the `name` in the config file, else `subcore`.
- `--config <path>`: the config file with key bindings and colours; the
  default is `~/.config/ttv/config.toml` (`$XDG_CONFIG_HOME/ttv/config.toml`).
  A missing default file is ignored, a missing named file is an error.
- `--dump-config`: print the default configuration with comments and exit;
  save it as the config file and edit.
- `-h`, `--help` and `-v`, `--version`.

## Key Bindings

The bindings below are the defaults. Every one of them can be changed in the
config file; see [Configuration](#configuration). The help dialog (`?`) always
shows the bindings that are active.

### Movement

- `h`, `Left`: move left; wraps to the last column from the first
- `l`, `Right`: move right; wraps to the first column from the last
- `j`, `Down`: move down
- `k`, `Up`: move up
- `w`: next column
- `b`: previous column
- `gg`: first row
- `G`: last row
- `0`, `^`: first column (a stray `0` while typing a count jumps here;
  unbind `0` from `first_column` in the config if that bites)
- `$`: last column
- `Ctrl-d`: half a page down
- `Ctrl-u`: half a page up
- `PgDn`, `Ctrl-f`: a page down
- `PgUp`, `Ctrl-b`: a page up
- `Home`, `End`: first or last row

Counts work as in vim: a number before a motion repeats it (`5j`, `3l`, `2w`,
`4n`), `NG` or `Ngg` jumps to row N, and `N Ctrl-d` or `N Ctrl-u` moves N
rows. `0` on its own still goes to the first column. Vertical motions stop at
the first data row; the frozen header is never selected, and an overshooting
count such as `200k` in a 150-row file lands on the first row. Vertical
motions keep the horizontal scroll where it was.

### Search and filter

- `/`: search
- `n`: next search result
- `N`: previous search result
- `Esc`: clear search highlighting, or close the open dialog
- `f`: filter by the current column
- `r`: remove the filter on the current column

### Sort and types

- `s`: sort ascending by the current column (an edit: `u` undoes it)
- `S`: sort descending by the current column
- `t`: toggle the column type (String, Number, Date)

### Yank and paste

- `y`: copy the current cell to the clipboard
- `Y`: copy the current row to the clipboard, cells separated by tabs
- `p`, `P`: paste the last yank or removal over the cell; a block is laid out
  from the cursor, in visual mode a single value fills the selection

### Visual mode

- `v`, `Ctrl-v`: visual mode; select a block of cells from here to the cursor
- `V`: visual line mode; select whole rows
- `o`: swap the anchor and the cursor

### Editing

- `dd`: remove the current row; `3dd` removes three
- `d` + motion: remove the rows a vertical motion spans (`dj`, `d3j`, `dG`,
  `dgg`) or the columns a horizontal one spans (`dl`, `dh`, `d$`, `d0`)
- `d` in visual mode: remove the selected rows (`V`) or columns (`v`)
- `x`: clear the cell; with a count, N cells to the right; in visual mode
  every selected cell
- `X`: remove and copy to the clipboard: the current row, or the visual
  selection
- `E`: edit the cell in a vim line editor (see [Editing](#editing-1))
- `i`, `Ctrl-I`, `a`: edit the cell, inserting at the start or appending at
  the end; in visual mode the text goes into every selected cell
- `cc`: clear the cell and type its new value; in visual mode every selected
  cell gets it
- `ir`, `or`: insert an empty row above or below the cursor (with a count, N
  rows) and start typing in it; in visual mode around the selection
- `ic`, `oc`: insert an empty column left or right of the cursor (with a
  count, N columns) and name it in the header; in visual mode around the
  selection
- `u`: undo the last edit; with a count, N edits
- `W`: write the table back to the file

### View

- `_`: toggle the width limit on the current column
- `zc`: hide the current column behind a narrow marker, like a closed fold;
  in visual mode the selected columns
- `zo`: show the hidden column under the cursor again; in visual mode the
  selected columns
- `za`: hide the current column, or show it when hidden
- `zR`: show every hidden column
- `I`: statistics for the current column
- `?`: help
- `q`: quit; asks whether to write or discard pending edits

### Mouse

- Left click: select the cell under the pointer
- Scroll wheel: move the selection up or down one row
- Click on buttons and checkboxes: works in the search, filter and statistics
  dialogs

Mouse support depends on the terminal; keyboard navigation always works.

## Features in Detail

### Progressive loading

Large files appear instantly and fill in while they load. The footer shows a
progress bar for files whose size is known, and a row counter for pipes and
gzip input. Once loading finishes it shows the row count. Type detection runs
after the load completes, so the column type in the footer may change once.

If loading stops early (memory limit, a line over 1MB, a parse error) the
footer says so and the rows loaded so far remain fully usable.

### Data types and sorting

TTV samples each column after loading and classifies it as String, Number or
Date when at least 90% of the sampled non-empty cells fit. Press `t` to cycle
the type by hand, then `s` or `S` to sort.

- Strings: byte-wise order
- Numbers: numeric order; integers, floats, scientific notation and thousands
  separators (`1,234.5`, `1_234`) are accepted; cells that do not parse sort
  as zero
- Dates: chronological; ISO-8601 (`2024-10-17`, with optional time and zone),
  US (`10/17/2024`), EU (`17/10/2024`), `2024/10/17`, `2024.10.17`,
  `Jan 02, 2006`, `January 02, 2006`, `02-Jan-2006` and `02 Jan 2006`

### Statistics and plots

Press `I` on a column to open the statistics dialog.

- Numeric columns: count, min, max, range, sum, mean, median, mode, standard
  deviation, variance, quartiles and IQR, plus a histogram
- String and date columns: total, unique and empty counts, the frequency of
  each value with percentages, plus a bar chart of the 15 most frequent values

When filters are active, statistics are computed on the filtered rows only
and the dialog title says so.

### Search

1. Press `/`.
2. Type the query. Tab moves between the field, the `Use Regex` and
   `Case Sensitive` checkboxes and the buttons; Space or Enter toggles a
   focused checkbox.
3. Press Enter to search, then `n` and `N` to move between matches and `Esc`
   to clear the highlighting.

Plain text search is a case-insensitive substring match unless
`Case Sensitive` is checked. Regex search uses Go regular expression syntax
and is case-insensitive unless `Case Sensitive` is checked (TTV prepends
`(?i)` for you). The current match is highlighted in the accent colour, other
matches in the panel colour, and the footer shows the position such as
`Match 3/12`.

Regex examples:

- `^ERROR` matches cells starting with ERROR
- `\.txt$` matches cells ending in .txt
- `\d{4}-\d{2}-\d{2}` matches ISO dates
- `user(name)?` matches user or username
- `error|warning|critical` matches any of the three
- `@.*\.(com|org)$` matches email domains ending in .com or .org

### Column filter

1. Move to the column and press `f`.
2. Pick an operator from the dropdown, enter the value and optionally check
   `Case Sensitive`.
3. Press Enter. Repeat on other columns to add more filters; all filters are
   combined with AND.
4. Press `f` on a filtered column to edit it (an empty value removes it), or
   `r` to remove it.

Filtered column headers are marked with asterisks and the alert colour, and a
strip above the footer describes the filter on the current column.

Operators:

- `contains`: the cell contains the value
- `equals`: the cell equals the value
- `starts with`: the cell starts with the value
- `ends with`: the cell ends with the value
- `regex`: the cell matches the regular expression
- `>`, `<`, `>=`, `<=`: numeric comparison on any column; cells that do not
  parse as numbers never match. On a column typed as Date the comparison is
  chronological and the value must be a date in one of the formats listed
  above.
- `unique`: keeps the first row for each distinct value in the column and
  drops the rest, so 200 rows with 12 distinct values in the column become 12
  rows; the value field is ignored
- `unique rows`: keeps the first of each set of rows that are identical in
  every column

Text operators and both unique operators are case-insensitive unless
`Case Sensitive` is checked. An invalid regex or a non-numeric threshold
matches nothing. When filters are combined, the value filters run first and
the unique filters last, so duplicates are removed from the rows that match.

### Yank

`y` copies the current cell and `Y` the current row to the system clipboard.
Rows and blocks are tab-separated with one line per row, so they paste
straight into a spreadsheet or a shell. Yanks over 50MB are refused.

TTV detects the clipboard of the system it runs on and also sends the OSC 52
terminal escape (tmux forwards it when `set -g set-clipboard on` is set;
payloads over 1MB skip it). The footer reports which channels were used.

- Windows and WSL: the Windows clipboard through
  `cmd.exe /c chcp 65001 & clip`, so non-ASCII text survives; plain
  `clip.exe` if `cmd.exe` is missing
- macOS: `pbcopy`
- Linux on Wayland: `wl-copy` (wl-clipboard)
- Linux on X11: `xclip`, else `xsel`
- Termux: `termux-clipboard-set`
- Anything else: the first of those that is installed, else OSC 52 alone, in
  which case the footer says the copy could not be verified

To use another program, set `command` in the `[clipboard]` section of the
config file to anything that reads the text on stdin; `osc52 = false` turns
the escape off.

### Visual mode

Press `v` (or `Ctrl-v`) to anchor a selection at the current cell and move
with the usual motions, counts included, to extend it into a rectangle of
cells; the footer shows its size after every move, `20j` included. `V`
selects whole rows instead, and `v`/`V` switch between the two. `o` swaps the
anchor and the cursor so the other end can be adjusted. `y` copies the
selection as tab-separated text and leaves visual mode (`Y` copies the whole
rows of a block selection), `Esc`, `q` or pressing the same key again cancels
without quitting, and any other command leaves visual mode before running.
The header row is frozen and never part of a selection: `ggVGy` yanks every
data row, and the footer counts those.

### Editing

Editing works like fdisk: every change is staged in memory and the file is
only touched when you press `W`. The footer marks the file `[+]` while edits
are pending and sums them up after each one ("1 column (Age) and 3 rows
removed, 2 cells changed, sorted by Age ascending"). `u` undoes edits one at
a time, structural ones included.

`q` (or Ctrl-C) asks whether to write, discard or stay while edits are
pending. `w`, `d`, `c` or Esc answer directly; Tab moves between the buttons,
and the bright one is the one Enter will press.

#### Rows and columns

`d` is vim's operator:

- `dd` removes the current row; `3dd` three.
- `dj`, `d3j`, `dG` and `dgg` remove the rows a vertical motion spans.
- `dl`, `dh`, `d$` and `d0` remove the columns a horizontal motion spans, with
  vim's rules: no wrap-around, and `dh` in the first column does nothing.
- In visual mode `d` removes the selected rows (`V`) or columns (`v`).
- Counts multiply as in vim: `2d3j` moves six rows down and removes seven,
  `2dG` removes from row 2 to the cursor.
- The last row and the last column cannot be removed.

`X` cuts: it copies before it removes (the current row, or the visual
selection; whole columns are copied with every row of the table) and leaves
the table alone if no clipboard channel accepted the text.

#### Adding rows and columns

`ir` and `or` insert an empty row above or below the cursor, `ic` and `oc` an
empty column left or right, following sc-im (vim's `i` is before, `o` after).
The cursor moves to the new row, whose cell opens in insert mode, or to the
new column, whose header opens for its name (Esc leaves it unnamed). Counts
add several. `u` removes them again. Rows cannot be inserted while a filter
is active, since they would not be visible. Because `i` and `o` are also
commands of their own, they wait half a second for the second key, as vim
does with `timeoutlen`.

#### Cells and the line editor

`x` clears the cell under the cursor (`3x` three cells; in visual mode every
selected cell). `E` opens the cell in a line editor that behaves like a vim
line; `i` and `a` open it straight in insert mode, `cc` clears it first.

- Motions: `h l 0 ^ $ | w b e W B E f F t T ; ,`, with counts.
- Operators: `d c y` with motions or text objects (`iw aw iW aW`, `i"`,
  `a'`, `i(`, `a[`, `i{`, `i<`); `dd cc yy D C Y`.
- Commands: `x X s S r ~ p P`; `u` and `Ctrl-r`; `.` repeats the last change;
  `R` replaces; `i a I A` insert.
- Visual: `v` selects characters, then `o d c y x ~ u U r p`.
- Insert mode: Backspace, Delete, the arrows, Home, End, Ctrl-w and Ctrl-u
  work as usual.
- Leaving: Enter applies the value. Esc in normal mode cancels, as on vim's
  command line. A vertical table motion in normal mode (`j`, `k`, `G`,
  paging) applies the value and moves to that cell, so `i`, text, `Esc`, `j`
  edits a cell and steps to the next. Ctrl-C acts as Esc.
- The register and `.` carry over from cell to cell. The cursor moves by
  code point: combining marks are drawn with their base character but count
  as positions of their own.

#### Bulk edit

In visual mode `i` (or Ctrl-I), `a` and `cc` work like vim's block insert.
The editor opens on the first selected cell; what you type is inserted at the
start (`i`), appended (`a`) or replaces the value (`cc`) in every selected
cell when you press Esc or Enter, as one undo step. Select five empty cells
with `Ctrl-v`, press `Ctrl-I`, type, Esc: all five hold the text.

#### Paste

`y`, `Y`, a visual yank, `X` and `d` also fill an in-app register (the system
clipboard is never read). `p` or `P` replaces the cell under the cursor with
it; a yanked block is laid out from the cursor and clipped to the table; in
visual mode a single value fills every selected cell. Text yanked inside the
cell editor pastes into a cell the same way. `x` does not touch the register,
so a yanked value survives clearing cells.

#### Filters and sorting

An edit made in a filtered view changes the unfiltered table too. A removed
column takes its filter and width limit with it, and `u` brings them back.
Sorting is an edit as well: it applies to the whole table, is written by `W`,
and `u` restores the previous order.

#### Writing and backups

`W` replaces the file atomically: a temporary file next to it, renamed into
place, permissions kept; symlinks are followed; `.gz` files stay gzip. Fields
are quoted only when they contain the separator, a quote or a line break, so
TSV and pipe files keep their look; blank lines dropped on load are not
written back, and line endings become LF.

`W` is refused when the table is not the whole file:

- input from a pipe
- `--lines`, `--skip-lines`, `--skip-prefix`, `--columns` or `--hide-columns`
- a load that stopped early
- ragged rows padded with NaN (load with `--strict` to reject them)
- the file changed on disk since it was loaded, is read-only, or has other
  hard links

Before the file is replaced, a private copy of its previous version goes to
the backup directory (see [backup] under [Configuration](#configuration)), so
a bad edit can be recovered by hand. The footer names the copy after each
write.

#### Deviations from vim

In the table, `x` clears content instead of being `dl` (as in sc-im, the vim
spreadsheet), `X` cuts to the clipboard, `W` writes and `I` shows statistics.
The line editor's keys are vim's and are not remappable; the table keys are.

### Hiding columns

`zc` hides the column under the cursor (in visual mode the selected columns)
the way vim closes a fold: the column collapses to a one-character dimmed
marker (`»`) so its place stays visible. While the cursor is on it, the footer
names it and the preview box shows the column name and the cell's value,
whether or not it would fit. `zo` shows it again, `za` toggles, `zR` shows
every hidden column. Editing a cell in a hidden column opens it first. At
least one column always stays visible. Hiding is a view setting: it is not
written by `W`, not undone by `u`, and it follows its column when columns are
removed or added.

### Column width limits

Columns whose cells exceed 50 characters in the first 100 rows are limited to
50 characters automatically; longer cells are cut with an ellipsis. Press `_`
on any column to toggle its limit. Cells are never wrapped onto several lines.

While the cursor is on a cut cell, a floating box shows the full value,
word-wrapped and titled with the column name, and disappears when you move
on. By default it is centred at the bottom of the table. `position` in the
`[preview]` section of the config file moves it: `bottom` (default), `top`
(centred under the header) or `cursor`, which lays the box over the selected
cell so the value pops out in place, its first line starting where the cell's
text starts (or its last line ending there when there is no room below).
Values longer than 1000 characters, or too tall to fit in half the table, are
not previewed.

## Advanced Examples

### Bioinformatics formats

```bash
ttv sample.vcf --skip-prefix "##"             # VCF, also works on .vcf.gz
ttv otu_table.txt --skip-prefix "# "          # QIIME OTU tables
ttv mutations.maf --skip-prefix "#"           # MAF
ttv intervals.interval_list --skip-prefix "@" # SAM-style headers
ttv peaks.bed --skip-prefix "track","browser" # BED with headers
```

### Everyday use

```bash
ttv app.log -n 1000                            # first 1000 lines only
ttv data.csv --hide-columns 2,4                # hide sensitive columns
git log --pretty=format:'%h%x09%an%x09%ar%x09%s' | ttv
cat data.json | jq -r '.[] | [.id, .name, .value] | @csv' | ttv
ttv data.txt -s ";"                            # semicolon-delimited
```

## Configuration

TTV reads `~/.config/ttv/config.toml` if it exists (or the file named with
`--config`). `ttv --dump-config` prints the defaults with comments; save that
output as the config file and edit what you want to change. Unknown keys are
reported at startup, so a typo cannot silently keep a default.

### [keys]

One line per action, `action = key` or `action = [key, key]`. An empty list
unbinds the action. Digits 1 to 9 are reserved for count prefixes and are
rejected in bindings; `0` may be bound and is the default for `first_column`.
Keys that are bound to nothing do nothing: tview's own table bindings are
never reached, so unbinding `cancel` simply disables Escape.

- Key spellings: a single character such as `h`, `G` or `$`; a name from
  `esc`, `enter`, `tab`, `space`, `left`, `right`, `up`, `down`, `home`,
  `end`, `pgup`, `pgdn`, `f1` to `f12`; a modifier form such as `ctrl+d`,
  `alt+x` or `shift+v`.
- Sequences: a quoted string with spaces is a multi-key chord, for example
  `first_row = "g g"`.
- Validation: a key bound to two actions is rejected at startup with a
  message naming both. A key that is also the start of a longer chord (`i`
  and `i c`) is allowed: it waits half a second for the next key, as vim's
  `timeoutlen` does, and runs on its own when none comes.

Actions, by section of the help dialog:

- Movement: `move_left`, `move_right`, `move_down`, `move_up`,
  `next_column`, `prev_column`, `first_row`, `last_row`, `first_column`,
  `last_column`, `half_page_down`, `half_page_up`, `page_down`, `page_up`
- Search and filter: `search`, `next_match`, `prev_match`, `cancel`,
  `filter`, `remove_filter`
- Sort and types: `sort_asc`, `sort_desc`, `toggle_type`
- Yank and visual: `yank`, `yank_row`, `visual`, `visual_row`, `visual_swap`
- Edit: `delete`, `cut`, `clear`, `edit`, `insert`, `append`, `change`,
  `paste`, `insert_row`, `open_row`, `insert_column`, `open_column`, `undo`,
  `write`
- View: `toggle_width`, `fold_column`, `unfold_column`, `toggle_fold`,
  `unfold_all`, `stats`, `help`, `quit`

```toml
[keys]
move_left  = ["h", "left"]
first_row  = "g g"
quit       = ["q", "ctrl+c"]
stats      = []          # unbound
```

### [theme]

`name` picks the built-in scheme to start from; each other entry overrides
one colour role. Colours are `colour<n>` (an xterm-256 palette index, as in
tmux), `#rrggbb`, or a name such as `red`.

Roles:

- `background`: window and cell background
- `text`: default text
- `dim`: secondary text such as the footer position and hints, and the fold
  marker of hidden columns
- `panel`: raised surfaces such as the header row and input fields
- `stripe`: alternate rows in the statistics table
- `border`: table separators
- `accent`: the cursor, the frozen column, the footer file name, dialog
  borders and keys
- `alert`: active filters and the filter strip
- `selection`: the background of cells inside a visual selection
- `cursorline`: the row under the cursor, tinted so it can be followed across
  a wide table

```toml
[theme]
name       = "subcore"
accent     = "colour208"
alert      = "#ff5f5f"
```

### [preview]

- `position`: `bottom` (default) and `top` centre the full-value box at the
  bottom of the table or under the header; `cursor` lays it over the
  selected cell.

```toml
[preview]
position = "cursor"
```

### [clipboard]

- `command`: a program that reads the text to copy on stdin, replacing the
  automatic detection, for example `xclip -selection clipboard`.
- `osc52`: `true` (default) or `false`; whether to also send the OSC 52
  escape.

```toml
[clipboard]
command = "wl-copy --primary"
osc52   = false
```

### [backup]

Before `W` replaces a file, a copy of its previous version is made in the
backup directory as `<name>.<path hash>.<timestamp>`: created private (file
0600, directory 0700), fsynced before the file is replaced, and named after
the real file with a short hash of its full path so same-named files in
different directories do not mix. The oldest copies of a file are pruned
beyond `keep`.

- `enabled`: keep a backup on every write; default `true`.
- `dir`: the directory; empty means `$XDG_STATE_HOME/ttv/backup`
  (`~/.local/state/ttv/backup`; the local application data directory on
  Windows). `~` is expanded and relative paths are resolved at startup.
- `keep`: how many backups of one file to keep; default 20, `0` keeps them
  all.

```toml
[backup]
enabled = true
dir     = "~/backups/ttv"
keep    = 10
```

## Large Files

TTV keeps every cell in memory. A file of a few hundred MB works well; a
multi-GB file needs several times its size in RAM and will exhaust memory
without a limit. For very large inputs today:

- `ttv big.csv -m 2048` loads until roughly 2GB of estimated cell data and
  keeps that much viewable
- `ttv big.csv -n 1000000` loads the first million lines
- `ttv big.csv --skip-lines 5000000 -n 1000000` looks at a window further in

A streaming design that indexes row offsets on disk and loads only the visible
window is the planned next step and would lift this limit.

## Development

```bash
make build     # build ./ttv
make test      # go test -race with coverage
make lint      # golangci-lint (must be installed)
make snapshot  # local goreleaser dry run (goreleaser must be installed)
```

Releases are built by GoReleaser from the GitHub Actions `release` workflow
whenever a `v*` tag is pushed. The release notes are the output of
`git log --oneline` since the previous tag, produced by
`scripts/release-notes.sh`.

### Colour schemes

Every colour the UI uses comes from one `Theme` value in
`internal/app/theme.go`, keyed by role (background, text, accent, alert and
so on). To add a built-in scheme, add an entry to `builtinThemes`; it becomes
selectable with `--theme <name>`. Users can override any role, or the whole
scheme, in the `[theme]` section of the config file without touching code.

### Layout

- `cmd/ttv`: the executable; holds the build version and calls the app
- `internal/app`: the application: loaders, the table model with filters and
  sorting, statistics and the tview user interface
- `internal/app/testdata`: fixture files used by the tests

## Credits

TTV is a continuation of work created by Xiuqiang (Stephen) Chen
([@codechenx](https://github.com/codechenx)) and originally published at
https://github.com/codechenx/FastTableViewer under the Apache License 2.0.
This repository continues the project with the full original commit history
preserved; see [NOTICE](NOTICE) for the attribution notice.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
