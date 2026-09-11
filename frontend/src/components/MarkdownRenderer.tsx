'use client';

import dynamic from 'next/dynamic';
import React from 'react';

// react-katex requires browser APIs, so we load it dynamically with SSR disabled.
const InlineMath = dynamic(
  () => import('react-katex').then((mod) => mod.InlineMath),
  { ssr: false },
);
const BlockMath = dynamic(
  () => import('react-katex').then((mod) => mod.BlockMath),
  { ssr: false },
);

// ---------------------------------------------------------------------------
// Inline renderer: inline math ($...$), inline code (`...`), bold, italic
// ---------------------------------------------------------------------------

function renderInline(text: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Match inline math, links, inline code, bold, italic (in priority order).
  const regex =
    /(\$\$[\s\S]*?\$\$)|(\$[^$]+\$)|(\\\([\s\S]*?\\\))|(\\\[[\s\S]*?\\\])|(\[[^\]]+\]\([^)]+\))|(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*]+\*)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = regex.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index));
    }

    const token = match[0];

    // Math may arrive using either dollar delimiters or the standard
    // TeX \\(...\\) / \\[...\\] delimiters. Handle these first so the ordinary
    // Markdown branches cannot expose the TeX source as plain text.
    if (token.startsWith('$$') && token.endsWith('$$')) {
      nodes.push(
        <div key={'bm-inline-' + match.index} className="my-3 text-center">
          <BlockMath math={token.slice(2, -2).trim()} />
        </div>,
      );
      lastIndex = match.index + token.length;
      continue;
    }
    if (token.startsWith('\\(') && token.endsWith('\\)')) {
      nodes.push(
        <InlineMath key={'im-alt-' + match.index} math={token.slice(2, -2)} />,
      );
      lastIndex = match.index + token.length;
      continue;
    }
    if (token.startsWith('\\[') && token.endsWith('\\]')) {
      nodes.push(
        <div key={'bm-alt-' + match.index} className="my-3 text-center">
          <BlockMath math={token.slice(2, -2).trim()} />
        </div>,
      );
      lastIndex = match.index + token.length;
      continue;
    }

    if (token.startsWith('$') && token.endsWith('$') && !token.startsWith('`')) {
      const expr = token.slice(1, -1);
      nodes.push(<InlineMath key={`im-${match.index}`} math={expr} />);
    } else if (token.startsWith('[') && token.includes('](') && token.endsWith(')')) {
      const close = token.indexOf('](');
      const label = token.slice(1, close);
      const href = sanitizeHref(token.slice(close + 2, -1));
      if (href) {
        nodes.push(
          <a
            key={`link-${match.index}`}
            href={href}
            className="text-forge-600 underline decoration-forge-400 underline-offset-2 hover:text-forge-700 dark:text-forge-400 dark:decoration-forge-500"
          >
            {label}
          </a>,
        );
      } else {
        nodes.push(label);
      }
    } else if (token.startsWith('`') && token.endsWith('`')) {
      nodes.push(
        <code
          key={`ic-${match.index}`}
          className="rounded bg-anvil-100 px-1.5 py-0.5 font-mono text-sm text-ember-600 dark:bg-anvil-800 dark:text-ember-400"
        >
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith('**') && token.endsWith('**')) {
      nodes.push(
        <strong key={`b-${match.index}`}>{token.slice(2, -2)}</strong>,
      );
    } else if (token.startsWith('*') && token.endsWith('*')) {
      nodes.push(<em key={`i-${match.index}`}>{token.slice(1, -1)}</em>);
    }

    lastIndex = match.index + token.length;
  }

  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex));
  }

  return nodes;
}

