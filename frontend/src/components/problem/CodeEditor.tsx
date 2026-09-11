'use client';

import { useEditorTheme } from '@/hooks/useEditorTheme';

import { useState, useCallback } from 'react';
import Editor from '@monaco-editor/react';
import { Copy, Check, Map } from 'lucide-react';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Language mapping (our value -> Monaco language id)
// ---------------------------------------------------------------------------

const LANGUAGE_MAP: Record<string, string> = {
  cpp: 'cpp',
  c: 'c',
  java: 'java',
  python: 'python',
  go: 'go',
  rust: 'rust',
};

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface CodeEditorProps {
  code: string;
  language: string;
  readOnly?: boolean;
  onChange?: (value: string) => void;
  height?: string | number;
}

// ---------------------------------------------------------------------------
// CodeEditor
// ---------------------------------------------------------------------------

export default function CodeEditor({
  code,
  language,
  readOnly = false,
  onChange,
  height = '400px',
}: CodeEditorProps) {
  const editorTheme = useEditorTheme();
  const [copied, setCopied] = useState(false);
  const [showMinimap, setShowMinimap] = useState(false);

  const monacoLang = LANGUAGE_MAP[language] ?? language;

  // ---- Copy to clipboard ----
  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback: select-and-copy not supported in all envs; silently fail
    }
  }, [code]);

  // ---- Editor change ----
  const handleEditorChange = useCallback(
    (value: string | undefined) => {
      if (onChange && value !== undefined) {
        onChange(value);
      }
    },
    [onChange],
  );

  return (
    <div className="overflow-hidden rounded-xl border border-anvil-200 dark:border-anvil-700">
      {/* Toolbar */}
      <div className="flex items-center justify-between border-b border-anvil-200 bg-anvil-900 px-3 py-1.5 dark:border-anvil-700" style={{ backgroundColor: 'var(--ds)' }}>
        <span className="text-xs font-medium uppercase tracking-wider text-anvil-400" style={{ color: 'var(--dm)' }}>
          {monacoLang}
        </span>

        <div className="flex items-center gap-1">
          {/* Minimap toggle */}
          <button
            type="button"
            onClick={() => setShowMinimap((prev) => !prev)}
            className={cn(
              'rounded p-1.5 transition-colors',
              showMinimap ? 'bg-forge-100 text-forge-700 dark:bg-forge-900 dark:text-forge-300' : 'text-anvil-500 hover:text-anvil-900 dark:hover:text-anvil-100',
            )}
            aria-label="切换代码缩略图"
            title="切换代码缩略图"
          >
            <Map className="h-3.5 w-3.5" />
          </button>

          {/* Copy button */}
          <button
            type="button"
            onClick={handleCopy}
            className={cn('rounded p-1.5 text-anvil-500 transition-colors', 'hover:text-anvil-900 dark:hover:text-anvil-100')}
            aria-label="复制代码"
            title="复制代码"
          >
            {copied ? (
              <Check className="h-3.5 w-3.5 text-success-400" />
            ) : (
              <Copy className="h-3.5 w-3.5" />
            )}
          </button>
        </div>
      </div>

      {/* Monaco Editor */}
      <Editor
        height={height}
        language={monacoLang}
        value={code}
        theme={editorTheme.theme}
                beforeMount={editorTheme.beforeMount}
        onChange={handleEditorChange}
        options={{
          readOnly,
          lineNumbers: 'on',
          minimap: { enabled: showMinimap },
          scrollBeyondLastLine: false,
          fontSize: 14,
          fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
          tabSize: 4,
          automaticLayout: true,
          padding: { top: 12, bottom: 12 },
          wordWrap: 'on',
          renderLineHighlight: readOnly ? 'none' : 'line',
          domReadOnly: readOnly,
        }}
        loading={
          <div className="flex items-center justify-center bg-anvil-900 py-12 text-anvil-400" style={{ backgroundColor: 'var(--dp)', color: 'var(--dm)' }}>
            正在加载编辑器…
          </div>
        }
      />
    </div>
  );
}
