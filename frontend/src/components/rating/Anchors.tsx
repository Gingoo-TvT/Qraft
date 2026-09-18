'use client';
import { useCallback, useEffect, useState } from 'react';
import { createAnchor, listAnchors, ratingError } from '@/lib/rating-api';
import type { Anchor } from '@/lib/rating-types';
import { Field, Message, Panel, dateLabel } from './Primitives';
const EMPTY = { title: '', source_url: '', rating: 1600, rating_source: '', statement_summary: '', solution_summary: '', population: '', family: '', source_confirmed: false, kc_ids: [] as string[] };
export default function Anchors() {
 const [kcIds, setKCIds] = useState('');
 const [anchors, setAnchors] = useState<Anchor[]>([]);
 const [value, setValue] = useState(EMPTY);
 const [date, setDate] = useState(new Date().toISOString().slice(0, 10));
 const [loading, setLoading] = useState(false);
 const [error, setError] = useState('');
 const [expanded, setExpanded] = useState(false);
 const load = useCallback(async () => { setLoading(true); try { setAnchors(await listAnchors() ?? []); } catch (e) { setError(ratingError(e)); } finally { setLoading(false); } }, []);
 useEffect(() => { void load(); }, [load]);
 async function submit(event: React.FormEvent) {
  event.preventDefault(); setLoading(true); setError('');
  try { await createAnchor({ ...value, kc_ids: kcIds.split(',').map(id => id.trim()).filter(Boolean), retrieved_at: date + 'T00:00:00Z' }); setValue(EMPTY); setKCIds(''); setExpanded(false); await load(); }
  catch (e) { setError(ratingError(e)); } finally { setLoading(false); }
 }
 return <Panel title="审核过的参照题" description="仅将来源已核实、题面和核心解法可比较的历史题加入锚点库。不会自动导入未经确认的材料。">
  {error && <Message error>{error}</Message>}
  {!anchors.length && !loading && <Message>锚点库仍为空。请先选取有可靠来源的历史题，由管理员核对分数与解法后加入；没有足够可比锚点时，评估会显示“参照不足”。</Message>}
  {anchors.length > 0 && <ul className="divide-y divide-[var(--dl)]">{anchors.map(anchor => <li key={anchor.id} className="py-3"><div className="flex justify-between gap-3"><a className="af-link font-medium" href={anchor.source_url} target="_blank" rel="noreferrer">{anchor.title}</a><span>{anchor.rating}</span></div><p className="mt-1 text-sm text-[var(--dm)]">{anchor.population} · {anchor.family} · 审核于 {dateLabel(anchor.reviewed_at)}</p><details className="mt-2 text-sm"><summary className="cursor-pointer text-[var(--dm)]">查看比较依据</summary><p className="mt-2 whitespace-pre-wrap">{anchor.statement_summary}</p><p className="mt-2 whitespace-pre-wrap">{anchor.solution_summary}</p><p className="mt-2 text-[var(--dm)]">分数来源：{anchor.rating_source} · 采集 {dateLabel(anchor.retrieved_at)}</p></details></li>)}</ul>}
  <button className="forge-btn-secondary" onClick={() => setExpanded(!expanded)}>{expanded ? '收起录入' : '录入并审核参照题'}</button>
  {expanded && <form className="space-y-4 border-t border-[var(--dl)] pt-4" onSubmit={submit}>
   <div className="grid gap-4 sm:grid-cols-2">
    <Field label="题目标题"><input className="forge-input" required value={value.title} onChange={e => setValue(v => ({ ...v, title: e.target.value }))} /></Field>
    <Field label="来源链接"><input className="forge-input" type="url" required value={value.source_url} onChange={e => setValue(v => ({ ...v, source_url: e.target.value }))} /></Field>
    <Field label="CF 参考 rating"><input className="forge-input" type="number" min={800} max={3500} step={100} required value={value.rating} onChange={e => setValue(v => ({ ...v, rating: Number(e.target.value) }))} /></Field>
    <Field label="分数来源与核实依据"><input className="forge-input" required value={value.rating_source} onChange={e => setValue(v => ({ ...v, rating_source: e.target.value }))} /></Field>
    <Field label="采集日期"><input className="forge-input" type="date" required value={date} onChange={e => setDate(e.target.value)} /></Field>
    <Field label="适用人群"><input className="forge-input" required placeholder="例如：CF 常规比赛个人参赛者" value={value.population} onChange={e => setValue(v => ({ ...v, population: e.target.value }))} /></Field>
    <Field label="题目结构或家族"><input className="forge-input" required placeholder="用于识别同源题与结构覆盖" value={value.family} onChange={e => setValue(v => ({ ...v, family: e.target.value }))} /></Field>
    <Field label="相关 KC 编号（逗号分隔，选填）"><input className="forge-input" value={kcIds} onChange={e => setKCIds(e.target.value)} /></Field>
   </div>
   <Field label="题面与约束摘要"><textarea className="forge-input min-h-24" required value={value.statement_summary} onChange={e => setValue(v => ({ ...v, statement_summary: e.target.value }))} /></Field>
   <Field label="核心解法与关键观察"><textarea className="forge-input min-h-24" required value={value.solution_summary} onChange={e => setValue(v => ({ ...v, solution_summary: e.target.value }))} /></Field>
   <label className="flex items-start gap-2 text-sm"><input type="checkbox" required checked={value.source_confirmed} onChange={e => setValue(v => ({ ...v, source_confirmed: e.target.checked }))} />我已核实来源、分数、题面与解法，并批准作为参照题。</label>
   <button className="forge-btn-primary" type="submit" disabled={loading}>{loading ? '正在保存…' : '审核并加入锚点库'}</button>
  </form>}
 </Panel>;
}
