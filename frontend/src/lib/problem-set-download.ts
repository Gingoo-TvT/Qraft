import { APIError, exportProblemSetTestingURL } from './api';
import { currentSession, serviceFetch } from './auth-session';
import { desktopRuntime } from './desktop-runtime';

export function parseExportTagCatalog(text: string): { activeTagsTree: unknown[] } {
 const value: unknown = JSON.parse(text);
 if (!value || typeof value !== 'object' || !('activeTagsTree' in value) || !Array.isArray(value.activeTagsTree)) {
  throw new Error('请选择含 activeTagsTree 的标签目录 JSON。');
 }
 // Send only the active tree, not the full deleted-tag dump or database metadata.
 const result = { activeTagsTree: value.activeTagsTree };
 if (new TextEncoder().encode(JSON.stringify({ tag_catalog: result })).length > 900 * 1024) {
  throw new Error('有效标签目录过大，最多支持 900 KiB。');
 }
 return result;
}

export interface ExportNumbering { prefix: string; start: number }

export function exportNumberingQuery(numbering?: ExportNumbering): string {
 if (!numbering) return '';
 if (!/^[A-Za-z0-9_-]{0,24}$/.test(numbering.prefix) || !Number.isSafeInteger(numbering.start) || numbering.start < 1 || numbering.start > 999999) {
  throw new Error('编号前缀最多 24 位，仅支持英文字母、数字、下划线和连字符；起始编号须为 1 到 999999 的整数。');
 }
 return '&id_prefix=' + encodeURIComponent(numbering.prefix) + '&start_index=' + numbering.start;
}

export async function downloadProblemSetTesting(id: string, format: 'generic' | 'hydro', name: string, tagCatalog?: { activeTagsTree: unknown[] }, numbering?: ExportNumbering): Promise<void> {
 const payload = format === 'generic' && tagCatalog ? { tag_catalog: tagCatalog } : undefined;
 const numberingQuery = exportNumberingQuery(numbering);
 const desktop = desktopRuntime();
 if (desktop) {
  const response = await fetch(desktop.base + '/native/download', {
   method: 'POST',
   headers: { 'Content-Type': 'application/json', 'X-Qraft-Service': desktop.state.service_url, 'X-CSRF-Token': currentSession()?.csrf_token ?? '' },
   body: JSON.stringify({ path: '/api/v1/problem-sets/' + encodeURIComponent(id) + '/export.zip?mode=testing&format=' + format + numberingQuery, name, ...(payload ? { body: payload } : {}) }),
  });
  const body = await response.json();
  if (!response.ok || !body.success) throw new Error(body.error?.message ?? 'ZIP 保存失败；带标签的导出需要更新桌面客户端。');
  if (body.data?.status !== 'cancelled') window.dispatchEvent(new CustomEvent('algoforge:exported', { detail: body.data }));
  return;
 }
 const response = await serviceFetch(exportProblemSetTestingURL(id, format) + numberingQuery, payload ? {
  method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
 } : {});
 if (!response.ok) {
  const body = await response.json().catch(() => null);
  throw new APIError(body?.error?.message ?? 'ZIP 下载失败，请稍后重试。', body?.error?.code ?? 'EXPORT_ERROR', response.status);
 }
 if (!response.headers.get('content-type')?.includes('application/zip')) throw new Error('服务没有返回 ZIP，请检查连接与登录状态。');
 const url = URL.createObjectURL(await response.blob());
 const anchor = document.createElement('a');
 anchor.href = url;
 anchor.download = name;
 document.body.appendChild(anchor);
 anchor.click();
 anchor.remove();
 window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
