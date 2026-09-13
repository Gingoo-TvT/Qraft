import { useEffect, useState } from 'react';
import { ArrowUpRight, Check, Download, FolderOpen, HardDrive, Loader2, Monitor, Network, Play, RefreshCw, Settings2, Square, Terminal } from 'lucide-react';
import type { DesktopConfig } from '@/lib/desktop-runtime';
import ThemeSettings from './ThemeSettings';
import ClientUpdateSettings from './ClientUpdateSettings';
import { navigate, useSearchParams } from './router';
import { nativeRequest, serviceURL, useDesktop } from './runtime';

const sections = [{ id: 'connection', label: '服务连接', icon: Network }, { id: 'local', label: '本地后端', icon: HardDrive }, { id: 'appearance', label: '外观', icon: Monitor }, { id: 'about', label: '关于', icon: Settings2 }];
export default function Settings() {
 const { state, saveConfig, operation, connection, probing, probe, setNotice } = useDesktop();
 const query = useSearchParams();
 const selectedSection = query.get('tab');
 const section = sections.some(item => item.id === selectedSection) ? selectedSection : state.config.mode === 'local' && !state.service_url ? 'local' : 'connection';
 const [config, setConfig] = useState<DesktopConfig>(state.config);
 const [saving, setSaving] = useState(false);
 const [error, setError] = useState('');
 const [bundle, setBundle] = useState('');
 const [versionId, setVersionId] = useState('');
 const [advanced, setAdvanced] = useState(false);
 const { mode, server_url, local_port, schema_version } = state.config;
 useEffect(() => { setConfig({ mode, server_url, local_port, schema_version }); }, [mode, server_url, local_port, schema_version]);
 async function save() {
  setSaving(true); setError('');
  try { await saveConfig(config); } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  finally { setSaving(false); }
 }
 async function run(action: string, arg = '') {
  setError('');
  try { await operation(action, arg); } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
 }
 async function choose() {
  setError('');
  try { const path = await nativeRequest<string>('choose-bundle', {}); if (path) setBundle(path); }
  catch (e) { setError(e instanceof Error ? e.message : String(e)); }
 }
 async function external(url: string) {
  try { await nativeRequest('open-external', { url }); }
  catch (e) { setNotice(e instanceof Error ? e.message : String(e)); }
 }
 return <div className="desktop-settings">
  <div className="desktop-page-heading"><div><span className="desktop-eyebrow">APPLICATION</span><h1>客户端设置</h1><p>选择服务、管理本机后端，或调整软件外观。</p></div></div>
  <div className="desktop-settings-layout">
   <nav aria-label="客户端设置分类">{sections.map(({ id, label, icon: Icon }) => <button key={id} className={section === id ? 'active' : ''} onClick={() => navigate('/desktop/settings?tab=' + id)}><Icon size={16} />{label}</button>)}</nav>
   <div className="desktop-settings-content">
    {error && <div className="desktop-inline-error" role="alert">{error}</div>}
    {section === 'connection' && <>
     <section className="desktop-panel desktop-settings-panel"><h2>连接方式</h2><p>桌面界面始终在本机运行，题库和任务由所选服务保存。</p>
      <div className="desktop-mode-options">
       <label className={config.mode === 'remote' ? 'selected' : ''}><input type="radio" name="connectionMode" checked={config.mode === 'remote'} onChange={() => setConfig({ ...config, mode: 'remote' })} /><Network size={20} /><strong>已有服务</strong><small>连接自己或团队部署的 Qraft 服务</small></label>
       <label className={config.mode === 'local' ? 'selected' : ''}><input type="radio" name="connectionMode" checked={config.mode === 'local'} onChange={() => setConfig({ ...config, mode: 'local' })} /><HardDrive size={20} /><strong>本机后端</strong><small>独立部署完整服务，数据保存在本机</small></label>
      </div>
      {config.mode === 'remote' ? <label className="desktop-field">服务地址<input type="url" aria-label="Qraft 服务地址" value={config.server_url} onChange={e => setConfig({ ...config, server_url: e.target.value })} placeholder="https://qraft.example.com" /><small>填写服务根地址，如 https://qraft.example.com。远程连接无需安装 Docker 或 WSL。</small></label> :
       <label className="desktop-field">本地服务端口<input type="number" aria-label="本地服务端口" min={1024} max={65535} value={config.local_port} onChange={e => setConfig({ ...config, local_port: Number(e.target.value) })} /><small>保存后，前往“本地后端”导入镜像并启动服务。</small></label>}
      <div className="desktop-form-actions"><button className="desktop-primary-button" onClick={() => void save()} disabled={saving || state.operation.busy}>{saving ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}保存连接设置</button><button className="desktop-subtle-button" onClick={() => void probe()} disabled={probing || !state.service_url}><RefreshCw size={14} className={probing ? 'animate-spin' : ''} />检测已保存的连接</button></div>
     </section>
     <section className="desktop-panel desktop-settings-panel"><h2>当前服务</h2><dl className="desktop-details"><dt>地址</dt><dd>{serviceURL(state) || (state.config.mode === 'local' ? '本机后端尚未启动' : '尚未设置')}</dd><dt>连接状态</dt><dd>{probing ? '检测中…' : connection?.ready ? '已连接' : '未连接'}</dd><dt>服务版本</dt><dd>{connection?.release_version || '—'}</dd></dl>{connection?.error && <p className="desktop-inline-error">{connection.error}</p>}</section>
    </>}
    {section === 'local' && <>
     <section className="desktop-panel desktop-settings-panel"><h2>本地后端</h2><p>首次使用需要 Docker Desktop 的 Linux 容器。界面、连接设置和远程模式可以独立使用。</p>
      <ol className="desktop-local-steps"><li><b>1</b><span>检测环境</span></li><li><b>2</b><span>导入后端包</span></li><li><b>3</b><span>启动并配置模型</span></li></ol>
      <div className="desktop-form-actions"><button className="desktop-subtle-button" disabled={state.operation.busy} onClick={() => void run('check')}><Check size={14} />检测 Docker</button><button className="desktop-subtle-button" onClick={() => void external('https://docs.docker.com/desktop/setup/install/windows-install/')}><ArrowUpRight size={14} />Docker 安装说明</button></div>
      <p className="desktop-setting-hint">从同一版本的发布页下载 Qraft 后端镜像包，再在下方选择。<button className="desktop-text-button" onClick={() => void external("https://github.com/Gingoo-TvT/Qraft/releases")}>下载后端包 ↗</button></p><label className="desktop-field">离线后端镜像包<div className="desktop-file-field"><input aria-label="后端镜像包路径" value={bundle} onChange={e => setBundle(e.target.value)} placeholder="选择对应版本的 .tar.gz 文件" /><button className="desktop-subtle-button" onClick={() => void choose()}><FolderOpen size={15} />选择</button></div></label>
      {!state.packaged && <p className="desktop-inline-error">当前开发构建未绑定正式后端清单。本地运行请使用包含后端清单的完整客户端。</p>}
      {state.config.mode !== 'local' && <p className="desktop-setting-hint">当前选择的是已有服务。请先在“服务连接”中保存“本机后端”模式。</p>}
      <div className="desktop-form-actions">
       <button className="desktop-subtle-button" disabled={state.operation.busy || !bundle || !state.packaged || state.config.mode !== 'local'} onClick={() => void run('import', bundle)}><Download size={14} />导入并校验</button>
       <button className="desktop-primary-button" disabled={state.operation.busy || !state.packaged || state.config.mode !== 'local'} onClick={() => void run('start', versionId.trim())}><Play size={14} />启动本地后端</button>
       <button className="desktop-subtle-button" disabled={state.operation.busy || state.config.mode !== 'local'} onClick={() => void run('stop')}><Square size={13} />安全停止</button>
      </div>
     </section>
     <section className="desktop-panel desktop-settings-panel"><h2>模型配置后的应用</h2><p>首次启动会先提供配置工作区。在侧栏“模型配置”和“去重服务”保存设置后，点击下方按钮应用去重配置并启动生成服务。</p>
      <button className="desktop-primary-button" disabled={state.operation.busy || !state.packaged || state.config.mode !== 'local'} onClick={() => void run('sync', versionId.trim())}><RefreshCw size={14} />应用去重配置并启动</button>
      <button className="desktop-text-button" onClick={() => setAdvanced(!advanced)}>{advanced ? '收起高级选项' : '高级选项'}</button>
      {advanced && <label className="desktop-field">去重模型版本 ID（可选）<input aria-label="去重模型版本 ID" value={versionId} onChange={e => setVersionId(e.target.value)} placeholder="仅存在多个匹配版本时填写 UUID" /></label>}
     </section>
     <section className="desktop-panel desktop-settings-panel"><div className="desktop-panel-heading"><h2>状态与日志</h2><div className="desktop-form-actions"><button className="desktop-subtle-button" disabled={state.operation.busy} onClick={() => void run('status')}>查看状态</button><button className="desktop-subtle-button" disabled={state.operation.busy} onClick={() => void run('logs')}><Terminal size={14} />查看日志</button></div></div>
      {state.operation.busy && <p className="desktop-working"><Loader2 className="animate-spin" size={15} />正在处理，请等待完成…</p>}
      <pre className="desktop-log">{state.operation.error ? state.operation.error + '\n\n' : ''}{state.operation.message || '尚无操作记录。'}</pre>
     </section>
    </>}
    {section === 'appearance' && <ThemeSettings />}
    {section === 'about' && <><ClientUpdateSettings /><section className="desktop-panel desktop-settings-panel"><div className="desktop-about-mark">Q</div><h2>Qraft · 题构</h2><p>完整桌面工作台 · v{state.version}</p><p>题目创作、独立校验与题集编排，共用同一套服务能力。</p><dl className="desktop-details"><dt>客户端数据目录</dt><dd>{state.data_dir}</dd><dt>服务版本</dt><dd>{connection?.release_version || '未连接'}</dd></dl><div className="desktop-form-actions"><button className="desktop-subtle-button" onClick={() => void external('https://github.com/Gingoo-TvT/Qraft/releases')}><ArrowUpRight size={14} />版本与下载</button><button className="desktop-subtle-button" onClick={() => void external('https://github.com/Gingoo-TvT/Qraft/blob/main/docs/windows-desktop.md')}>使用说明</button></div></section></>}
   </div>
  </div>
 </div>;
}
