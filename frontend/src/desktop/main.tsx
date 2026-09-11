import React from 'react';
import { createRoot } from 'react-dom/client';
import { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';
import JSONWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker';
import TSWorker from 'monaco-editor/esm/vs/language/typescript/ts.worker?worker';
import '@/app/globals.css';
import 'katex/dist/katex.min.css';
import './styles.css';
import '@/styles/home.css';
import '@/styles/detail.css';
import './themes.css';
import './shared-shell.css';
import type { DesktopTheme } from '@/lib/desktop-runtime';
import { useAppStore } from '@/stores/appStore';
import { applyTheme, mixColors } from './themes';
import { boot, DesktopProvider } from './runtime';
import { navigate } from './router';
import App from './App';

self.MonacoEnvironment = {
 getWorker(_module: string, label: string) {
  if (label === 'json') return new JSONWorker();
  if (label === 'typescript' || label === 'javascript') return new TSWorker();
  return new EditorWorker();
 },
};
loader.config({ monaco });
function updateEditorThemes(theme: DesktopTheme) {
 for (const mode of ['light', 'dark'] as const) {
  const c = theme.colors[mode];
  monaco.editor.defineTheme('algoforge-' + mode, {
   base: mode === 'dark' ? 'vs-dark' : 'vs', inherit: true, rules: [],
   colors: {
    'editor.background': c.surface, 'editor.foreground': c.text,
    'editorLineNumber.foreground': c.muted, 'editorLineNumber.activeForeground': c.text,
    'editorCursor.foreground': c.accent, 'editor.selectionBackground': mixColors(c.surface, c.accent, 0.24),
    'editor.lineHighlightBackground': mixColors(c.surface, c.text, 0.04),
    'editor.inactiveSelectionBackground': mixColors(c.surface, c.accent, 0.12),
    'editorIndentGuide.background1': c.border, 'editorIndentGuide.activeBackground1': c.muted,
    'editorWidget.background': c.surface, 'editorWidget.foreground': c.text, 'editorWidget.border': c.border,
    'editorSuggestWidget.background': c.surface, 'editorSuggestWidget.foreground': c.text,
    'editorSuggestWidget.border': c.border, 'editorSuggestWidget.selectedBackground': mixColors(c.surface, c.accent, 0.14),
    'minimap.background': c.surface, 'focusBorder': c.accent,
   },
  });
 }
}
window.addEventListener('algoforge:theme', event => {
 const { theme, dark } = (event as CustomEvent<{ theme: DesktopTheme; dark: boolean }>).detail;
 updateEditorThemes(theme);
 monaco.editor.setTheme(dark ? 'algoforge-dark' : 'algoforge-light');
});
const initial = boot();
useAppStore.setState({ darkMode: applyTheme(initial.preferences, window.matchMedia('(prefers-color-scheme: dark)').matches) });
if (window.location.pathname === initial.ui_base + '/' && initial.preferences.last_path !== '/') {
 navigate(initial.preferences.last_path, true);
}
createRoot(document.getElementById('root')!).render(<DesktopProvider><App /></DesktopProvider>);
