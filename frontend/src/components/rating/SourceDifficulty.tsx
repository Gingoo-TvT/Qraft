import type { SourceReference } from '@/lib/rating-types';
import { dateLabel } from './Primitives';

export default function SourceDifficulty({ reference }: { reference?: SourceReference }) {
 if (!reference) return null;
 const source = reference.difficulty;
 const verified = reference.status === 'verified';
 const url = source?.source_url;
 const safeURL = url && /^https:\/\//i.test(url) ? url : undefined;
 const platforms: Record<string, string> = { codeforces: 'Codeforces', luogu: '洛谷', nowcoder: '牛客' };
 return <div className="rounded-lg border border-[var(--dl)] bg-[var(--ds)] p-4 text-sm" aria-label="来源难度依据">
  <p className="font-medium">来源难度：{source ? (platforms[source.platform] ?? source.platform) + ' · ' + source.label : '未取得'}{source && !verified ? '（待重新核对）' : ''}</p>
  {source && <p className="mt-1 text-xs text-[var(--dm)]">{verified ? '已核对原题' : '未作为评分依据'} · 抓取于 {dateLabel(source.fetched_at)}{safeURL && <> · <a className="af-link" href={safeURL} target="_blank" rel="noreferrer">查看原站</a></>}</p>}
  <p className="mt-2 text-xs text-[var(--dm)]">{reference.reason}{source && source.scale !== 'codeforces_rating' ? ' 原站等级单独保留，不直接换算成 CF 分数。' : ''} 人工评价和管理员正式评级独立保留。</p>
 </div>;
}
