import { useEffect, useState } from 'react';
import { CheckCircle2, Copy, Download, FileArchive, Loader2 } from 'lucide-react';
import { nativeRequest, useDesktop, type ExportRecord } from './runtime';
import { formatRelativeTime } from '@/lib/utils';

function bytes(n: number) { return n < 1024 ? n + ' B' : n < 1024 ** 2 ? (n / 1024).toFixed(1) + ' KB' : (n / 1024 ** 2).toFixed(1) + ' MB'; }
export default function Exports() {
 const [records, setRecords] = useState<ExportRecord[]>([]);
 const { setNotice } = useDesktop();
 useEffect(() => {
  let live = true;
  async function load() { try { const result = await nativeRequest<ExportRecord[]>('downloads'); if (live) setRecords(result); } catch (e) { if (live) setNotice(e instanceof Error ? e.message : String(e)); } }
  void load(); const id = window.setInterval(() => void load(), 1000);
  return () => { live = false; window.clearInterval(id); };
 }, [setNotice]);
 return <div className="desktop-exports"><div className="desktop-page-heading"><div><span className="desktop-eyebrow">EXPORTS</span><h1>导出记录</h1><p>本次会话中保存到电脑的文件。题目和题集详情页均可直接导出。</p></div></div>
  <section className="desktop-panel">{records.length ? <div className="desktop-export-list">{records.map(item => <div className="desktop-export-row" key={item.id}><span className="desktop-export-icon">{item.status === 'downloading' ? <Loader2 size={22} className="animate-spin" /> : item.status === 'completed' ? <CheckCircle2 size={22} /> : <FileArchive size={22} />}</span><div><strong>{item.name}</strong><p>{item.path}</p><small>{item.status === 'completed' ? '已保存' : item.status === 'failed' ? '导出失败' : '正在保存'} · {bytes(item.bytes)}{item.total > 0 ? ' / ' + bytes(item.total) : ''} · {formatRelativeTime(item.created)}</small>{item.error && <p role="alert" className="desktop-inline-error">{item.error}</p>}</div>{item.status === 'completed' && <button aria-label={'复制路径：' + item.name} className="desktop-subtle-button" onClick={() => void navigator.clipboard.writeText(item.path).then(() => setNotice('文件路径已复制')).catch(() => setNotice('复制失败，请手动选择路径'))}><Copy size={14} />复制路径</button>}</div>)}</div> :
   <div className="desktop-empty large"><Download size={34} /><strong>还没有导出文件</strong><p>在题目或题集详情中点击下载，软件会弹出系统“另存为”窗口。</p></div>}
  </section>
 </div>;
}
