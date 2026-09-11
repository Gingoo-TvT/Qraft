import { useState } from 'react';
import { ArrowRight, HardDrive, Layers3, Loader2, Network } from 'lucide-react';
import { useDesktop } from './runtime';
import ThemeQuickPicker from './ThemeQuickPicker';

export default function Welcome() {
 const { state, saveConfig } = useDesktop();
 const [mode, setMode] = useState<'local' | 'remote' | null>(null);
 const [url, setURL] = useState('');
 const [saving, setSaving] = useState(false);
 const [error, setError] = useState('');
 async function begin() {
  if (!mode) return;
  setSaving(true); setError('');
  try { await saveConfig({ ...state.config, mode, server_url: mode === 'remote' ? url.trim() : '' }); }
  catch (e) { setError(e instanceof Error ? e.message : String(e)); setSaving(false); }
 }
 return <main className="qraft-welcome" data-bundled-workbench="true">
  <header><div className="qraft-welcome-brand"><span><Layers3 size={25} /></span><div><strong>Qraft</strong><small>题构</small></div></div><ThemeQuickPicker /></header>
  <section className="qraft-welcome-content">
   <span className="desktop-eyebrow">YOUR NEXT GREAT PROBLEM</span>
   <h1>让好题，从这里发生。</h1>
   <p className="qraft-welcome-intro">从一个想法，到一道好题，再到一整套练习与比赛。<br />先选择你的工作区，开始这次创作。</p>
   <div className="qraft-welcome-options" role="group" aria-label="选择工作区方式">
    <button type="button" data-desktop-nav="true" aria-pressed={mode === 'local'} className={mode === 'local' ? 'selected' : ''} onClick={() => { setMode('local'); setError(''); }}><HardDrive size={25} /><strong>在本机自行部署</strong><span>创建独立的工作区，题库与任务保存在自己的电脑。</span><small>需要 Docker Desktop · Linux 容器</small></button>
    <button type="button" data-desktop-nav="true" aria-pressed={mode === 'remote'} className={mode === 'remote' ? 'selected' : ''} onClick={() => { setMode('remote'); setError(''); }}><Network size={25} /><strong>连接已有服务</strong><span>填写自己或团队的服务地址，连接后使用该工作区。</span><small>无需 Docker · 需要可访问的服务地址</small></button>
   </div>
   <form onSubmit={event => { event.preventDefault(); void begin(); }}>
    {mode === 'remote' && <label className="desktop-field">服务地址<input type="url" aria-label="Qraft 服务地址" required value={url} onChange={event => setURL(event.target.value)} placeholder="https://qraft.example.com" /><small>填写提供 Web 与 API 的服务根地址。连接后将显示该服务的题库和任务。</small></label>}
    {mode === 'local' && <p className="qraft-welcome-hint">下一步检测 Docker、导入后端包，并配置你自己的模型 API。</p>}
    {error && <p className="desktop-inline-error" role="alert">{error}</p>}
    <button type="submit" className="desktop-primary-button" disabled={!mode || saving || (mode === 'remote' && !url.trim())}>{saving ? <Loader2 size={16} className="animate-spin" /> : <ArrowRight size={16} />}{mode === 'remote' ? '连接工作区' : mode === 'local' ? '开始部署' : '选择一种方式开始'}</button>
   </form>
   <p className="qraft-welcome-footnote">全新的开始。没有预设服务、账号或题库，也不会读取其他客户端的数据。</p>
  </section>
  <footer><span>Qraft · 题构</span><span>v{state.version} · 开源算法创作工作区</span></footer>
 </main>;
}
