'use client';
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { getGenerationReadiness } from '@/lib/api';
import { useAuth } from './AuthProvider';
export default function GenerationReadiness() {
 const { isAdmin } = useAuth();
 const [message, setMessage] = useState('');
 useEffect(() => {
  let active = true;
  void getGenerationReadiness().then(result => { if (active) setMessage(result.data?.ready ? '' : result.data?.message || '生成服务尚未准备好，请联系管理员。'); }).catch(() => { /* The health banner and submission retain actionable errors for older services. */ });
  return () => { active = false; };
 }, []);
 return message ? <p role="status" className="rounded-lg border border-[var(--dl)] p-4 text-sm">{message}{isAdmin && <Link className="af-link ml-3" href="/settings">管理模型配置</Link>}</p> : null;
}