function sanitizeHref(href: string): string | null {
  const value = href.trim();
  if (!value) return null;
  if (/^(https?:\/\/|mailto:|\/(?!\/)|#|\.\.\/|\.\/)/i.test(value)) {
    return value;
  }
  return null;
}

function splitTableRow(row: string): string[] {
  return row
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((cell) => cell.trim());
}

function isTableSeparatorRow(row: string): boolean {
  const cells = splitTableRow(row);
  if (cells.length < 2) return false;
  return cells.every((cell) => /^:?-{3,}:?$/.test(cell.replace(/\s+/g, '')));
}

function isTableStart(lines: string[], index: number): boolean {
  if (index + 1 >= lines.length) return false;
  const header = lines[index].trim();
  const separator = lines[index + 1].trim();
  return header.includes('|') && isTableSeparatorRow(separator);
}

function renderTable(
  headers: string[],
  rows: string[][],
  key: string,
): React.ReactNode {
  const columnCount = Math.max(
    headers.length,
    ...rows.map((row) => row.length),
    0,
  );

  return (
    <div key={key} className="my-3 overflow-x-auto rounded-lg border border-anvil-200 dark:border-anvil-700">
      <table className="forge-table">
        <thead>
          <tr>
            {Array.from({ length: columnCount }).map((_, index) => (
              <th key={`th-${index}`}>
                {renderInline(headers[index] ?? '')}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => (
            <tr key={`tr-${rowIndex}`}>
              {Array.from({ length: columnCount }).map((_, colIndex) => (
                <td key={`td-${rowIndex}-${colIndex}`}>
                  {renderInline(row[colIndex] ?? '')}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function isStrongCodeStart(line: string): boolean {
  const trimmed = line.trim();
  return (
    /^#\s*(include|define|ifdef|ifndef|endif|pragma)\b/.test(trimmed) ||
    /^(using\s+namespace\s+\w+|namespace\s+\w+\s*\{)/.test(trimmed) ||
    /^(int|long|short|float|double|char|bool|void|size_t|unsigned|signed)\s+\*?\w+\s*\([^;]*\)\s*\{?$/.test(trimmed) ||
    /^(for|while|if|else\s+if|switch)\s*\(.*\)\s*\{?$/.test(trimmed)
  );
}

function looksLikeCodeLine(line: string): boolean {
  const trimmed = line.trim();
  if (!trimmed) return false;
  if (/^(\d+\.\s|[-*]\s|#{1,6}\s)/.test(trimmed)) return false;

  return (
    isStrongCodeStart(line) ||
    /^(\}|\{|\};?|}\s*else\s*\{?|else\s*\{?)$/.test(trimmed) ||
    /^(case\b.*:|default:)$/.test(trimmed) ||
    /^(return|break|continue)\b.*;?$/.test(trimmed) ||
    /^(printf|scanf|puts|gets|malloc|free|sizeof|cin|cout)\b/.test(trimmed) ||
    /^(struct|class|enum)\s+\w+\s*\{?$/.test(trimmed) ||
    /^(const\s+)?(int|long|short|float|double|char|bool|void|size_t|unsigned|signed)\b.*[;{]$/.test(trimmed) ||
    /^[A-Za-z_]\w*(\[[^\]]+\])?\s*(=|\+=|-=|\*=|\/=|%=|\+\+|--).*[;)]?$/.test(trimmed) ||
    (/[;{}]/.test(trimmed) && /[A-Za-z_]\w*/.test(trimmed) && /[=();]/.test(trimmed))
  );
}

function isIndentedCodeContinuation(line: string): boolean {
  return /^\s{2,}\S/.test(line) && !/^(\s*[-*])\s/.test(line) && !/^\s*\d+\.\s/.test(line);
}

function isLikelyCodeBlockStart(lines: string[], index: number): boolean {
  const line = lines[index];
  if (!line || !looksLikeCodeLine(line)) return false;
  if (isStrongCodeStart(line)) return true;

  let codeLikeLines = 0;
  for (let offset = 0; index + offset < lines.length && offset < 5; offset++) {
    const candidate = lines[index + offset];
    if (candidate.trim() === '') break;
    if (looksLikeCodeLine(candidate) || (codeLikeLines > 0 && isIndentedCodeContinuation(candidate))) {
      codeLikeLines++;
      continue;
    }
    break;
  }

  return codeLikeLines >= 2;
}

function detectCodeLanguage(codeLines: string[]): string {
  const code = codeLines.join('\n');
  if (/using\s+namespace|std::|cout\s*<<|cin\s*>>/.test(code)) return 'cpp';
  if (/#\s*include|printf\s*\(|scanf\s*\(|int\s+main\s*\(/.test(code)) return 'c';
  if (/public\s+static\s+void\s+main|System\.out/.test(code)) return 'java';
  if (/console\.log|function\s+\w+\s*\(|\b(const|let|var)\s+\w+\s*=/.test(code)) return 'js';
  return '';
}

function renderCodeBlock(
  codeLines: string[],
  lang: string,
  key: string,
): React.ReactNode {
  return (
    <pre
      key={key}
      className="my-3 overflow-x-auto rounded-lg bg-anvil-900 p-4 text-sm text-anvil-100"
    >
      <code data-lang={lang}>{codeLines.join('\n')}</code>
    </pre>
  );
}

// ---------------------------------------------------------------------------
// Block-level Markdown → JSX renderer with KaTeX math support
// ---------------------------------------------------------------------------

/**
 * Lightweight Markdown-to-JSX renderer with KaTeX math support.
 *
 * Supports:
 *  - Block math:  $$ ... $$
 *  - Inline math: $ ... $
 *  - Headers (#, ##, ###, ####)
 *  - Bold / italic
 *  - Code blocks (``` ... ```)
 *  - Unfenced C/C++-style code blocks from legacy quiz content
 *  - Inline code (`)
 *  - Ordered lists (1. / 2. / ...)
 *  - Unordered lists (- / *)
 *  - Blank-line paragraph separation
 */
function renderMarkdown(md: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  const lines = md.split('\n');
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();
    const trimmedStart = line.trimStart();

    // ---- Block math ----
    if (trimmed.startsWith('$$')) {
      const mathLines: string[] = [];
      const rest = trimmed.slice(2);
      const sameLineClose = rest.indexOf('$$');
      if (sameLineClose >= 0) {
        const expr = rest.slice(0, sameLineClose);
        nodes.push(
          <div key={`bm-${i}`} className="my-3 text-center">
            <BlockMath math={expr.trim()} />
          </div>,
        );
        i++;
        continue;
      }
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('$$')) {
        mathLines.push(lines[i]);
        i++;
      }
      nodes.push(
        <div key={`bm-${i}`} className="my-3 text-center">
          <BlockMath math={mathLines.join('\n').trim()} />
        </div>,
      );
      i++;
      continue;
    }

    // ---- Alternate block math delimiter (\\[ ... \\]) ----
    if (trimmed.startsWith('\\[')) {
      const mathLines: string[] = [];
      const rest = trimmed.slice(2);
      const sameLineClose = rest.indexOf('\\]');
      if (sameLineClose >= 0) {
        nodes.push(
          <div key={`bm-alt-${i}`} className="my-3 text-center">
            <BlockMath math={rest.slice(0, sameLineClose).trim()} />
          </div>,
        );
        i++;
        continue;
      }
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('\\]')) {
        mathLines.push(lines[i]);
        i++;
      }
      nodes.push(
        <div key={`bm-alt-${i}`} className="my-3 text-center">
          <BlockMath math={mathLines.join('\n').trim()} />
        </div>,
      );
      if (i < lines.length) i++;
      continue;
    }

    // ---- Horizontal rule ----
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      nodes.push(
        <hr key={`hr-${i}`} className="my-4 border-anvil-200 dark:border-anvil-700" />,
      );
      i++;
      continue;
    }

    // ---- Blockquote ----
    if (trimmed.startsWith('>')) {
      const quoteLines: string[] = [];
      while (i < lines.length && lines[i].trim().startsWith('>')) {
        quoteLines.push(lines[i].replace(/^\s*>\s?/, ''));
        i++;
      }
      nodes.push(
        <blockquote
          key={`quote-${i}`}
          className="my-3 border-l-4 border-anvil-300 pl-4 text-anvil-700 dark:border-anvil-600 dark:text-anvil-300"
        >
          {renderMarkdown(quoteLines.join('\n'))}
        </blockquote>,
      );
      continue;
    }

    // ---- Table ----
    if (isTableStart(lines, i)) {
      const headers = splitTableRow(lines[i]);
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length) {
        const current = lines[i].trim();
        if (
          current === '' ||
          current.startsWith('>') ||
          current.startsWith('$$') ||
          current.startsWith('```') ||
          current.startsWith('#') ||
          !current.includes('|')
        ) {
          break;
        }
        rows.push(splitTableRow(lines[i]));
        i++;
      }
      nodes.push(renderTable(headers, rows, `table-${i}`));
      continue;
    }

    // ---- Code block ----
    if (trimmed.startsWith('```')) {
      const lang = trimmed.slice(3).trim();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('```')) {
        codeLines.push(lines[i]);
        i++;
      }
      nodes.push(renderCodeBlock(codeLines, lang, `code-${i}`));
      i++;
      continue;
    }

    // ---- Unfenced code block from legacy quiz content ----
    if (isLikelyCodeBlockStart(lines, i)) {
      const codeLines: string[] = [];
      const blankLines: string[] = [];
      while (i < lines.length) {
        const current = lines[i];
        if (current.trim() === '') {
          if (
            i + 1 < lines.length &&
            (looksLikeCodeLine(lines[i + 1]) || isIndentedCodeContinuation(lines[i + 1]))
          ) {
            blankLines.push(current);
            i++;
            continue;
          }
          break;
        }

        if (looksLikeCodeLine(current) || (codeLines.length > 0 && isIndentedCodeContinuation(current))) {
          codeLines.push(...blankLines, current);
          blankLines.length = 0;
          i++;
          continue;
        }

        break;
      }

      nodes.push(renderCodeBlock(codeLines, detectCodeLanguage(codeLines), `auto-code-${i}`));
      continue;
    }

    // ---- Headers (h1-h4) ----
    if (trimmedStart.startsWith('#### ')) {
      nodes.push(
        <h4
          key={`h4-${i}`}
          className="mb-2 mt-4 text-sm font-bold text-anvil-900 dark:text-anvil-100"
        >
          {renderInline(trimmedStart.slice(5))}
        </h4>,
      );
      i++;
      continue;
    }
    if (trimmedStart.startsWith('### ')) {
      nodes.push(
        <h3
          key={`h3-${i}`}
          className="mb-2 mt-5 text-base font-bold text-anvil-900 dark:text-anvil-100"
        >
          {renderInline(trimmedStart.slice(4))}
        </h3>,
      );
      i++;
      continue;
    }
    if (trimmedStart.startsWith('## ')) {
      nodes.push(
        <h2
          key={`h2-${i}`}
          className="mb-2 mt-6 text-lg font-bold text-anvil-900 dark:text-anvil-100"
        >
          {renderInline(trimmedStart.slice(3))}
        </h2>,
      );
      i++;
      continue;
    }
    if (trimmedStart.startsWith('# ')) {
      nodes.push(
        <h1
          key={`h1-${i}`}
          className="mb-3 mt-6 text-xl font-bold text-anvil-900 dark:text-anvil-50"
        >
          {renderInline(trimmedStart.slice(2))}
        </h1>,
      );
      i++;
      continue;
    }

    // ---- Ordered list ----
    if (/^\s*\d+\.\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+\.\s/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+\.\s/, ''));
        i++;
      }
      nodes.push(
        <ol key={`ol-${i}`} className="my-2 list-decimal pl-6 text-anvil-800 dark:text-anvil-200">
          {items.map((item, idx) => (
            <li key={idx} className="mb-1">
              {renderInline(item)}
            </li>
          ))}
        </ol>,
      );
      continue;
    }

    // ---- Unordered list ----
    if (/^(\s*[-*])\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^(\s*[-*])\s/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*]\s/, ''));
        i++;
      }
      nodes.push(
        <ul key={`ul-${i}`} className="my-2 list-disc pl-6 text-anvil-800 dark:text-anvil-200">
          {items.map((item, idx) => (
            <li key={idx} className="mb-1">
              {renderInline(item)}
            </li>
          ))}
        </ul>,
      );
      continue;
    }

    // ---- Blank line ----
    if (line.trim() === '') {
      i++;
      continue;
    }

    // ---- Paragraph (collect consecutive non-special lines) ----
    const paraLines: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !lines[i].trim().startsWith('$$') &&
      !lines[i].trim().startsWith('\\[') &&
      !lines[i].trim().startsWith('```') &&
      !lines[i].trim().startsWith('>') &&
      !lines[i].trim().startsWith('|') &&
      !isLikelyCodeBlockStart(lines, i) &&
      !lines[i].trimStart().startsWith('# ') &&
      !lines[i].trimStart().startsWith('## ') &&
      !lines[i].trimStart().startsWith('### ') &&
      !lines[i].trimStart().startsWith('#### ') &&
      !/^(\s*[-*])\s/.test(lines[i]) &&
      !/^\s*\d+\.\s/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    if (paraLines.length > 0) {
      nodes.push(
        <p
          key={`p-${i}`}
          className="my-2 leading-relaxed text-anvil-800 dark:text-anvil-200"
        >
          {renderInline(paraLines.join(' '))}
        </p>,
      );
    }
  }

  return nodes;
}

// ---------------------------------------------------------------------------
// Exported component
// ---------------------------------------------------------------------------

interface MarkdownRendererProps {
  content: string;
  className?: string;
}

export default function MarkdownRenderer({ content, className }: MarkdownRendererProps) {
  if (!content) return null;
  return (
    <div className={className ?? 'markdown-content'}>
      {renderMarkdown(content)}
    </div>
  );
}
