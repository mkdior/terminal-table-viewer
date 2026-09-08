package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gdamore/tcell/v2"
)

// Config is the on-disk configuration: key bindings and colour scheme.
//
//	[keys]
//	move_left = ["h", "left"]   # one key or a list; "g g" is a two-key sequence
//	quit = ""                   # empty unbinds
//
//	[theme]
//	name = "subcore"            # built-in scheme to start from
//	accent = "colour101"        # colour roles: colour<n>, #rrggbb or a colour name
type Config struct {
	Keys      map[string]keyList `toml:"keys"`
	Theme     map[string]string  `toml:"theme"`
	Clipboard ClipboardConfig    `toml:"clipboard"`
	Preview   PreviewConfig      `toml:"preview"`
	Backup    BackupConfig       `toml:"backup"`
}

// BackupConfig controls where the previous version of a written file is kept.
type BackupConfig struct {
	Enabled *bool  `toml:"enabled"` // keep a backup on every write (default true)
	Dir     string `toml:"dir"`     // directory; empty means $XDG_STATE_HOME/ttv/backup
	Keep    *int   `toml:"keep"`    // backups kept per file (default 20, 0 = all)
}

// PreviewConfig controls the full-value box shown for cut cells.
type PreviewConfig struct {
	Position string `toml:"position"` // cursor (default), top or bottom
}

// ClipboardConfig overrides how yanked text reaches the clipboard.
type ClipboardConfig struct {
	Command string `toml:"command"` // command reading the text on stdin; empty means auto-detect
	OSC52   *bool  `toml:"osc52"`   // also emit the OSC 52 escape (default true)
}

// keyList is one key or a list of keys in TOML.
type keyList []string

// UnmarshalTOML accepts a bare string as well as an array of strings.
func (k *keyList) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		*k = keyList{v}
	case []any:
		out := make(keyList, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("key list contains a non-string value %v", item)
			}
			out = append(out, s)
		}
		*k = out
	default:
		return fmt.Errorf("expected a key or a list of keys, got %T", data)
	}
	return nil
}

// defaultConfigPath is ~/.config/ttv/config.toml, honouring XDG_CONFIG_HOME.
func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ttv", "config.toml")
}

