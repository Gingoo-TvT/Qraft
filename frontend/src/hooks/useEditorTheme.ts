'use client';

import { useCallback, useEffect, useRef } from 'react';
import type { Monaco } from '@monaco-editor/react';
import { mixColors } from '@/desktop/themes';

const THEME_ID = 'algoforge-workspace';
const registered = new WeakMap<Monaco, string>();

function applyEditorTheme(monaco: Monaco) {
 const dark = document.documentElement.classList.contains('dark');
 const style = getComputedStyle(document.documentElement);
 const read = (name: string, fallback: string) => {
  const value = style.getPropertyValue(name).trim();
  return /^#[0-9a-f]{6}$/i.test(value) ? value : fallback;
 };
 const surface = read('--dp', dark ? '#1A2636' : '#FFFFFF');
 const text = read('--dt', dark ? '#E8F0FA' : '#1C2B40');
 const muted = read('--dm', dark ? '#A2B5CF' : '#596C83');
 const border = read('--dl', dark ? '#314259' : '#DDE5EF');
 const accent = read('--da', dark ? '#86B8EE' : '#2563A8');
 const key = [dark, surface, text, muted, border, accent].join('|');
 if (registered.get(monaco) !== key) {
  monaco.editor.defineTheme(THEME_ID, {
   base: dark ? 'vs-dark' : 'vs', inherit: true, rules: [],
   colors: {
    'editor.background': surface, 'editor.foreground': text,
    'editorLineNumber.foreground': muted, 'editorLineNumber.activeForeground': text,
    'editorCursor.foreground': accent, 'editor.selectionBackground': mixColors(surface, accent, 0.24),
    'editor.inactiveSelectionBackground': mixColors(surface, accent, 0.12),
    'editor.lineHighlightBackground': mixColors(surface, text, 0.04),
    'editorIndentGuide.background1': border, 'editorIndentGuide.activeBackground1': muted,
    'editorWidget.background': surface, 'editorWidget.foreground': text, 'editorWidget.border': border,
    'editorSuggestWidget.background': surface, 'editorSuggestWidget.foreground': text,
    'editorSuggestWidget.border': border, 'editorSuggestWidget.selectedBackground': mixColors(surface, accent, 0.14),
    'minimap.background': surface, 'focusBorder': accent,
   },
  });
  registered.set(monaco, key);
 }
 monaco.editor.setTheme(THEME_ID);
}

// Monaco is supplied by the editor's own lazy loader. Updating only its theme
// keeps every existing model, selection and undo history intact.
export function useEditorTheme() {
 const monacoRef = useRef<Monaco | null>(null);
 const beforeMount = useCallback((monaco: Monaco) => {
  monacoRef.current = monaco;
  applyEditorTheme(monaco);
 }, []);
 useEffect(() => {
  const update = () => { if (monacoRef.current) applyEditorTheme(monacoRef.current); };
  window.addEventListener('algoforge:theme', update);
  return () => window.removeEventListener('algoforge:theme', update);
 }, []);
 return { theme: THEME_ID, beforeMount };
}
