package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleTheme() DesktopTheme {
	return DesktopTheme{
		Schema: "algoforge.desktop-theme", Version: 1, ID: "custom-study", Name: "书房",
		Colors: ThemeModes{
			Light: ThemeColors{Accent: "#7452B8", Background: "#F6F7F9", Surface: "#FFFFFF", Sidebar: "#FAFBFC", Text: "#20242C", Muted: "#606875", Border: "#E1E4E9"},
			Dark:  ThemeColors{Accent: "#B9A1EE", Background: "#16181D", Surface: "#1D2027", Sidebar: "#191C22", Text: "#ECEEF3", Muted: "#A7AFBE", Border: "#343943"},
		},
	}
}
func TestLegacyPreferencesAcquireColorThemeWithoutChangingMode(t *testing.T) {
	dir := t.TempDir()
	old := []byte(`{"theme":"dark","density":"compact","sidebar_collapsed":true,"last_path":"/problem-sets/new"}`)
	if err := os.WriteFile(filepath.Join(dir, "ui-preferences.json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPreferences(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ColorTheme != "graphite" || got.Theme != "dark" || got.Density != "compact" || !got.SidebarCollapsed || got.LastPath != "/problem-sets/new" {
		t.Fatalf("legacy preferences changed: %+v", got)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "ui-preferences.json"))
	if string(onDisk) != string(old) {
		t.Fatal("reading old preferences rewrote the user's file")
	}
}
func TestCustomThemeSurvivesRestartAndInvalidUpdatePreservesSavedTheme(t *testing.T) {
	dir := t.TempDir()
	p := DefaultPreferences()
	p.Theme = "dark"
	p.ColorTheme = "custom-study"
	p.CustomThemes = []DesktopTheme{sampleTheme()}
	if err := SavePreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	next := NewManager(dir, CommandRunner{}, RuntimeManifest{})
	loaded, err := next.Preferences()
	if err != nil || !reflect.DeepEqual(loaded, p) {
		t.Fatalf("custom theme lost on restart: %+v %v", loaded, err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "ui-preferences.json"))
	bad := p
	bad.CustomThemes = []DesktopTheme{sampleTheme()}
	bad.CustomThemes[0].Colors.Dark.Muted = "#252525"
	if err := next.SavePreferences(bad); err == nil {
		t.Fatal("unreadable text was saved")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "ui-preferences.json"))
	if string(before) != string(after) {
		t.Fatal("invalid update damaged the current theme")
	}
}
func TestThemePreferencesRejectInvalidDocuments(t *testing.T) {
	cases := map[string]func(*Preferences){
		"missing-selected": func(p *Preferences) { p.ColorTheme = "missing" },
		"duplicate-id":     func(p *Preferences) { p.CustomThemes = append(p.CustomThemes, sampleTheme()) },
		"too-many": func(p *Preferences) {
			for i := 0; i < maxCustomThemes; i++ {
				p.CustomThemes = append(p.CustomThemes, sampleTheme())
			}
		},
		"reserved-id":        func(p *Preferences) { p.CustomThemes[0].ID = "graphite" },
		"future-version":     func(p *Preferences) { p.CustomThemes[0].Version = 2 },
		"wrong-schema":       func(p *Preferences) { p.CustomThemes[0].Schema = "anything" },
		"css-color":          func(p *Preferences) { p.CustomThemes[0].Colors.Light.Accent = "var(--accent)" },
		"url-color":          func(p *Preferences) { p.CustomThemes[0].Colors.Light.Background = "url(https://example.com)" },
		"short-color":        func(p *Preferences) { p.CustomThemes[0].Colors.Light.Text = "#123" },
		"alpha-color":        func(p *Preferences) { p.CustomThemes[0].Colors.Light.Text = "#20242CFF" },
		"unreadable-sidebar": func(p *Preferences) { p.CustomThemes[0].Colors.Light.Sidebar = "#303030" },
		"long-name":          func(p *Preferences) { p.CustomThemes[0].Name = strings.Repeat("字", 41) },
		"control-name":       func(p *Preferences) { p.CustomThemes[0].Name = "坏\n名字" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := DefaultPreferences()
			p.ColorTheme = "custom-study"
			p.CustomThemes = []DesktopTheme{sampleTheme()}
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("invalid theme accepted")
			}
		})
	}
}
func TestThemeJSONKeepsVersionedPalette(t *testing.T) {
	theme := sampleTheme()
	data, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	var next DesktopTheme
	if err = json.Unmarshal(data, &next); err != nil {
		t.Fatal(err)
	}
	if err = next.Validate(); err != nil {
		t.Fatal(err)
	}
	if next != theme {
		t.Fatal("theme JSON lost palette or identity")
	}
}
