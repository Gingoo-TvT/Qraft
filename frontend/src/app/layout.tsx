import type { Metadata } from 'next';
import './globals.css';
import '@/styles/home.css';
import '@/styles/detail.css';
import AppShell from '@/components/layout/AppShell';

// ---------------------------------------------------------------------------
// Metadata
// ---------------------------------------------------------------------------

export const metadata: Metadata = {
  title: {
    default: 'Qraft · 题构',
    template: '%s | Qraft · 题构',
  },
  description: '算法竞赛自动出题平台',
  icons: {
    icon: '/qraft.svg',
  },
};

// ---------------------------------------------------------------------------
// Root Layout
// ---------------------------------------------------------------------------

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <body className="min-h-screen font-sans">
        <AppShell>{children}</AppShell>
      </body>
    </html>
  );
}
