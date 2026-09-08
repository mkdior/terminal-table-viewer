package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Theme is the set of colours the UI draws with. Every element takes its
// colour from one of these roles, so a new scheme is just a new Theme value.
// Colours are tcell values: palette indices (tcell.PaletteColor), named
// colours (tcell.ColorRed) and true colour (tcell.NewHexColor) all work.
type Theme struct {
	Background tcell.Color // window and cell background
	Text       tcell.Color // default text
	Dim        tcell.Color // secondary text: footer position, hints, inactive borders
	Panel      tcell.Color // raised surfaces: header row, input fields, highlights
	Stripe     tcell.Color // alternate rows in the statistics table
	Border     tcell.Color // table separators
	Accent     tcell.Color // cursor, frozen column, footer file name, dialog borders, keys
	Alert      tcell.Color // active filters and the filter strip
	Selection  tcell.Color // background of cells inside a visual selection
	CursorLine tcell.Color // background of the other cells in the cursor's row
}

// roles maps config names to the theme's colour roles.
func (t *Theme) roles() map[string]*tcell.Color {
	return map[string]*tcell.Color{
		"background": &t.Background, "text": &t.Text, "dim": &t.Dim, "panel": &t.Panel,
		"stripe": &t.Stripe, "border": &t.Border, "accent": &t.Accent, "alert": &t.Alert,
		"selection": &t.Selection, "cursorline": &t.CursorLine,
	}
}

// themeRoleNames lists the colour roles in config order.
var themeRoleNames = []string{"background", "text", "dim", "panel", "stripe", "border", "accent", "alert", "selection", "cursorline"}

// selectedStyle is the cursor cell and the focused dialog button.
func (t Theme) selectedStyle() tcell.Style {
	return tcell.Style{}.Background(t.Accent).Foreground(t.Background).Bold(true)
}

// highlightStyle marks secondary highlights such as other search matches.
func (t Theme) highlightStyle() tcell.Style {
	return tcell.Style{}.Background(t.Panel).Foreground(t.Accent)
}

// tag renders a colour as an inline colour tag for tview dynamic-colour text.
// Tags accept #rrggbb but not palette indices, so the colour is resolved to
// its RGB value; colours without one fall back to the default foreground.
func (t Theme) tag(c tcell.Color) string {
	hex := c.Hex()
	if hex < 0 {
		return "[-]"
	}
	return fmt.Sprintf("[#%06x]", hex)
}

// builtinThemes are the schemes selectable with --theme. Add a scheme here
// (or register one from a config file later) and it is available by name.
var builtinThemes = map[string]Theme{
	// subcore mirrors the maintainer's tmux status bar; the values are the
	// xterm-256 indices used in tmux.conf, so terminal palette overrides
	// apply to both alike.
	"subcore": {
		Background: tcell.PaletteColor(233),
		Text:       tcell.PaletteColor(252),
		Dim:        tcell.PaletteColor(239),
		Panel:      tcell.PaletteColor(237),
		Stripe:     tcell.PaletteColor(235),
		Border:     tcell.PaletteColor(238),
		Accent:     tcell.PaletteColor(101),
		Alert:      tcell.PaletteColor(131),
		Selection:  tcell.PaletteColor(240),
		CursorLine: tcell.PaletteColor(236),
	},
}

// defaultThemeName is the scheme used when --theme is not given.
const defaultThemeName = "subcore"

// theme is the active scheme. setTheme replaces it before the UI is built.
var theme = builtinThemes[defaultThemeName]

// setTheme activates the named built-in scheme.
func setTheme(name string) error {
	t, ok := builtinThemes[name]
	if !ok {
		return fmt.Errorf("unknown theme %q; available: %s", name, strings.Join(themeNames(), ", "))
	}
	theme = t
	return nil
}

// themeNames lists the built-in schemes in alphabetical order.
func themeNames() []string {
	names := make([]string, 0, len(builtinThemes))
	for name := range builtinThemes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// styleForm applies the active theme to a dialog form, its checkboxes and
// buttons. Call it after all items and buttons have been added.
func styleForm(form *tview.Form) {
	form.SetBorder(true)
	form.SetBorderColor(theme.Accent)
	form.SetBackgroundColor(theme.Background)
	form.SetLabelColor(theme.Text)
	form.SetFieldBackgroundColor(theme.Panel)
	form.SetFieldTextColor(theme.Text)
	form.SetButtonBackgroundColor(theme.Panel)
	form.SetButtonTextColor(theme.Text)
	for i := 0; i < form.GetButtonCount(); i++ {
		form.GetButton(i).SetActivatedStyle(theme.selectedStyle())
	}
	for i := 0; i < form.GetFormItemCount(); i++ {
		if cb, ok := form.GetFormItem(i).(*tview.Checkbox); ok {
			cb.SetLabelColor(theme.Text).SetFieldBackgroundColor(theme.Panel).SetFieldTextColor(theme.Accent)
		}
	}
}
