'use client';

import type { ReactNode } from 'react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { BookMarked, ChevronDown, ChevronsLeft, ChevronsRight, Cpu, Database, FileText, GitBranch, Layers3, LayoutDashboard, ListChecks, Plus, Settings2, ShieldAlert, SlidersHorizontal, Sparkles, Trophy, Upload, type LucideIcon } from 'lucide-react';
import manifest from '../../../package.json';

type NavItem = { label:string; href:string; icon:LucideIcon };
const HOME: NavItem[] = [{ label:'工作台', href:'/', icon:LayoutDashboard }];
const CREATE: NavItem[] = [
 { label:'创建题目', href:'/problems/new', icon:Plus },
 { label:'生成题集', href:'/problem-sets/new', icon:Sparkles },
 { label:'题库组卷', href:'/problem-sets/assemble', icon:Layers3 },
];
const LIBRARY: NavItem[] = [
 { label:'编程题库', href:'/problems', icon:FileText },
 { label:'客观题库', href:'/quizzes', icon:ListChecks },
 { label:'题集管理', href:'/problem-sets', icon:BookMarked },
];
const TASKS: NavItem[] = [{ label:'任务中心', href:'/workflows', icon:GitBranch }];
const TOOLS: NavItem[] = [
 { label:'客观题生成', href:'/quizzes/new', icon:Plus },
 { label:'Excel 导入', href:'/quizzes/import', icon:Upload },
 { label:'Hydro 校验', href:'/problems/hydro/import', icon:Upload },
 { label:'知识点目录', href:'/knowledge-points', icon:BookMarked },
 { label:'测试数据', href:'/testdata-config', icon:Database },
 { label:'隔离区', href:'/problems/quarantine', icon:ShieldAlert },
 { label:'天梯赛专项', href:'/problems/gplt', icon:Trophy },
];
const SETTINGS: NavItem[] = [
 { label:'模型配置', href:'/settings', icon:Settings2 },
 { label:'去重服务', href:'/embedding', icon:Cpu },
];
const ALL = [...HOME,...CREATE,...LIBRARY,...TASKS,...TOOLS,...SETTINGS];
export default function Sidebar({ collapsed, onCollapsedChange, onNavigate, desktop = false, footerExtra }: {
 collapsed:boolean; onCollapsedChange:(collapsed:boolean)=>void; onNavigate:()=>void; desktop?:boolean; footerExtra?:ReactNode;
}) {
 const pathname = usePathname();
 const match = ALL.filter(item => item.href === '/' ? pathname === '/' : pathname === item.href || pathname.startsWith(item.href + '/')).sort((a,b)=>b.href.length-a.href.length)[0]?.href;
 function links(items:NavItem[]) {
  return items.map(({href,label,icon:Icon}) => <Link key={href} href={href} className={'af-nav-item' + (match === href ? ' active' : '')}
   data-desktop-nav={desktop ? 'true' : undefined} aria-label={label} aria-current={match === href ? 'page' : undefined} title={collapsed ? label : undefined} onClick={onNavigate}>
   <Icon size={18} strokeWidth={1.7} /><span>{label}</span>
  </Link>);
 }
 return <aside id="workspace-sidebar" className="af-sidebar">
  <Link href="/" className="af-brand" aria-label="Qraft 工作台" onClick={onNavigate}><span className="af-brand-mark"><Layers3 size={21} strokeWidth={1.8} /></span>
   <span className="af-brand-copy"><strong>Qraft</strong><small>题构 · 算法创作工作区</small></span></Link>
  <nav aria-label="主导航" className="af-sidebar-nav scrollbar-thin">
   <div className="af-nav-group">{links(HOME)}</div>
   <div className="af-nav-group"><p className="af-nav-label">创作</p>{links(CREATE)}</div>
   <div className="af-nav-group"><p className="af-nav-label">内容库</p>{links(LIBRARY)}</div>
   <div className="af-nav-group">{links(TASKS)}</div>
   <details className="af-nav-tools" key={TOOLS.some(item=>item.href===match) ? 'active-tool' : 'inactive-tool'} open={TOOLS.some(item=>item.href===match) || undefined}>
    <summary aria-label="更多工具" title={collapsed ? '更多工具' : undefined}><SlidersHorizontal size={18} strokeWidth={1.7} /><span>更多工具</span><ChevronDown size={14} /></summary>
    <div className="af-nav-tool-list">{links(TOOLS)}</div>
   </details>
  </nav>
  <footer className="af-sidebar-bottom"><nav aria-label="工作区设置">{links(SETTINGS)}</nav>
   {footerExtra}
   <div className="af-sidebar-meta"><span>v{manifest.version}</span><button type="button" className="af-icon-button" aria-label={collapsed ? '展开侧栏' : '收起侧栏'} title={collapsed ? '展开侧栏' : '收起侧栏'} onClick={()=>onCollapsedChange(!collapsed)}>
    {collapsed ? <ChevronsRight size={17} /> : <ChevronsLeft size={17} />}</button></div>
  </footer>
 </aside>;
}
