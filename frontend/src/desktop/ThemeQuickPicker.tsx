import { useCallback, useEffect, useRef, useState } from 'react';
import { ArrowUpRight, Check, Laptop, Moon, Palette, Sun } from 'lucide-react';
import { useAppStore } from '@/stores/appStore';
import { useDesktop } from './runtime';
import Link from './router';
import { BUILTIN_THEMES, getTheme } from './themes';

export default function ThemeQuickPicker() {
 const { preferences, updatePreferences } = useDesktop();
 const dark = useAppStore(state => state.darkMode);
 const [open, setOpen] = useState(false);
 const root = useRef<HTMLDivElement>(null);
 const trigger = useRef<HTMLButtonElement>(null);
 const panel = useRef<HTMLDivElement>(null);
 const current = getTheme(preferences);
 const close = useCallback((restoreFocus = true) => {
  setOpen(false);
  if (restoreFocus) trigger.current?.focus({ preventScroll: true });
 }, []);
 useEffect(() => {
  if (!open) return;
  panel.current?.querySelector<HTMLButtonElement>('[data-current-theme="true"]')?.focus({ preventScroll: true });
  const outside = (event: PointerEvent) => {
   if (event.target instanceof Node && !root.current?.contains(event.target)) close();
  };
  const escape = (event: KeyboardEvent) => {
   if (event.key === 'Escape') { event.preventDefault(); close(); }
  };
  document.addEventListener('pointerdown', outside);
  document.addEventListener('keydown', escape);
  return () => {
   document.removeEventListener('pointerdown', outside);
   document.removeEventListener('keydown', escape);
  };
 }, [open, close]);
 const custom = preferences.custom_themes ?? [];
 function colorOptions(themes: typeof BUILTIN_THEMES) {
  return themes.map(theme => <button type="button" key={theme.id}
   className={'theme-quick-choice' + (current.id === theme.id ? ' selected' : '')}
   aria-label={'切换到' + theme.name + '主题'} aria-pressed={current.id === theme.id}
   data-current-theme={current.id === theme.id}
   onClick={() => { void updatePreferences({ color_theme: theme.id }); }}>
   <i style={{ background: theme.colors[dark ? 'dark' : 'light'].accent }} aria-hidden="true" />
   <span>{theme.name}</span><Check size={14} className="theme-quick-check" aria-hidden="true" />
  </button>);
 }
 return <div className="theme-quick" ref={root}>
  <button type="button" ref={trigger} className="desktop-toolbar-icon" title="外观与主题"
   aria-label="外观与主题" aria-expanded={open} aria-haspopup="dialog" aria-controls={open ? 'desktop-quick-theme' : undefined}
   onClick={() => open ? close() : setOpen(true)}><Palette size={17} /></button>
  {open && <div id="desktop-quick-theme" role="dialog" aria-label="快捷主题" className="theme-quick-panel" ref={panel}
   onKeyDown={event => {
    if (event.key !== 'Tab') return;
    const controls = Array.from(panel.current?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href]') ?? []);
    const first = controls[0], last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
   }}>
   <div className="theme-quick-heading"><strong>外观与主题</strong><span>即时应用</span></div>
   <div className="theme-quick-modes" role="group" aria-label="快捷外观模式">
    {[{ id: 'system', label: '系统', Icon: Laptop }, { id: 'light', label: '浅色', Icon: Sun }, { id: 'dark', label: '深色', Icon: Moon }].map(({ id, label, Icon }) =>
     <button type="button" key={id} aria-label={id === 'system' ? '跟随系统' : label}
      aria-pressed={preferences.theme === id} className={preferences.theme === id ? 'active' : ''}
      onClick={() => { void updatePreferences({ theme: id as typeof preferences.theme }); }}><Icon size={15} />{label}</button>)}
   </div>
   <p className="theme-quick-label">颜色主题</p>
   <div className="theme-quick-grid" role="group" aria-label="快捷预设主题">{colorOptions(BUILTIN_THEMES)}</div>
   {custom.length > 0 && <><p className="theme-quick-label">我的主题</p><div className="theme-quick-grid theme-quick-custom" role="group" aria-label="快捷自定义主题">{colorOptions(custom)}</div></>}
   <Link href="/desktop/settings?tab=appearance" className="theme-quick-manage" onClick={() => close(false)}><span>管理主题</span><ArrowUpRight size={15} /></Link>
  </div>}
 </div>;
}
