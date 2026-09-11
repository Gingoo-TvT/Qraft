import type { DesktopPreferences, DesktopTheme, ThemeColors } from '@/lib/desktop-runtime';

export const MAX_CUSTOM_THEMES = 12;
export const MAX_THEME_BYTES = 16 * 1024;
const COLOR_KEYS = ['accent', 'background', 'surface', 'sidebar', 'text', 'muted', 'border'] as const;
const HEX = /^#[0-9a-f]{6}$/i;
const ID = /^[a-z][a-z0-9-]{0,63}$/;

const lightBase = { background: '#F6F7F9', surface: '#FFFFFF', sidebar: '#FAFBFC', text: '#20242C', muted: '#606875', border: '#E1E4E9' };
const darkBase = { background: '#16181D', surface: '#1D2027', sidebar: '#191C22', text: '#ECEEF3', muted: '#A7AFBE', border: '#343943' };
function preset(id: string, name: string, light: Partial<ThemeColors>, dark: Partial<ThemeColors>): DesktopTheme {
 return { schema: 'algoforge.desktop-theme', version: 1, id, name,
  colors: { light: { ...lightBase, accent: '#46546D', ...light }, dark: { ...darkBase, accent: '#B0BED8', ...dark } } };
}
export const BUILTIN_THEMES: DesktopTheme[] = [
 preset('graphite', '石墨', {}, {}),
 preset('bay', '海湾蓝',
  { accent: '#2563A8', background: '#F4F7FB', sidebar: '#F8FAFD', text: '#1C2B40', muted: '#596C83', border: '#DDE5EF' },
  { accent: '#86B8EE', background: '#131B27', surface: '#1A2636', sidebar: '#162130', text: '#E8F0FA', muted: '#A2B5CF', border: '#314259' }),
 preset('iris', '鸢尾紫',
  { accent: '#7452B8', background: '#F7F6FA', sidebar: '#FAF9FC', text: '#2C2639', muted: '#6C627C', border: '#E5DFED' },
  { accent: '#B9A1EE', background: '#1A1722', surface: '#24202F', sidebar: '#1F1B29', text: '#F0EAF8', muted: '#B7ABC9', border: '#40364F' }),
 preset('pine', '松林绿',
  { accent: '#27715A', background: '#F4F8F6', sidebar: '#F8FBF9', text: '#23362E', muted: '#586F63', border: '#DAE6DE' },
  { accent: '#87C8AA', background: '#141D19', surface: '#1E2A23', sidebar: '#18231D', text: '#E6F0E9', muted: '#A4BAAC', border: '#33493C' }),
 preset('rose', '玫瑰',
  { accent: '#AA4C6B', background: '#FBF6F7', sidebar: '#FDF9FA', text: '#382830', muted: '#7C616D', border: '#ECDFE4' },
  { accent: '#E8A0B8', background: '#21181D', surface: '#2C2027', sidebar: '#261B22', text: '#F5E8ED', muted: '#C8AAB7', border: '#4C3540' }),
 preset('amber', '琥珀',
  { accent: '#976019', background: '#FAF8F3', sidebar: '#FCFBF7', text: '#382F21', muted: '#776C57', border: '#E8E2D5' },
  { accent: '#E1BB77', background: '#201C15', surface: '#2A251C', sidebar: '#251F18', text: '#F1ECDF', muted: '#C2B69D', border: '#494032' }),
];

function rgb(color: string): number[] {
 return [1, 3, 5].map(index => parseInt(color.slice(index, index + 2), 16));
}
function hex(values: number[]): string {
 return '#' + values.map(value => Math.round(value).toString(16).padStart(2, '0')).join('').toUpperCase();
}
export function mixColors(from: string, to: string, amount: number): string {
 const a = rgb(from), b = rgb(to);
 return hex(a.map((value, index) => value + (b[index] - value) * amount));
}
function luminance(color: string): number {
 const c = rgb(color).map(value => {
  const n = value / 255;
  return n <= 0.04045 ? n / 12.92 : ((n + 0.055) / 1.055) ** 2.4;
 });
 return c[0] * 0.2126 + c[1] * 0.7152 + c[2] * 0.0722;
}
export function contrastRatio(a: string, b: string): number {
 const x = luminance(a), y = luminance(b);
 return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}
