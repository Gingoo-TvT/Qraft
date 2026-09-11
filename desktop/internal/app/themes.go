package app

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxCustomThemes = 12

type ThemeColors struct {
	Accent     string `json:"accent"`
	Background string `json:"background"`
	Surface    string `json:"surface"`
	Sidebar    string `json:"sidebar"`
	Text       string `json:"text"`
	Muted      string `json:"muted"`
	Border     string `json:"border"`
}
type ThemeModes struct {
	Light ThemeColors `json:"light"`
	Dark  ThemeColors `json:"dark"`
}
type DesktopTheme struct {
	Schema  string     `json:"schema"`
	Version int        `json:"version"`
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Colors  ThemeModes `json:"colors"`
}

var themeHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var customThemeID = regexp.MustCompile(`^custom-[a-z0-9-]{1,57}$`)
var builtinThemeIDs = map[string]bool{"graphite": true, "bay": true, "iris": true, "pine": true, "rose": true, "amber": true}

func (p *Preferences) normalizeThemes() {
	if p.ColorTheme == "" {
		p.ColorTheme = "graphite"
	}
}
func (p Preferences) validateThemes() error {
	if len(p.CustomThemes) > maxCustomThemes {
		return fmt.Errorf("最多保存 12 个自定义主题")
	}
	ids := make(map[string]bool)
	for _, theme := range p.CustomThemes {
		if err := theme.Validate(); err != nil {
			return err
		}
		if ids[theme.ID] {
			return fmt.Errorf("自定义主题 ID 重复")
		}
		ids[theme.ID] = true
	}
	if p.ColorTheme != "" && !builtinThemeIDs[p.ColorTheme] && !ids[p.ColorTheme] {
		return fmt.Errorf("选择的颜色主题不存在")
	}
	return nil
}
func (t DesktopTheme) Validate() error {
	if t.Schema != "algoforge.desktop-theme" || t.Version != 1 {
		return fmt.Errorf("不支持的主题格式或版本")
	}
	if !customThemeID.MatchString(t.ID) {
		return fmt.Errorf("无效的自定义主题 ID")
	}
	if strings.TrimSpace(t.Name) == "" || utf8.RuneCountInString(t.Name) > 40 {
		return fmt.Errorf("主题名称需为 1–40 个可见字符")
	}
	for _, c := range t.Name {
		if c < 32 || c == 127 {
			return fmt.Errorf("主题名称包含不可见控制字符")
		}
	}
	for _, mode := range []ThemeColors{t.Colors.Light, t.Colors.Dark} {
		if err := mode.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (c ThemeColors) Validate() error {
	for _, color := range []string{c.Accent, c.Background, c.Surface, c.Sidebar, c.Text, c.Muted, c.Border} {
		if !themeHex.MatchString(color) {
			return fmt.Errorf("颜色必须是 #RRGGBB 格式")
		}
	}
	for _, background := range []string{c.Background, c.Surface, c.Sidebar} {
		if themeContrast(c.Text, background) < 4.5 || themeContrast(c.Muted, background) < 4.5 {
			return fmt.Errorf("主题正文或次级文字对比度不足")
		}
	}
	return nil
}
func themeLuminance(color string) float64 {
	var channels [3]float64
	for i := range channels {
		value, _ := strconv.ParseUint(color[1+2*i:3+2*i], 16, 8)
		c := float64(value) / 255
		if c <= .04045 {
			channels[i] = c / 12.92
		} else {
			channels[i] = math.Pow((c+.055)/1.055, 2.4)
		}
	}
	return channels[0]*.2126 + channels[1]*.7152 + channels[2]*.0722
}
func themeContrast(a, b string) float64 {
	x, y := themeLuminance(a), themeLuminance(b)
	return (math.Max(x, y) + .05) / (math.Min(x, y) + .05)
}
