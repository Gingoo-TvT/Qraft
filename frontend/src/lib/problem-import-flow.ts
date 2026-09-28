import type { SourceDifficulty } from './rating-types';
import type { ImportInputItem, SourceDocument } from './problem-import-api';

export type SourceCandidate = ImportInputItem & { selected: boolean; error?: string; difficulty?: SourceDifficulty };
type Preview = (url: string) => Promise<SourceDocument>;
export const MAX_IMPORT_ITEMS = 50;

export function parseSourceURLs(text: string): string[] {
 const lines = text.split(/\r?\n/).map(line => line.trim()).filter(Boolean);
 if (!lines.length) throw new Error('请填写至少一个公开题目或题集链接。');
 if (lines.length > MAX_IMPORT_ITEMS) throw new Error('每次最多解析 50 个链接。');
 return lines.map(line => {
  let url: URL;
  try { url = new URL(line); } catch { throw new Error('链接格式不正确：' + line); }
  if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) throw new Error('只支持不含登录凭据的 HTTP/HTTPS 链接。');
  return url.href;
 });
}

async function limitedMap<T, U>(values: T[], fn: (value: T, index: number) => Promise<U>): Promise<U[]> {
 const output: U[] = [];
 for (let offset = 0; offset < values.length; offset += 2) {
  output.push(...await Promise.all(values.slice(offset, offset + 2).map((value, index) => fn(value, offset + index))));
 }
 return output;
}

export async function previewSources(urls: string[], preview: Preview): Promise<{ items: SourceCandidate[]; warnings: string[] }> {
 const warnings: string[] = [];
 const groups = await limitedMap(urls, async (url, index) => {
  try {
   const document = await preview(url);
   warnings.push(...(document.warnings ?? []));
   if (!document.items?.length) throw new Error('没有识别到题面或题目链接，可改用粘贴题面。');
   return document.items.map((item, child) => ({
    item_id: 'source-' + index + '-' + child, title: item.title || document.title || '未命名题目',
    statement: item.statement ?? '', source_url: item.url || document.final_url || url,
    source_id: item.id, difficulty: item.difficulty, selected: true,
   }));
  } catch (error) {
   return [{ item_id: 'source-' + index, title: url, statement: '', source_url: url, selected: false, error: error instanceof Error ? error.message : '链接读取失败' }];
  }
 });
 const items = groups.flat();
 if (items.length > MAX_IMPORT_ITEMS) {
  warnings.push('识别到 ' + items.length + ' 项。本次保留前 50 项，请拆分其余链接后继续。');
  return { items: items.slice(0, MAX_IMPORT_ITEMS), warnings };
 }
 return { items, warnings };
}

export async function resolveSelectedSources(candidates: SourceCandidate[], preview: Preview): Promise<{ items: ImportInputItem[]; failures: { item_id: string; error: string }[] }> {
 const selected = candidates.filter(item => item.selected);
 if (!selected.length || selected.length > MAX_IMPORT_ITEMS) throw new Error('请选择 1–50 道题目。');
 const results = await limitedMap(selected, async candidate => {
  try {
   let item = candidate;
   if (!item.statement.trim()) {
    if (!item.source_url) throw new Error('题面为空，请补充题面。');
    const document = await preview(item.source_url);
    if (document.kind === 'collection' || document.items.length !== 1 || !document.items[0].statement?.trim()) throw new Error('该链接未返回单道题面，请粘贴题面后重试。');
    const source = document.items[0];
    item = { ...item, title: source.title || document.title || item.title, statement: source.statement!, source_url: source.url || document.final_url || item.source_url, source_id: source.id };
   }
   const bytes = new TextEncoder().encode(item.statement).length;
   if (bytes > 128 * 1024) throw new Error('单题题面超过 128 KiB，请删除网页导航等无关内容后粘贴。');
   if (Array.from(item.title).length > 200) throw new Error('标题过长，请修正后重试。');
   const { item_id, title, statement, source_url, source_id } = item;
   return { item: { item_id, title, statement, source_url, source_id } };
  } catch (error) {
   return { failure: { item_id: candidate.item_id, error: error instanceof Error ? error.message : '题面读取失败' } };
  }
 });
 const items: ImportInputItem[] = [];
 const failures = results.flatMap(result => result.failure ? [result.failure] : []);
 let bytes = 0;
 for (const result of results) {
  if (!result.item) continue;
  const size = new TextEncoder().encode(result.item.statement).length;
  if (bytes + size > 1024 * 1024) {
   failures.push({ item_id: result.item.item_id, error: '本批题面总量超过 1 MiB，此项请单独提交。' });
   continue;
  }
  bytes += size;
  items.push(result.item);
 }
 return { items, failures };
}