export function foregroundColor(background: string): string {
 return contrastRatio('#FFFFFF', background) >= contrastRatio('#000000', background) ? '#FFFFFF' : '#000000';
}
// An accent may be pale (for example a user-selected yellow). Keep its hue,
// but use a readable derivative for labels and links on the actual surfaces.
export function readableAccent(accent: string, backgrounds: string[]): string {
 const good = (color: string) => backgrounds.every(background => contrastRatio(color, background) >= 4.5);
 if (good(accent)) return accent;
 const light = backgrounds.reduce((sum, background) => sum + luminance(background), 0) / backgrounds.length > 0.3;
 const target = light ? '#000000' : '#FFFFFF';
 for (let step = 1; step <= 100; step++) {
  const candidate = mixColors(accent, target, step / 100);
  if (good(candidate)) return candidate;
 }
 return target;
}

function object(value: unknown, label: string): Record<string, unknown> {
 if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(label + '格式不正确');
 return value as Record<string, unknown>;
}
function exactKeys(value: Record<string, unknown>, keys: readonly string[], label: string) {
 if (Object.keys(value).some(key => !keys.includes(key)) || keys.some(key => !(key in value))) {
  throw new Error(label + '字段不正确，请使用导出的主题 JSON 格式');
 }
}
export function validateTheme(value: unknown): DesktopTheme {
 const theme = object(value, '主题');
 exactKeys(theme, ['schema', 'version', 'id', 'name', 'colors'], '主题');
 if (theme.schema !== 'algoforge.desktop-theme' || theme.version !== 1) throw new Error('不支持的主题格式或版本');
 if (typeof theme.id !== 'string' || !ID.test(theme.id)) throw new Error('主题 ID 必须为不超过 64 位的英文小写字母、数字或短横线');
 if (typeof theme.name !== 'string' || !theme.name.trim() || [...theme.name].length > 40 || /[\u0000-\u001f\u007f]/.test(theme.name)) {
  throw new Error('主题名称需为 1–40 个可见字符');
 }
 const colors = object(theme.colors, '主题颜色');
 exactKeys(colors, ['light', 'dark'], '主题颜色');
 const result: Record<string, ThemeColors> = {};
 for (const mode of ['light', 'dark'] as const) {
  const palette = object(colors[mode], mode === 'light' ? '浅色模式' : '深色模式');
  exactKeys(palette, COLOR_KEYS, '配色');
  for (const key of COLOR_KEYS) {
   if (typeof palette[key] !== 'string' || !HEX.test(palette[key] as string)) throw new Error('颜色必须是 #RRGGBB 格式，不支持 CSS、脚本或链接');
  }
  const c = Object.fromEntries(COLOR_KEYS.map(key => [key, (palette[key] as string).toUpperCase()])) as ThemeColors;
  // All normal-sized text, including secondary labels and navigation, must remain readable.
  for (const background of [c.background, c.surface, c.sidebar]) {
   if (contrastRatio(c.text, background) < 4.5 || contrastRatio(c.muted, background) < 4.5) {
    throw new Error((mode === 'light' ? '浅色' : '深色') + '模式的正文或次级文字对比度不足，请调整文字或背景颜色');
   }
  }
  result[mode] = c;
 }
 return { schema: 'algoforge.desktop-theme', version: 1, id: theme.id, name: theme.name.trim(), colors: { light: result.light, dark: result.dark } };
}
export function parseThemeJSON(text: string): DesktopTheme {
 if (new TextEncoder().encode(text).length > MAX_THEME_BYTES) throw new Error('主题文件不能超过 16 KB');
 let value: unknown;
 try { value = JSON.parse(text); } catch { throw new Error('无法读取 JSON，请检查文件格式'); }
 return validateTheme(value);
}
export function serializeTheme(theme: DesktopTheme): string {
 return JSON.stringify(validateTheme(theme), null, 2) + '\n';
}
export function getTheme(preferences: Pick<DesktopPreferences, 'color_theme' | 'custom_themes'>): DesktopTheme {
 return [...BUILTIN_THEMES, ...(preferences.custom_themes ?? [])].find(theme => theme.id === preferences.color_theme) ?? BUILTIN_THEMES[0];
}
export function createTheme(name: string, accent: string, baseTheme: DesktopTheme): DesktopTheme {
 return validateTheme({
  schema: 'algoforge.desktop-theme', version: 1, id: 'custom-' + crypto.randomUUID(), name,
  colors: { light: { ...baseTheme.colors.light, accent }, dark: { ...baseTheme.colors.dark, accent } },
 });
}
export function normalizePreferences(preferences: DesktopPreferences): DesktopPreferences {
 return { ...preferences, color_theme: preferences.color_theme || 'graphite', custom_themes: preferences.custom_themes ?? [] };
}

