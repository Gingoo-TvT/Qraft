import { desktopRuntime } from './desktop-runtime';

// Local configuration exports use the same native Save As flow as API exports.
export async function saveTextFile(content: string, name: string): Promise<void> {
 const desktop = desktopRuntime();
 if (desktop) {
  const response = await fetch(desktop.base + '/native/save-text', {
   method: 'POST', headers: { 'Content-Type': 'application/json' },
   body: JSON.stringify({ name, content }),
  });
  const result = await response.json();
  if (!response.ok || !result.success) throw new Error(result.error?.message ?? '保存文件失败');
  if (result.data.status !== 'cancelled') window.dispatchEvent(new CustomEvent('algoforge:exported', { detail: result.data }));
  return;
 }
 const url = URL.createObjectURL(new Blob([content], { type: 'application/json' }));
 const anchor = document.createElement('a');
 anchor.href = url; anchor.download = name;
 document.body.appendChild(anchor); anchor.click(); anchor.remove();
 URL.revokeObjectURL(url);
}
