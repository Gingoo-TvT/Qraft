'use client';
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { getRatingWorkspace } from '@/lib/rating-api';
import SourceDifficulty from './SourceDifficulty';
import type { SourceReference, OfficialRating } from '@/lib/rating-types';
export default function ProblemRating({ problemID, target }: { problemID: string; target: number }) {
 const [source, setSource] = useState<SourceReference>();
 const [available, setAvailable] = useState(false);
 const [official, setOfficial] = useState<OfficialRating | null>(null);
 useEffect(() => {
  let active = true; setAvailable(false);
  void getRatingWorkspace(problemID).then(result => { if (active) { setOfficial(result.official ?? null); setSource(result.subject.source_reference); setAvailable(true); } }).catch(() => { if (active) setOfficial(null); });
  return () => { active = false; };
 }, [problemID]);
 if (!available) return null;
 return <div className="space-y-3"><SourceDifficulty reference={source} /><div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border border-[var(--dl)] bg-[var(--ds)] px-4 py-3 text-sm">
  <span>目标难度 <strong>{target}</strong></span><span>正式 rating <strong>{official ? official.rating : '暂定'}</strong>{official?.stale && <span className="ml-1 text-[var(--dm)]">（旧版本，待复核）</span>}</span><Link className="af-link" href={'/rating?problem=' + encodeURIComponent(problemID)}>查看评估与依据</Link>
 </div></div>;
}