const SHADES = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950];
function setScale(root: HTMLElement, prefix: string, color: string) {
 const tints: Record<number, number> = { 50: 0.94, 100: 0.87, 200: 0.73, 300: 0.54, 400: 0.29 };
 const shades: Record<number, number> = { 600: 0.12, 700: 0.26, 800: 0.40, 900: 0.57, 950: 0.72 };
 for (const step of SHADES) {
  const value = step < 500 ? mixColors(color, '#FFFFFF', tints[step]) : step > 500 ? mixColors(color, '#000000', shades[step]) : color;
  root.style.setProperty('--' + prefix + '-' + step, rgb(value).join(' '));
 }
}
export function applyTheme(preferences: Pick<DesktopPreferences, 'theme' | 'density' | 'color_theme' | 'custom_themes'>, systemDark: boolean, root: HTMLElement = document.documentElement): boolean {
 const dark = preferences.theme === 'dark' || (preferences.theme === 'system' && systemDark);
 const theme = getTheme(preferences), colors = theme.colors[dark ? 'dark' : 'light'];
 const selected = mixColors(colors.sidebar, colors.accent, dark ? 0.14 : 0.08);
 const accent = readableAccent(colors.accent, [colors.background, colors.surface, colors.sidebar, selected]);
 const variables: Record<string, string> = {
  '--db': colors.background, '--dp': colors.surface, '--ds': colors.sidebar, '--dl': colors.border,
  '--dt': colors.text, '--dm': colors.muted, '--da': accent,
  '--dg': selected, '--dh': mixColors(colors.surface, colors.text, dark ? 0.055 : 0.04),
  '--d-button': colors.accent, '--d-button-text': foregroundColor(colors.accent),
  '--d-button-hover': mixColors(colors.accent, foregroundColor(colors.accent) === '#FFFFFF' ? '#000000' : '#FFFFFF', 0.1),
  '--shadow': dark ? '0 8px 28px #00000030' : '0 8px 28px #20242C0A',
 };
 Object.entries(variables).forEach(([key, value]) => root.style.setProperty(key, value));
 setScale(root, 'forge', theme.colors.light.accent);
 setScale(root, 'anvil', mixColors(theme.colors.light.text, theme.colors.light.muted, 0.24));
 // Preserve surface hierarchy and readable neutral text in shared business pages.
 root.style.setProperty('--anvil-50', rgb(theme.colors.light.sidebar).join(' '));
 root.style.setProperty('--anvil-100', rgb(theme.colors.light.background).join(' '));
 root.style.setProperty('--anvil-400', rgb(colors.muted).join(' '));
 root.style.setProperty('--anvil-500', rgb(colors.muted).join(' '));
 root.style.setProperty('--anvil-600', rgb(colors.muted).join(' '));
 root.style.setProperty('--anvil-700', rgb(theme.colors.dark.border).join(' '));
 root.style.setProperty('--anvil-800', rgb(theme.colors.dark.surface).join(' '));
 root.style.setProperty('--anvil-900', rgb(theme.colors.dark.background).join(' '));
 root.style.setProperty('--anvil-950', rgb(theme.colors.dark.sidebar).join(' '));
 root.classList.toggle('dark', dark);
 root.style.colorScheme = dark ? 'dark' : 'light';
 root.dataset.density = preferences.density;
 root.dataset.colorTheme = theme.id;
 if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent('algoforge:theme', { detail: { theme, dark } }));
 return dark;
}
