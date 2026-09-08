package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gdamore/tcell/v2"
)

func resetKeysAndTheme(t *testing.T) {
	t.Helper()
	oldKeys, oldTheme := keys, theme
	t.Cleanup(func() { keys, theme = oldKeys, oldTheme })
}

func TestApplyConfigKeysAndTheme(t *testing.T) {
	resetKeysAndTheme(t)
	src := `
[keys]
move_left = "z"
move_right = ["m", "right"]
first_row = "g g"
quit = []
[theme]
name = "subcore"
accent = "colour208"
alert = "#ff0000"
text = "white"
`
	var cfg Config
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cfg, ""); err != nil {
		t.Fatal(err)
	}
	if act, _ := keys.resolve([]keyStroke{{key: tcell.KeyRune, ch: 'z'}}); act != actMoveLeft {
		t.Errorf("z should be move_left, got %q", act)
	}
	if act, _ := keys.resolve([]keyStroke{{key: tcell.KeyRune, ch: 'h'}}); act != "" {
		t.Errorf("h should be unbound after remapping move_left, got %q", act)
	}
	if got := keys.keysFor(actMoveRight); got != "m, Right" {
		t.Errorf("move_right keys = %q", got)
	}
	if act, _ := keys.resolve([]keyStroke{{key: tcell.KeyRune, ch: 'q'}}); act != "" {
		t.Errorf("quit should be unbound, got %q", act)
	}
	if theme.Accent != tcell.PaletteColor(208) || theme.Alert != tcell.NewHexColor(0xff0000) || theme.Text != tcell.ColorWhite {
		t.Errorf("theme roles not applied: %+v", theme)
	}
	if theme.Background != builtinThemes["subcore"].Background {
		t.Error("roles not named in the config must keep the built-in value")
	}
}

func TestApplyConfigRejectsBadInput(t *testing.T) {
	resetKeysAndTheme(t)
	cases := map[string]string{
		"unknown action": "[keys]\nfly = \"x\"\n",
		"bad key":        "[keys]\nquit = \"hyper+q\"\n",
		"conflict":       "[keys]\nquit = \"h\"\n",
		"unknown role":   "[theme]\nsparkle = \"red\"\n",
		"bad colour":     "[theme]\naccent = \"colour999\"\n",
		"unknown theme":  "[theme]\nname = \"nope\"\n",
	}
	for name, src := range cases {
		var cfg Config
		if _, err := toml.Decode(src, &cfg); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if err := applyConfig(cfg, ""); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestThemeFlagOverridesConfig(t *testing.T) {
	resetKeysAndTheme(t)
	builtinThemes["test-scheme"] = Theme{Accent: tcell.ColorRed}
	defer delete(builtinThemes, "test-scheme")
	cfg := Config{Theme: map[string]string{"name": "subcore"}}
	if err := applyConfig(cfg, "test-scheme"); err != nil {
		t.Fatal(err)
	}
	if theme.Accent != tcell.ColorRed {
		t.Error("--theme must win over the config's theme name")
	}
}

func TestDumpConfigRoundTrips(t *testing.T) {
	resetKeysAndTheme(t)
	var buf bytes.Buffer
	if err := dumpConfig(&buf); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if _, err := toml.Decode(buf.String(), &cfg); err != nil {
		t.Fatalf("dumped config must parse: %v\n%s", err, buf.String())
	}
	if len(cfg.Keys) != len(actionCatalog) {
		t.Errorf("dump lists %d actions, catalog has %d", len(cfg.Keys), len(actionCatalog))
	}
	if err := applyConfig(cfg, ""); err != nil {
		t.Fatalf("dumped config must apply: %v", err)
	}
	if theme != builtinThemes[defaultThemeName] {
		t.Error("dumped theme must reproduce the default theme exactly")
	}
	for _, a := range actionCatalog {
		if got, want := keys.keysFor(a.act), defaultKeymap().keysFor(a.act); got != want {
			t.Errorf("%s: %q after round trip, want %q", a.act, got, want)
		}
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.toml")
	if _, err := loadConfig(missing, false); err != nil {
		t.Errorf("a missing default config must be ignored, got %v", err)
	}
	if _, err := loadConfig(missing, true); err == nil {
		t.Error("a missing --config file must be reported")
	}
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("[keys]\nquit = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path, true)
	if err != nil || len(cfg.Keys["quit"]) != 1 || cfg.Keys["quit"][0] != "x" {
		t.Errorf("loadConfig = %+v, %v", cfg, err)
	}
	// A misspelt key must not be ignored: enable is not enabled.
	if err := os.WriteFile(path, []byte("[backup]\nenable = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path, true); err == nil || !strings.Contains(err.Error(), "unknown key backup.enable") {
		t.Errorf("unknown keys must be reported, got %v", err)
	}
}

func TestParseColor(t *testing.T) {
	for spec, want := range map[string]tcell.Color{
		"colour101": tcell.PaletteColor(101), "color7": tcell.PaletteColor(7), "233": tcell.PaletteColor(233),
		"#87875f": tcell.NewHexColor(0x87875f), "Red": tcell.ColorRed,
	} {
		got, err := parseColor(spec)
		if err != nil || got != want {
			t.Errorf("parseColor(%q) = %v, %v; want %v", spec, got, err, want)
		}
		if back, err := parseColor(colorSpec(got)); err != nil || back != got {
			t.Errorf("colorSpec(%v) = %q does not round trip: %v %v", got, colorSpec(got), back, err)
		}
	}
	for _, bad := range []string{"", "colour256", "#12", "notacolour"} {
		if _, err := parseColor(bad); err == nil {
			t.Errorf("parseColor(%q) should fail", bad)
		}
	}
	if !strings.HasPrefix(colorSpec(tcell.PaletteColor(101)), "colour") {
		t.Error("palette colours should dump as colour<n>")
	}
}
