import React, { Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowUpRight, BookOpen, Boxes, Check, Command, Download, FileText, FolderOpen, HelpCircle, Layers3, LayoutDashboard, ListChecks, Loader2, Plus, Search, Settings2, ShieldCheck, Sparkles, Tag, Workflow, X } from 'lucide-react';
import Link, { navigate, usePathname } from './router';
import { NotificationToaster } from '@/components/layout/NotificationToaster';
import Home from './Home';
import Welcome from './Welcome';
import Settings from './Settings';
import Exports from './Exports';
import ThemeQuickPicker from './ThemeQuickPicker';
import Header from '@/components/layout/Header';
import Sidebar from '@/components/layout/Sidebar';
import { routePage } from './routes';
import { startupPath } from './startup-path';
import { boot, nativeRequest, useDesktop, type ExportRecord } from './runtime';

const groups = [
 { label: '工作空间', items: [{ to: '/', name: '工作台', icon: LayoutDashboard }, { to: '/problems/new', name: '创建题目', icon: Plus }, { to: '/problem-sets/new', name: '生成题集', icon: Sparkles }, { to: '/problem-sets/assemble', name: '题库组卷', icon: Layers3 }, { to: '/workflows', name: '任务中心', icon: Workflow }] },
 { label: '内容管理', items: [{ to: '/problems', name: '编程题库', icon: FileText }, { to: '/quizzes', name: '客观题库', icon: BookOpen }, { to: '/problem-sets', name: '题集管理', icon: FolderOpen }, { to: '/problems/quarantine', name: '隔离区', icon: ShieldCheck }] },
 { label: '工具与配置', items: [{ to: '/knowledge-points', name: '知识点目录', icon: Tag }, { to: '/settings', name: '模型配置', icon: Settings2 }, { to: '/embedding', name: '去重服务', icon: Boxes }] },
];
const actions = [...groups.flatMap(group => group.items), { to: '/quizzes/new', name: '创建客观题', icon: Plus }, { to: '/quizzes/import', name: '导入客观题', icon: Download }, { to: '/problems/hydro/import', name: '校验 Hydro 包', icon: ShieldCheck }, { to: '/testdata-config', name: '测试数据工具', icon: ListChecks }, { to: '/desktop/settings', name: '客户端设置', icon: Settings2 }, { to: '/desktop/exports', name: '导出记录', icon: Download }];
class PageBoundary extends React.Component<{ children: React.ReactNode }, { error: string }> {
 state = { error: '' };
 static getDerivedStateFromError(error: Error) { return { error: error.message }; }
 render() { return this.state.error ? <div className="desktop-empty large" role="alert"><HelpCircle size={30} /><strong>页面暂时无法显示</strong><p>{this.state.error}</p><button className="desktop-primary-button" onClick={() => this.setState({ error: '' })}>重新加载页面</button></div> : this.props.children; }
}
export default function App() {
 const path = usePathname();
 const { state, preferences, connection, probing, notice, setNotice, updatePreferences, refreshState } = useDesktop();
 const [palette, setPalette] = useState(false);
 const [mobileOpen, setMobileOpen] = useState(false);
 const content = useRef<HTMLElement>(null);
 const commandTrigger = useRef<HTMLButtonElement>(null);
 const [commandIndex, setCommandIndex] = useState(0);
 const [query, setQuery] = useState('');
 const [exporting, setExporting] = useState(false);
 const Page = routePage(path);
 const commands = useMemo(() => actions.filter(item => item.name.toLowerCase().includes(query.trim().toLowerCase())), [query]);
 useEffect(() => { setCommandIndex(0); }, [query, palette]);
 useEffect(() => { if (palette) document.getElementById('desktop-command-' + commandIndex)?.scrollIntoView({ block: 'nearest' }); }, [palette, commandIndex]);
 useEffect(() => {
  const trigger = commandTrigger.current;
  if (palette) return () => trigger?.focus();
 }, [palette]);
 useEffect(() => { updatePreferences({ last_path: startupPath(path, window.location.search) }); }, [path, updatePreferences]);
 useEffect(() => { setMobileOpen(false); content.current?.scrollTo({ top: 0, left: 0, behavior: 'instant' }); }, [path]);
 useEffect(() => {
  // Effects run after DOM commit; hidden-window smoke tests cannot rely on rAF.
  void nativeRequest('ready', { bundled_workbench: document.querySelector('[data-bundled-workbench]') !== null, navigation_items: document.querySelectorAll('[data-desktop-nav]').length, route: window.location.pathname, viewport_width: window.innerWidth, viewport_height: window.innerHeight, device_pixel_ratio: window.devicePixelRatio }).catch(() => {});
 }, []);
 useEffect(() => {
  const handler = (event: KeyboardEvent) => {
   if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === 'k') { event.preventDefault(); setPalette(value => !value); }
   if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'n') { event.preventDefault(); navigate(event.shiftKey ? '/problem-sets/new' : '/problems/new'); }
   if ((event.ctrlKey || event.metaKey) && event.key === ',') { event.preventDefault(); navigate('/desktop/settings'); }
   if (event.key === 'Escape') { setPalette(false); setMobileOpen(false); }
  };
  window.addEventListener('keydown', handler);
  return () => window.removeEventListener('keydown', handler);
 }, []);
 useEffect(() => {
  if (!notice) return;
  const id = window.setTimeout(() => setNotice(''), 6000); return () => window.clearTimeout(id);
 }, [notice, setNotice]);
 useEffect(() => {
  const handler = (event: MouseEvent) => {
   const anchor = (event.target as HTMLElement).closest?.('a[href]') as HTMLAnchorElement | null;
   if (!anchor || event.button !== 0) return;
   const url = new URL(anchor.href, window.location.href);
   const prefix = boot().api_base;
   if (url.origin === window.location.origin && url.pathname.startsWith(prefix + '/api/v1/')) {
    event.preventDefault(); event.stopPropagation();
    const remotePath = url.pathname.slice(prefix.length) + url.search;
    const tail = url.pathname.split('/').pop() || 'export.zip';
    const suggested = tail === 'export' ? 'quizzes.xlsx' : tail === 'input' ? 'testcase.in' : tail === 'output' ? 'testcase.out' : tail;
    const name = anchor.download || 'Qraft-' + suggested;
    setExporting(true);
    void nativeRequest<ExportRecord>('download', { path: remotePath, name }).then(result => {
     if (result.status !== 'cancelled') { setNotice('已保存：' + result.name); navigate('/desktop/exports'); }
    }).catch(error => setNotice(error.message)).finally(() => setExporting(false));
   } else if (url.origin !== window.location.origin && ['http:', 'https:'].includes(url.protocol)) {
    event.preventDefault(); event.stopPropagation();
    void nativeRequest('open-external', { url: url.href }).catch(error => setNotice(error.message));
   }
  };
  document.addEventListener('click', handler, true);
  return () => document.removeEventListener('click', handler, true);
 }, [setNotice]);
 useEffect(() => {
  const exported = (event: Event) => {
   const item = (event as CustomEvent<ExportRecord>).detail;
   setNotice('已保存：' + item.name); navigate('/desktop/exports');
  };
  window.addEventListener('algoforge:exported', exported);
  return () => window.removeEventListener('algoforge:exported', exported);
 }, [setNotice]);
 if (!state.configured) return <Welcome />;
 function select(to: string) { setPalette(false); setQuery(''); navigate(to); }
 return <div className={'af-shell af-desktop font-sans' + (preferences.sidebar_collapsed ? ' is-collapsed' : '') + (mobileOpen ? ' is-mobile-open' : '')} data-bundled-workbench="true">
  {mobileOpen && <button type="button" aria-label="关闭导航" className="af-sidebar-backdrop" onClick={() => setMobileOpen(false)} />}
  <Sidebar desktop collapsed={preferences.sidebar_collapsed} onCollapsedChange={value => { void updatePreferences({ sidebar_collapsed: value }); }} onNavigate={() => setMobileOpen(false)}
   footerExtra={<div className="af-client-links"><Link href="/desktop/exports" title="导出记录"><Download size={15} /><span>导出记录</span></Link><Link href="/desktop/settings" title="客户端设置"><Settings2 size={15} /><span>客户端设置</span></Link></div>} />
  <div className="af-main">
   <Header appearance={{ mode: preferences.theme, colorTheme: preferences.color_theme ?? 'graphite' }} onAppearanceChange={patch => { void updatePreferences({ ...(patch.mode ? { theme: patch.mode } : {}), ...(patch.colorTheme ? { color_theme: patch.colorTheme } : {}) }); }} storageError={false} onMenu={() => setMobileOpen(value => !value)} menuOpen={mobileOpen}
    themePicker={<ThemeQuickPicker />}
    actions={<button type="button" className="af-icon-button" title="导出记录" aria-label="查看导出记录" onClick={() => navigate('/desktop/exports')}>{exporting ? <Loader2 size={18} className="animate-spin" /> : <Download size={18} />}</button>} />
   <main ref={content} id="workspace-content" className="af-content scrollbar-thin" data-workbench-content="true">
    {!state.service_url && !path.startsWith('/desktop/') && <section className="desktop-service-notice" role="status" aria-label="服务连接提示">
      <div><strong>{state.operation.busy && ['start', 'sync'].includes(state.operation.action) ? '本机后端正在启动' : '服务尚未就绪'}</strong><p>你可以继续浏览页面和填写需求；读取题库、保存配置和提交任务需要先连接服务。</p></div>
      <div className="desktop-form-actions"><Link href={'/desktop/settings?tab=' + (state.config.mode === 'local' ? 'local' : 'connection')} className="desktop-subtle-button">{state.config.mode === 'local' ? '前往本地后端' : '连接已有服务'}</Link><button type="button" className="desktop-subtle-button" disabled={state.operation.busy} onClick={() => { void refreshState().catch(error => setNotice(error.message)); }}>刷新连接状态</button></div>
     </section>}
     <PageBoundary key={path}><Suspense fallback={<div className="desktop-empty large"><Loader2 size={26} className="animate-spin" /><strong>正在打开页面…</strong></div>}>
     {path === '/' ? <Home /> : path === '/desktop/settings' ? <Settings /> : path === '/desktop/exports' ? <Exports /> : Page ? <Page /> : <div className="desktop-empty large"><HelpCircle size={30} /><strong>没有这个页面</strong><Link href="/" className="desktop-primary-button">返回工作台</Link></div>}
    </Suspense></PageBoundary>
   </main>
   <footer className="desktop-statusbar"><button onClick={() => navigate('/desktop/settings')}><i className={connection?.ready ? 'connected' : probing ? 'pending' : ''} />{state.operation.busy ? (state.operation.action.startsWith('update-') ? '客户端更新中' : '本地服务操作中') : connection?.ready ? '服务已连接' : probing ? '正在连接服务' : '服务未连接'}{connection?.release_version && <span>v{connection.release_version}</span>}</button><span className="desktop-statusbar-hint">{state.operation.busy ? state.operation.message.split('\n')[0] : (state.config.mode === 'local' && !state.service_url ? '本机后端尚未启动，请完成部署设置' : '生成任务由工作区持续运行')}</span><button ref={commandTrigger} title="快捷操作 · Ctrl Shift K" onClick={() => setPalette(true)}><Command size={12} />快捷操作</button></footer>
  </div>
  {notice && <div role="status" className="desktop-notice"><Check size={17} /><span>{notice}</span><button aria-label="关闭提示" onClick={() => setNotice('')}><X size={16} /></button></div>}
  {palette && <div className="desktop-palette-overlay" onMouseDown={event => { if (event.target === event.currentTarget) setPalette(false); }}><section role="dialog" aria-modal="true" aria-label="快捷操作" className="desktop-palette" onKeyDown={event => {
   if (event.key !== 'Tab') return;
   const controls = event.currentTarget.querySelectorAll<HTMLElement>('input,button:not([tabindex="-1"])');
   const first = controls[0], last = controls[controls.length - 1];
   if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
   else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }}><div><Search size={19} /><input role="combobox" aria-expanded="true" aria-controls="desktop-command-list" aria-autocomplete="list" aria-activedescendant={commands.length ? 'desktop-command-' + commandIndex : undefined} aria-label="搜索功能" autoFocus value={query} onChange={e => setQuery(e.target.value)} placeholder="搜索页面或操作…" onKeyDown={e => {
   if (e.key === 'ArrowDown' && commands.length) { e.preventDefault(); setCommandIndex(value => (value + 1) % commands.length); }
   if (e.key === 'ArrowUp' && commands.length) { e.preventDefault(); setCommandIndex(value => (value + commands.length - 1) % commands.length); }
   if (e.key === 'Enter' && commands[commandIndex]) { e.preventDefault(); select(commands[commandIndex].to); }
  }} /><button aria-label="关闭快捷操作" onClick={() => setPalette(false)}><X size={17} /></button></div><nav id="desktop-command-list" role="listbox" aria-label="功能列表">{commands.length ? commands.map(({ to, name, icon: Icon }, index) => <button id={'desktop-command-' + index} role="option" tabIndex={-1} aria-selected={commandIndex === index} key={to} onMouseMove={() => setCommandIndex(index)} onClick={() => select(to)}><Icon size={17} /><span>{name}</span><ArrowUpRight size={15} /></button>) : <p>没有匹配的功能</p>}</nav><footer><span><kbd>↑</kbd><kbd>↓</kbd>选择</span><span><kbd>Enter</kbd>打开</span><span><kbd>Esc</kbd>关闭</span></footer></section></div>}
  <NotificationToaster />
 </div>;
}
