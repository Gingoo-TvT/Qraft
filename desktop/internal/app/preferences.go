package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Preferences struct {
	Theme            string         `json:"theme"`
	Density          string         `json:"density"`
	SidebarCollapsed bool           `json:"sidebar_collapsed"`
	LastPath         string         `json:"last_path"`
	ColorTheme       string         `json:"color_theme"`
	CustomThemes     []DesktopTheme `json:"custom_themes,omitempty"`
}

// Fresh installs match the Web workspace; existing files keep their saved or legacy palette.
func DefaultPreferences() Preferences {
	return Preferences{Theme: "system", ColorTheme: "bay", Density: "comfortable", LastPath: "/"}
}
func (p Preferences) Validate() error {
	if p.Theme != "system" && p.Theme != "light" && p.Theme != "dark" {
		return fmt.Errorf("未知界面主题")
	}
	if p.Density != "comfortable" && p.Density != "compact" {
		return fmt.Errorf("未知界面密度")
	}
	if !strings.HasPrefix(p.LastPath, "/") || strings.HasPrefix(p.LastPath, "//") || len(p.LastPath) > 500 || strings.ContainsAny(p.LastPath, "\r\n") {
		return fmt.Errorf("无效的工作台页面")
	}
	return p.validateThemes()
}
func LoadPreferences(dir string) (Preferences, error) {
	b, e := os.ReadFile(filepath.Join(dir, "ui-preferences.json"))
	if os.IsNotExist(e) {
		return DefaultPreferences(), nil
	}
	if e != nil {
		return Preferences{}, e
	}
	var p Preferences
	if e = json.Unmarshal(b, &p); e != nil {
		return p, fmt.Errorf("界面偏好文件损坏，请保留文件并恢复备份：%w", e)
	}
	p.normalizeThemes()
	return p, p.Validate()
}
func SavePreferences(dir string, p Preferences) error {
	p.normalizeThemes()
	if e := p.Validate(); e != nil {
		return e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(dir, "ui-preferences.json"), append(b, '\n'))
}
func (m *Manager) Preferences() (Preferences, error) { return LoadPreferences(m.dir) }
func (m *Manager) SavePreferences(p Preferences) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return SavePreferences(m.dir, p)
}
