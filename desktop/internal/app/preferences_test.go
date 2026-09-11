package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPreferencesSurviveClientRestart(t *testing.T) {
	dir := t.TempDir()
	c := DefaultConfig()
	c.ServerURL = "https://forge.example"
	if e := SaveConfig(dir, c); e != nil {
		t.Fatal(e)
	}
	p := DefaultPreferences()
	p.Theme = "dark"
	p.Density = "compact"
	p.SidebarCollapsed = true
	p.LastPath = "/problem-sets/new"
	if e := SavePreferences(dir, p); e != nil {
		t.Fatal(e)
	}
	next := NewManager(dir, CommandRunner{}, RuntimeManifest{})
	got, e := next.Preferences()
	if e != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("preferences lost: %+v %v", got, e)
	}
	existing, e := LoadConfig(dir)
	if e != nil || existing != c {
		t.Fatal("UI preferences changed service settings")
	}
	p.LastPath = "https://other.example"
	if e := next.SavePreferences(p); e == nil {
		t.Fatal("external last page accepted")
	}
	if e := os.WriteFile(filepath.Join(dir, "ui-preferences.json"), []byte("{bad"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := next.Preferences(); e == nil {
		t.Fatal("corrupt preferences were silently discarded")
	}
}

func TestFreshPreferencesUseBayWithoutCreatingAFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadPreferences(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ColorTheme != "bay" || got.Theme != "system" || got.Density != "comfortable" || got.LastPath != "/" {
		t.Fatalf("fresh install does not match the Web workspace: %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("fresh defaults are invalid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ui-preferences.json")); !os.IsNotExist(err) {
		t.Fatalf("reading defaults unexpectedly created a preferences file: %v", err)
	}
}

func TestSavedPalettesIgnoreNewInstallDefault(t *testing.T) {
	for _, colorTheme := range []string{"graphite", "custom-study"} {
		t.Run(colorTheme, func(t *testing.T) {
			dir := t.TempDir()
			want := Preferences{
				Theme: "dark", Density: "compact", SidebarCollapsed: true,
				LastPath: "/testdata-config", ColorTheme: colorTheme,
			}
			if colorTheme == "custom-study" {
				want.CustomThemes = []DesktopTheme{sampleTheme()}
			}
			original, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "ui-preferences.json")
			if err := os.WriteFile(file, original, 0600); err != nil {
				t.Fatal(err)
			}
			manager := NewManager(dir, CommandRunner{}, RuntimeManifest{})
			got, err := manager.Preferences()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("saved palette or other preferences changed: %+v, %v", got, err)
			}
			onDisk, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(onDisk, original) {
				t.Fatalf("reading existing preferences rewrote their file: %v", err)
			}
			if err := manager.SavePreferences(got); err != nil {
				t.Fatal(err)
			}
			restarted, err := LoadPreferences(dir)
			if err != nil || !reflect.DeepEqual(restarted, want) {
				t.Fatalf("saving and reloading changed the existing palette: %+v, %v", restarted, err)
			}
		})
	}
}
