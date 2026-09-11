import { useRef, useState, type CSSProperties } from 'react';
import { Check, Download, Laptop, Moon, Plus, Sun, Trash2, Upload, X } from 'lucide-react';
import type { DesktopTheme } from '@/lib/desktop-runtime';
import { saveTextFile } from '@/lib/export-file';
import { useAppStore } from '@/stores/appStore';
import { useDesktop } from './runtime';
import { BUILTIN_THEMES, MAX_CUSTOM_THEMES, MAX_THEME_BYTES, createTheme, foregroundColor, getTheme, parseThemeJSON, serializeTheme } from './themes';

function ThemePreview({ theme, dark }: { theme: DesktopTheme; dark: boolean }) {
 const colors = theme.colors[dark ? 'dark' : 'light'];
 const style = {
  '--preview-bg': colors.background, '--preview-surface': colors.surface, '--preview-sidebar': colors.sidebar,
  '--preview-border': colors.border, '--preview-text': colors.text, '--preview-muted': colors.muted,
  '--preview-accent': colors.accent, '--preview-button-text': foregroundColor(colors.accent),
 } as CSSProperties;
 return <span className="theme-preview" style={style} aria-hidden="true">
  <span className="theme-preview-sidebar"><i /><b /><i /><i /></span>
  <span className="theme-preview-main"><span className="theme-preview-toolbar"><i /><b /></span>
   <span className="theme-preview-heading" /><span className="theme-preview-line" />
   <span className="theme-preview-row"><i /><i /></span>
  </span>
 </span>;
}
export default function ThemeSettings() {
 const { preferences, updatePreferences, setNotice } = useDesktop();
 const [error, setError] = useState('');
 const [creating, setCreating] = useState(false);
 const [name, setName] = useState('');
 const [accent, setAccent] = useState('#4263A8');
 const [busy, setBusy] = useState(false);
 const input = useRef<HTMLInputElement>(null);
 const theme = getTheme(preferences);
 const custom = preferences.custom_themes ?? [];
 const dark = useAppStore(state => state.darkMode);

 async function saveTheme(next: DesktopTheme) {
  if (custom.length >= MAX_CUSTOM_THEMES) throw new Error('已保存 12 个自定义主题，请先删除不再使用的主题');
  const ok = await updatePreferences({ color_theme: next.id, custom_themes: [...custom, next] });
  if (!ok) throw new Error('主题未能保存，请稍后重试');
  setNotice('已保存并应用「' + next.name + '」');
 }
 async function create() {
  setError(''); setBusy(true);
  try {
   await saveTheme(createTheme(name.trim(), accent, theme));
   setCreating(false); setName('');
  } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  finally { setBusy(false); }
 }
 async function importFile(file?: File) {
  if (!file) return;
  setError(''); setBusy(true);
  try {
   if (file.size > MAX_THEME_BYTES) throw new Error('主题文件不能超过 16 KB');
   const parsed = parseThemeJSON(await file.text());
   await saveTheme({ ...parsed, id: 'custom-' + crypto.randomUUID() });
  } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  finally { setBusy(false); if (input.current) input.current.value = ''; }
 }
 async function exportTheme() {
  setError(''); setBusy(true);
  try { await saveTextFile(serializeTheme(theme), 'qraft-theme-' + theme.id + '.json'); }
  catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  finally { setBusy(false); }
 }
 function removeTheme(id: string) {
  setError('');
  void updatePreferences({
   custom_themes: custom.filter(item => item.id !== id),
   color_theme: preferences.color_theme === id ? 'graphite' : preferences.color_theme,
  });
 }
 function card(item: DesktopTheme) {
  const selected = theme.id === item.id;
  return <button className={'theme-card' + (selected ? ' selected' : '')}
   aria-label={'使用' + item.name + '主题'} aria-pressed={selected} disabled={busy}
   onClick={() => { setError(''); void updatePreferences({ color_theme: item.id }); }}>
   <ThemePreview theme={item} dark={dark} />
   <span className="theme-card-caption"><strong>{item.name}</strong><span>{selected && <Check size={16} />}</span></span>
  </button>;
 }
 return <div className="theme-settings">
  <section className="desktop-panel desktop-settings-panel theme-mode-panel">
   <div className="theme-section-heading"><div><h2>外观模式</h2><p>跟随系统，或为工作台选择固定的明暗外观。</p></div></div>
   <div className="theme-mode-control" role="group" aria-label="桌面主题">
    {[{ value: 'system', label: '跟随系统', Icon: Laptop }, { value: 'light', label: '浅色', Icon: Sun }, { value: 'dark', label: '深色', Icon: Moon }].map(({ value, label, Icon }) =>
     <button key={value} aria-pressed={preferences.theme === value} className={preferences.theme === value ? 'active' : ''}
      onClick={() => { void updatePreferences({ theme: value as typeof preferences.theme }); }}><Icon size={17} />{label}</button>)}
   </div>
  </section>
  <section className="desktop-panel desktop-settings-panel">
   <div className="theme-section-heading"><div><h2>颜色主题</h2><p>每套主题都包含浅色与深色配色，应用后自动保存。</p></div><span className="theme-current-label">当前 · {theme.name}</span></div>
   {error && <div className="desktop-inline-error" role="alert">{error}</div>}
   <div className="theme-grid" aria-label="内置颜色主题">{BUILTIN_THEMES.map(item => <div key={item.id}>{card(item)}</div>)}</div>
   {custom.length > 0 && <div className="theme-custom-section"><div className="theme-section-heading"><h3>我的主题</h3><span>{custom.length} / {MAX_CUSTOM_THEMES}</span></div>
    <div className="theme-grid" aria-label="我的主题">{custom.map(item => <div className="theme-custom-card" key={item.id}>{card(item)}
     <button className="theme-remove" disabled={busy} aria-label={'删除' + item.name + '主题'} title="删除自定义主题" onClick={() => removeTheme(item.id)}><Trash2 size={15} /></button>
    </div>)}</div>
   </div>}
   <div className="theme-actions">
    <button className="desktop-subtle-button" disabled={busy || custom.length >= MAX_CUSTOM_THEMES} onClick={() => { setCreating(!creating); setError(''); setAccent(theme.colors.light.accent); }}><Plus size={16} />创建主题</button>
    <button className="desktop-subtle-button" disabled={busy || custom.length >= MAX_CUSTOM_THEMES} onClick={() => input.current?.click()}><Upload size={16} />导入主题</button>
    <button className="desktop-subtle-button" disabled={busy} onClick={() => void exportTheme()}><Download size={16} />导出当前主题</button>
    <input className="theme-file-input" ref={input} type="file" accept=".json,application/json" aria-label="导入主题 JSON 文件" onChange={e => void importFile(e.target.files?.[0])} />
   </div>
   {creating && <div className="theme-create">
    <div className="theme-section-heading"><div><h3>创建自己的配色</h3><p>保留「{theme.name}」的背景与文字，换上喜欢的强调色。</p></div><button className="theme-close" aria-label="取消创建主题" onClick={() => setCreating(false)}><X size={18} /></button></div>
    <label className="desktop-field">主题名称<input aria-label="自定义主题名称" maxLength={40} value={name} onChange={e => setName(e.target.value)} placeholder="例如：我的书房" /></label>
    <label className="desktop-field">强调色<span className="theme-color-field"><input type="color" aria-label="选择强调色" value={/^#[0-9a-f]{6}$/i.test(accent) ? accent : '#4263A8'} onChange={e => setAccent(e.target.value.toUpperCase())} /><input aria-label="强调色十六进制" value={accent} maxLength={7} onChange={e => setAccent(e.target.value)} spellCheck={false} /></span></label>
    <div className="theme-color-swatches" role="group" aria-label="常用强调色">{['#46546D', '#2563A8', '#7452B8', '#27715A', '#AA4C6B', '#976019'].map(color =>
     <button key={color} aria-label={'强调色 ' + color} aria-pressed={accent.toUpperCase() === color} style={{ background: color, color: foregroundColor(color) }} onClick={() => setAccent(color)}>{accent.toUpperCase() === color && <Check size={16} />}</button>)}</div>
    <button className="desktop-primary-button" disabled={busy || !name.trim()} onClick={() => void create()}><Check size={16} />保存并应用</button>
   </div>}
   <p className="theme-help">导出 JSON 后可分别调整浅色与深色的背景、文字等配色，再导入使用。仅接受颜色值；不可读的配色会提示修改。</p>
  </section>
  <section className="desktop-panel desktop-settings-panel">
   <div className="theme-section-heading"><div><h2>布局</h2><p>外观偏好保存在这台设备，重启软件后继续沿用。</p></div></div>
   <label className="desktop-field theme-density-field">界面密度<select aria-label="界面密度" value={preferences.density} onChange={e => { void updatePreferences({ density: e.target.value as typeof preferences.density }); }}><option value="comfortable">舒适</option><option value="compact">紧凑</option></select></label>
   <label className="desktop-check"><input type="checkbox" checked={preferences.sidebar_collapsed} onChange={e => { void updatePreferences({ sidebar_collapsed: e.target.checked }); }} />收起侧栏文字</label>
  </section>
 </div>;
}
