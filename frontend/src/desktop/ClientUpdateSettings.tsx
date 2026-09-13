import { Download, Loader2, RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { nativeRequest, useDesktop } from './runtime';

export default function ClientUpdateSettings() {
 const { state, refreshState } = useDesktop();
 const [requesting, setRequesting] = useState(false);
 const [error, setError] = useState('');
 const [confirm, setConfirm] = useState(false);
 const update = state.update;
 const busy = requesting || state.operation.busy;
 async function run(action: 'check' | 'download' | 'install' | 'cancel') {
  setRequesting(true); setError('');
  try {
   await nativeRequest('client-update', { action });
   setConfirm(false);
   await refreshState();
  } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  finally { setRequesting(false); }
 }
 return <section className="desktop-panel desktop-settings-panel">
  <h2>客户端更新</h2>
  <p>检查并下载安装 Qraft 官方正式版本，保留现有设置。</p>
  <dl className="desktop-details"><dt>当前版本</dt><dd>v{state.version}</dd><dt>最新版本</dt><dd>{update.latest_version ? 'v' + update.latest_version : update.status === 'no_release' ? '暂无正式版本' : '尚未检查'}</dd></dl>
  <div role="status" aria-live="polite">
   {['checking', 'downloading', 'installing'].includes(update.status) && <Loader2 size={16} className="animate-spin" />}
   <p>{update.message || '点击“检查更新”获取最新正式版本。'}</p>
   {update.status === 'downloading' && <><progress aria-label="更新下载进度" max={update.total || 1} value={update.bytes} /><p>{(update.bytes / 1048576).toFixed(1)} / {(update.total / 1048576).toFixed(1)} MB</p></>}
  </div>
  {(error || update.error) && <p className="desktop-inline-error" role="alert">{error || update.error}</p>}
  <div className="desktop-form-actions">
   {['checking', 'downloading'].includes(update.status) && <button className="desktop-subtle-button" disabled={requesting} onClick={() => void run('cancel')}>取消{update.status === 'checking' ? '检查' : '下载'}</button>}
   <button className="desktop-subtle-button" disabled={busy} onClick={() => void run('check')}><RefreshCw size={14} />检查更新</button>
   {['available', 'error'].includes(update.status) && update.latest_version && <button className="desktop-primary-button" disabled={busy} onClick={() => void run('download')}><Download size={14} />下载更新</button>}
   {update.status === 'ready' && !confirm && <button className="desktop-primary-button" disabled={busy} onClick={() => setConfirm(true)}>安装并重启</button>}
  </div>
  {confirm && <div className="desktop-update-confirm" role="group" aria-label="确认安装更新"><p>请保存正在编辑的内容。安装后会重新打开客户端，继续使用原来的数据目录与连接设置。本次更新只更新客户端，本地后端继续运行。</p><div className="desktop-form-actions"><button className="desktop-primary-button" disabled={busy} onClick={() => void run('install')}>确认安装并重启</button><button className="desktop-subtle-button" disabled={busy} onClick={() => setConfirm(false)}>暂不安装</button></div></div>}
 </section>;
}
