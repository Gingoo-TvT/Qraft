'use client';

import { Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import Link from 'next/link';
import { ChevronRight, Menu, Search } from 'lucide-react';
import WebThemePicker from './WebThemePicker';
import type { WebAppearance } from '@/lib/web-appearance';
import { hasQuestionSearch, questionSearchHref } from '@/lib/question-search-route';

export type ThemePreference = WebAppearance['mode'];
const LABELS: Record<string,string> = {
 problems:'编程题库', quizzes:'客观题库', 'problem-sets':'题集管理', workflows:'任务中心',
 settings:'模型配置', embedding:'去重服务', 'knowledge-points':'知识点',
 desktop:'客户端', exports:'导出记录', search:'搜索题目',
 'testdata-config':'测试数据', quarantine:'隔离区', assemble:'题库组卷', new:'新建', edit:'编辑', import:'导入', hydro:'Hydro', gplt:'天梯赛专项',
};
function QuestionSearch({ pathname }: { pathname: string }) {
 const router = useRouter(), params = useSearchParams();
 const query = pathname === '/search' ? params.get('q') ?? '' : '';
 const [search, setSearch] = useState(query);
 const searchInput = useRef<HTMLInputElement>(null);
 useEffect(() => { setSearch(query); }, [pathname, query]);
 useEffect(() => {
  const focusSearch = (event: KeyboardEvent) => {
   if (!event.defaultPrevented && (event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k' && searchInput.current?.getClientRects().length) {
    event.preventDefault();
    searchInput.current.focus();
   }
  };
  document.addEventListener('keydown', focusSearch);
  return () => document.removeEventListener('keydown', focusSearch);
 }, []);
 function submit(event: React.FormEvent) {
  event.preventDefault();
  router.push(questionSearchHref(pathname, params.toString(), search));
 }
 return <>
  <form className="af-search" role="search" onSubmit={submit}><Search size={16} aria-hidden="true" />
   <input ref={searchInput} aria-label="搜索题目" maxLength={200} placeholder="搜索题目…" value={search} onChange={event => setSearch(event.target.value)} />
   <kbd aria-hidden="true">Ctrl K</kbd>
  </form>
  <span className="af-topbar-divider" />
 </>;
}
export default function Header({ appearance, onAppearanceChange, storageError, onMenu, menuOpen, themePicker, actions }: {
 appearance: WebAppearance; onAppearanceChange: (patch: Partial<WebAppearance>) => void; storageError: boolean; onMenu: () => void; menuOpen: boolean; themePicker?:ReactNode; actions?:ReactNode;
}) {
 const pathname = usePathname();
 const crumbs = useMemo(() => {
  let path = '';
  return [{ label:'工作台', href:'/' }, ...pathname.split('/').filter(Boolean).map(segment => {
   path += '/' + segment;
   let label = LABELS[segment] ?? '详情';
   if (path === '/problems/new') label = '创建题目';
   if (path === '/problem-sets/new') label = '生成题集';
   if (path === '/desktop/settings') label = '客户端设置';
   return { label, href:path };
  })];
 }, [pathname]);
 return <header className="af-topbar">
  <button type="button" className="af-icon-button af-mobile-menu" aria-label="打开导航" aria-controls="workspace-sidebar" aria-expanded={menuOpen} onClick={onMenu}><Menu size={20} /></button>
  <nav aria-label="当前位置" className="af-breadcrumb">{crumbs.map((crumb,index) =>
   <span key={crumb.href}>{index > 0 && <ChevronRight size={14} aria-hidden="true" />}
    {index === crumbs.length - 1 ? <strong aria-current="page">{crumb.label}</strong> : crumb.href === '/desktop' ? <span>{crumb.label}</span> : <Link href={crumb.href}>{crumb.label}</Link>}
   </span>)}</nav>
  <div className="af-topbar-actions">
   {hasQuestionSearch(pathname) && <Suspense fallback={null}><QuestionSearch pathname={pathname} /></Suspense>}
   {themePicker ?? <WebThemePicker appearance={appearance} onChange={onAppearanceChange} storageError={storageError} />}
   {actions}
  </div>
 </header>;
}
