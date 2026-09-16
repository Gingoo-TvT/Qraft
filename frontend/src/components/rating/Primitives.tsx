import { cloneElement, isValidElement, useId, type ReactNode, type ReactElement } from 'react';
import type { ReferenceEstimate } from '@/lib/rating-types';
export function Panel({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
 return <section className="forge-card space-y-4"><div><h2 className="text-lg font-semibold">{title}</h2>{description && <p className="mt-1 text-sm text-[var(--dm)]">{description}</p>}</div>{children}</section>;
}
export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
 const id = useId();
 const control = isValidElement(children) ? cloneElement(children as ReactElement<Record<string, unknown>>, { id, 'aria-labelledby': id + '-label', ...(hint ? { 'aria-describedby': id + '-hint' } : {}) }) : children;
 return <div className="block space-y-1.5 text-sm"><label id={id + '-label'} htmlFor={id} className="block font-medium">{label}</label>{control}{hint && <p id={id + '-hint'} className="text-xs text-[var(--dm)]">{hint}</p>}</div>;
}
export function Message({ children, error = false }: { children: ReactNode; error?: boolean }) {
 return <div role={error ? 'alert' : 'status'} className={'rounded-lg border p-4 text-sm ' + (error ? 'border-danger-400/30 text-danger-600 dark:text-danger-400' : 'border-[var(--dl)] bg-[var(--ds)] text-[var(--dm)]')}>{children}</div>;
}
export function BulletList({ items }: { items: string[] }) {
 return items?.length ? <ul className="list-disc space-y-1 pl-5 text-sm text-[var(--dm)]">{items.map((text, i) => <li key={i}>{text}</li>)}</ul> : null;
}
export function estimateLabel(estimate?: ReferenceEstimate): string {
 if (estimate?.lower !== undefined && estimate?.upper !== undefined) return estimate.lower + '–' + estimate.upper;
 return '参照不足';
}
export const dateLabel = (value?: string) => value ? new Date(value).toLocaleString('zh-CN') : '—';
