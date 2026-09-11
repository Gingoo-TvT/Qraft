'use client';

import { useEffect, useRef, useState } from 'react';
import { Check, Laptop, Moon, Palette, Sun } from 'lucide-react';
import { BUILTIN_THEMES } from '@/desktop/themes';
import { useAppStore } from '@/stores/appStore';
import type { WebAppearance } from '@/lib/web-appearance';

export default function WebThemePicker({ appearance, onChange, storageError }: {
 appearance: WebAppearance; onChange: (patch: Partial<WebAppearance>) => void; storageError: boolean;
}) {
 const [open, setOpen] = useState(false);
 const root = useRef<HTMLDivElement>(null);
 const trigger = useRef<HTMLButtonElement>(null);
 const panel = useRef<HTMLDivElement>(null);
 const dark = useAppStore(state => state.darkMode);
 useEffect(() => {
  if (!open) return;
  panel.current?.querySelector<HTMLElement>('[data-current="true"]')?.focus();
  const outside = (event: PointerEvent) => {
   if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false);
  };
  const escape = (event: KeyboardEvent) => {
   if (event.key === 'Escape') { event.preventDefault(); setOpen(false); trigger.current?.focus(); }
  };
  document.addEventListener('pointerdown', outside); document.addEventListener('keydown', escape);
  return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', escape); };
 }, [open]);
 return <div className="af-theme-picker" ref={root}><button type="button" ref={trigger} className="af-icon-button"
  aria-label="外观与主题" aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? 'web-theme-popover' : undefined} title="外观与主题" onClick={() => setOpen(!open)}><Palette size={19} /></button>
  {open && <div id="web-theme-popover" role="dialog" aria-label="网页外观与主题" className="af-theme-popover" ref={panel} onKeyDown={event => {
   if (event.key !== 'Tab') return;
   const items = Array.from(panel.current?.querySelectorAll<HTMLButtonElement>('button') ?? []);
   if (event.shiftKey && document.activeElement === items[0]) { event.preventDefault(); items[items.length - 1]?.focus(); }
   else if (!event.shiftKey && document.activeElement === items[items.length - 1]) { event.preventDefault(); items[0]?.focus(); }
  }}>
   <div className="af-theme-title"><strong>外观与主题</strong><span>即时应用</span></div>
   <div className="af-segmented" role="group" aria-label="界面明暗模式">
    {[{ mode:'system', label:'系统', Icon:Laptop }, { mode:'light', label:'浅色', Icon:Sun }, { mode:'dark', label:'深色', Icon:Moon }].map(({mode,label,Icon}) =>
     <button type="button" key={mode} aria-label={mode === 'system' ? '跟随系统' : label} aria-pressed={appearance.mode === mode} onClick={() => onChange({ mode: mode as WebAppearance['mode'] })}><Icon size={15} />{label}</button>)}
   </div>
   <p className="af-theme-label">颜色主题</p><div className="af-theme-grid">{BUILTIN_THEMES.map(theme =>
    <button type="button" key={theme.id} aria-label={'使用' + theme.name + '主题'} aria-pressed={appearance.colorTheme === theme.id} data-current={appearance.colorTheme === theme.id}
     onClick={() => onChange({ colorTheme: theme.id })}><i style={{ background: theme.colors[dark ? 'dark' : 'light'].accent }} /><span>{theme.name}</span>{appearance.colorTheme === theme.id && <Check size={14} />}</button>)}</div>
   <p className="af-theme-note">{storageError ? '浏览器无法保存偏好，本次打开仍可使用。' : '保存在当前浏览器，不影响正在编辑的内容。'}</p>
  </div>}
 </div>;
}
