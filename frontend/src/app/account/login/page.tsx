'use client';
import Link from 'next/link';
import SignIn from '@/components/auth/SignIn';
import { useAuth } from '@/components/auth/AuthProvider';
export default function LoginPage() {
 const { session, checking, error, refresh } = useAuth();
 if (checking) return <p role="status" className="p-8">正在检查账号…</p>;
 if (error) return <div className="p-8"><p role="alert">{error}</p><button className="forge-btn-secondary" onClick={() => void refresh()}>重试</button></div>;
 if (session?.authenticated || session?.mode === 'local') return <section className="mx-auto my-10 max-w-md space-y-4 p-6"><h1 className="text-xl font-semibold">工作区已就绪</h1><Link className="forge-btn-primary" href="/">进入工作台</Link></section>;
 return <SignIn />;
}
