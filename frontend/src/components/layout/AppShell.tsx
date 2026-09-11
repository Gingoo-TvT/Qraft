'use client';

import { useEffect, useState } from 'react';
import { usePathname } from 'next/navigation';
import Header from '@/components/layout/Header';
import { NotificationToaster } from '@/components/layout/NotificationToaster';
import Sidebar from '@/components/layout/Sidebar';
import { useWebAppearance } from '@/lib/web-appearance';

export default function AppShell({ children }: { children: React.ReactNode }) {
 const pathname = usePathname();
 const [collapsed, setCollapsed] = useState(false);
 const [mobileOpen, setMobileOpen] = useState(false);
 const { appearance, changeAppearance, storageError } = useWebAppearance();
 useEffect(() => {
  try { setCollapsed(localStorage.getItem('algoforge_sidebar_collapsed') === 'true'); }
  catch { /* The sidebar remains usable without browser storage. */ }
 }, []);
 useEffect(() => { setMobileOpen(false); }, [pathname]);
 useEffect(() => {
  if (!mobileOpen) return;
  const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setMobileOpen(false); };
  document.addEventListener('keydown', escape);
  return () => document.removeEventListener('keydown', escape);
 }, [mobileOpen]);
 function changeCollapsed(value: boolean) {
  setCollapsed(value);
  try { localStorage.setItem('algoforge_sidebar_collapsed', String(value)); } catch { /* Session preference only. */ }
 }
 if (pathname === '/annotate' || pathname.startsWith('/annotate/')) return <div className="af-standalone">{children}</div>;
 return <>
  <div className={'af-shell' + (collapsed ? ' is-collapsed' : '') + (mobileOpen ? ' is-mobile-open' : '')}>
   {mobileOpen && <button type="button" aria-label="关闭导航" className="af-sidebar-backdrop" onClick={() => setMobileOpen(false)} />}
   <Sidebar collapsed={collapsed} onCollapsedChange={changeCollapsed} onNavigate={() => setMobileOpen(false)} />
   <div className="af-main"><Header appearance={appearance} onAppearanceChange={changeAppearance} storageError={storageError} onMenu={() => setMobileOpen(!mobileOpen)} menuOpen={mobileOpen} />
    <main id="workspace-content" className="af-content scrollbar-thin">{children}</main>
   </div>
  </div><NotificationToaster />
 </>;
}
