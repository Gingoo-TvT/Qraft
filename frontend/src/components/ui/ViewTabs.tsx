'use client';
import type { ReactNode } from 'react';

export function ViewTabs({ id, value, onChange, items }: { id: string; value: string; onChange: (value: string) => void; items: { id: string; label: ReactNode }[] }) {
 return <div className="af-view-tabs" role="tablist" aria-label="内容视图" onKeyDown={event => {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault(); const index = items.findIndex(item => item.id === value);
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + items.length) % items.length;
  onChange(items[next].id); event.currentTarget.querySelectorAll<HTMLButtonElement>('[role=tab]')[next]?.focus();
 }}>{items.map(item => <button type="button" role="tab" id={id + '-tab-' + item.id} key={item.id} aria-controls={id + '-panel-' + item.id} aria-selected={value === item.id} tabIndex={value === item.id ? 0 : -1} onClick={() => onChange(item.id)}>{item.label}</button>)}</div>;
}
export function ViewPanel({ id, name, active, children, className = '' }: { id: string; name: string; active: string; children: ReactNode; className?: string }) {
 return <div className={'af-view-panel ' + className} role="tabpanel" id={id + '-panel-' + name} aria-labelledby={id + '-tab-' + name} hidden={active !== name} tabIndex={0}>{children}</div>;
}