// loadConfig reads path. A missing file is not an error unless explicit is
// set (the user named the file on the command line).
func loadConfig(path string, explicit bool) (Config, error) {
	var cfg Config
	if path == "" {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if os.IsNotExist(err) && !explicit {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// applyConfig installs the keys and theme from cfg. The theme name in the
// config is used unless themeFlag was given on the command line.
func applyConfig(cfg Config, themeFlag string) error {
	km := defaultKeymap()
	names := make([]string, 0, len(cfg.Keys))
	for name := range cfg.Keys {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		info, ok := actionByName(name)
		if !ok {
			return fmt.Errorf("config: unknown action %q in [keys]", name)
		}
		var chords [][]keyStroke
		for _, spec := range cfg.Keys[name] {
			if strings.TrimSpace(spec) == "" {
				continue
			}
			chord, err := parseChord(spec)
			if err != nil {
				return fmt.Errorf("config: %s: %w", name, err)
			}
			chords = append(chords, chord)
		}
		km.set(info.act, chords)
	}
	if err := km.validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	name := themeFlag
	if name == "" {
		name = cfg.Theme["name"]
	}
	if name == "" {
		name = defaultThemeName
	}
	if err := setTheme(name); err != nil {
		return err
	}
	t := theme
	roles := t.roles()
	for role, value := range cfg.Theme {
		if role == "name" {
			continue
		}
		dst, ok := roles[role]
		if !ok {
			return fmt.Errorf("config: unknown theme role %q; roles: %s", role, strings.Join(themeRoleNames, ", "))
		}
		c, err := parseColor(value)
		if err != nil {
			return fmt.Errorf("config: theme %s: %w", role, err)
		}
		*dst = c
	}
	theme = t
	keys = km
	clipboardOverride = strings.TrimSpace(cfg.Clipboard.Command)
	clipboardOSC52 = cfg.Clipboard.OSC52 == nil || *cfg.Clipboard.OSC52
	pos, err := parsePreviewPosition(cfg.Preview.Position)
	if err != nil {
		return fmt.Errorf("config: preview: %w", err)
	}
	previewPos = pos
	backupEnabled = cfg.Backup.Enabled == nil || *cfg.Backup.Enabled
	backupDirOverride = ""
	if dir := strings.TrimSpace(cfg.Backup.Dir); dir != "" {
		abs, err := expandPath(dir)
		if err != nil {
			return fmt.Errorf("config: backup dir: %w", err)
		}
		backupDirOverride = abs
	}
	backupKeep = defaultBackupKeep
	if cfg.Backup.Keep != nil {
		if *cfg.Backup.Keep < 0 {
			return fmt.Errorf("config: backup keep must be 0 or more, got %d", *cfg.Backup.Keep)
		}
		backupKeep = *cfg.Backup.Keep
	}
	return nil
}

// parseColor reads a colour as colour<n> (xterm-256 index), #rrggbb, or a
// name tcell knows such as "red".
func parseColor(s string) (tcell.Color, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	for _, prefix := range []string{"colour", "color"} {
		if n, ok := strings.CutPrefix(v, prefix); ok {
			v = n
			break
		}
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 || n > 255 {
			return 0, fmt.Errorf("palette index %d out of range 0-255", n)
		}
		return tcell.PaletteColor(n), nil
	}
	if strings.HasPrefix(v, "#") {
		if c := tcell.GetColor(v); c != tcell.ColorDefault {
			return c, nil
		}
		return 0, fmt.Errorf("invalid hex colour %q", s)
	}
	if c := tcell.GetColor(v); c != tcell.ColorDefault {
		return c, nil
	}
	return 0, fmt.Errorf("unknown colour %q", s)
}

// colorSpec renders a colour the way parseColor reads it.
func colorSpec(c tcell.Color) string {
	if c&tcell.ColorIsRGB == 0 && c.Valid() {
		return fmt.Sprintf("colour%d", int(c&^tcell.ColorValid))
	}
	if hex := c.Hex(); hex >= 0 {
		return fmt.Sprintf("#%06x", hex)
	}
	return "default"
}

// dumpConfig writes the default configuration as TOML with comments, ready to
// be saved as the config file and edited.
func dumpConfig(w io.Writer) error {
	var sb strings.Builder
	sb.WriteString("# ttv configuration. Save as " + defaultConfigPath() + " and edit.\n")
	sb.WriteString("# Keys: a single character (\"h\", \"G\", \"$\"), a name (esc, enter, tab, space,\n")
	sb.WriteString("# left, right, up, down, home, end, pgup, pgdn, f1-f12) or a modifier form\n")
	sb.WriteString("# (ctrl+d, alt+x, shift+v). A list binds several keys; a quoted sequence such\n")
	sb.WriteString("# as \"g g\" is a two-key chord; an empty list unbinds the action.\n\n")
	sb.WriteString("[keys]\n")
	for _, a := range actionCatalog {
		quoted := make([]string, len(a.keys))
		for i, k := range a.keys {
			quoted[i] = strconv.Quote(k)
		}
		fmt.Fprintf(&sb, "%-16s = [%s]  # %s\n", a.act, strings.Join(quoted, ", "), a.help)
	}
	sb.WriteString("\n# Colours: colour<n> for an xterm-256 palette index, #rrggbb, or a name such as\n")
	sb.WriteString("# \"red\". name selects the built-in scheme the roles below start from.\n\n")
	sb.WriteString("[theme]\n")
	fmt.Fprintf(&sb, "%-11s = %q\n", "name", defaultThemeName)
	t := builtinThemes[defaultThemeName]
	roles := t.roles()
	for _, role := range themeRoleNames {
		fmt.Fprintf(&sb, "%-11s = %q\n", role, colorSpec(*roles[role]))
	}
	sb.WriteString("\n# Clipboard: by default ttv detects the system (Windows and WSL, macOS, Wayland,\n")
	sb.WriteString("# X11, Termux) and also sends the OSC 52 escape. command replaces the detection\n")
	sb.WriteString("# with a program that reads the text on stdin, e.g. \"xclip -selection clipboard\".\n\n")
	sb.WriteString("[clipboard]\n")
	sb.WriteString("command = \"\"\n")
	sb.WriteString("osc52   = true\n")
	sb.WriteString("\n# Preview: where the box with the full value of a cut cell appears: \"bottom\" and\n")
	sb.WriteString("# \"top\" centre it at the bottom of the table or under the header, \"cursor\" lays it\n")
	sb.WriteString("# over the selected cell.\n\n")
	sb.WriteString("[preview]\n")
	sb.WriteString("position = \"bottom\"\n")
	sb.WriteString("\n# Backup: before W replaces a file, a private copy of the previous version is\n")
	sb.WriteString("# made in dir as <name>.<path hash>.<timestamp>. Empty dir means\n")
	sb.WriteString("# $XDG_STATE_HOME/ttv/backup (~/.local/state/ttv/backup); ~ is expanded.\n")
	sb.WriteString("# keep is how many backups of one file are kept; 0 keeps them all.\n\n")
	sb.WriteString("[backup]\n")
	sb.WriteString("enabled = true\n")
	sb.WriteString("dir     = \"\"\n")
	fmt.Fprintf(&sb, "keep    = %d\n", defaultBackupKeep)
	_, err := io.WriteString(w, sb.String())
	return err
}
